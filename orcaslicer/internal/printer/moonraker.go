// Package printer talks to the Kobra 3's Moonraker API: status, uploading and starting a G-code
// file, and pause, resume and cancel. Moonraker has no auth on the LAN.
package printer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrDisabled is returned when no printer URL is configured.
var ErrDisabled = errors.New("printing is disabled (PRINTER_URL is not set)")

// Temp is a heater's temperature and target in °C.
type Temp struct {
	Temp   float64 `json:"temp"`
	Target float64 `json:"target"`
}

// Status is the printer as the UI shows it.
type Status struct {
	Online        bool    `json:"online"`
	State         string  `json:"state"` // standby, printing, paused, complete, cancelled, error, or offline
	Filename      string  `json:"filename"`
	Progress      float64 `json:"progress"` // 0..1
	PrintDuration int     `json:"print_duration"`
	ETA           int     `json:"eta_s"`
	Extruder      Temp    `json:"extruder"`
	Bed           Temp    `json:"bed"`
	Message       string  `json:"message"`
}

// Client is a Moonraker connection. Status is cached for a couple of seconds.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	ttl     time.Duration

	mu     sync.Mutex
	cached Status
	at     time.Time
}

// New returns a client for baseURL (empty means printing is disabled).
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 5 * time.Second}, ttl: 2 * time.Second}
}

// Enabled reports whether a printer is configured.
func (c *Client) Enabled() bool { return c.BaseURL != "" }

// Status queries the printer, or returns the cached answer from the last two seconds. An
// unreachable printer is not an error: it is reported as offline.
func (c *Client) Status(ctx context.Context) Status {
	if !c.Enabled() {
		return Status{State: "offline", Message: "printing is disabled"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < c.ttl && !c.at.IsZero() {
		return c.cached
	}
	st := c.query(ctx)
	c.cached, c.at = st, time.Now()
	return st
}

func (c *Client) query(ctx context.Context) Status {
	const q = "/printer/objects/query?print_stats&display_status&virtual_sdcard&extruder&heater_bed"
	var body struct {
		Result struct {
			Status struct {
				PrintStats struct {
					State         string  `json:"state"`
					Filename      string  `json:"filename"`
					PrintDuration float64 `json:"print_duration"`
					Message       string  `json:"message"`
				} `json:"print_stats"`
				DisplayStatus struct {
					Progress float64 `json:"progress"`
					Message  string  `json:"message"`
				} `json:"display_status"`
				VirtualSDCard struct {
					Progress float64 `json:"progress"`
				} `json:"virtual_sdcard"`
				Extruder  mrTemp `json:"extruder"`
				HeaterBed mrTemp `json:"heater_bed"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := c.getJSON(ctx, q, &body); err != nil {
		return Status{State: "offline", Message: err.Error()}
	}
	s := body.Result.Status
	st := Status{
		Online:        true,
		State:         s.PrintStats.State,
		Filename:      s.PrintStats.Filename,
		Progress:      s.DisplayStatus.Progress,
		PrintDuration: int(s.PrintStats.PrintDuration),
		Extruder:      Temp{Temp: s.Extruder.Temperature, Target: s.Extruder.Target},
		Bed:           Temp{Temp: s.HeaterBed.Temperature, Target: s.HeaterBed.Target},
		Message:       s.PrintStats.Message,
	}
	if st.Progress == 0 {
		st.Progress = s.VirtualSDCard.Progress
	}
	if st.State == "" {
		st.State = "standby"
	}
	if st.State == "printing" && st.Progress > 0.01 && st.Progress < 1 {
		st.ETA = int(float64(st.PrintDuration) * (1 - st.Progress) / st.Progress)
	}
	if st.Message == "" {
		st.Message = s.DisplayStatus.Message
	}
	return st
}

// mrTemp is how Moonraker names a heater's values.
type mrTemp struct {
	Temperature float64 `json:"temperature"`
	Target      float64 `json:"target"`
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("moonraker: %s", res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Upload stores a G-code file in Moonraker's gcodes folder and starts printing it.
func (c *Client) Upload(ctx context.Context, filename string, gcode io.Reader) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("root", "gcodes")
	mw.WriteField("print", "true")
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, gcode); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/server/files/upload", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{Timeout: 5 * time.Minute} // a G-code file can be tens of MB
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	if res.StatusCode >= 300 {
		return fmt.Errorf("moonraker upload: %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	c.invalidate()
	return nil
}

// Action sends pause, resume or cancel.
func (c *Client) Action(ctx context.Context, action string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	switch action {
	case "pause", "resume", "cancel":
	default:
		return fmt.Errorf("unknown printer action %q", action)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/printer/print/"+action, nil)
	if err != nil {
		return err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("moonraker %s: %s", action, res.Status)
	}
	c.invalidate()
	return nil
}

func (c *Client) invalidate() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}
