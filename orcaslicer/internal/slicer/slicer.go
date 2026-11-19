// Package slicer runs the OrcaSlicer command line on a plate. Slices run one at a time, in the
// order they were asked for, on a single worker goroutine. The inputs are written as a 3MF with
// every object already at its bed position, and flattened presets (see internal/profiles).
package slicer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/fsutil"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
)

// Slice states.
const (
	StatusIdle    = "idle"
	StatusQueued  = "queued"
	StatusSlicing = "slicing"
	StatusDone    = "done"
	StatusError   = "error"
)

const logTail = 4096

// Result is a slice as the API returns it. Stale is not stored: the app sets it by comparing the
// stored hash with the plate's current one.
type Result struct {
	Status        string   `json:"status"`
	Started       string   `json:"started,omitempty"`
	Finished      string   `json:"finished,omitempty"`
	Error         string   `json:"error,omitempty"`
	Log           string   `json:"log,omitempty"`
	Stale         bool     `json:"stale"`
	EstimatedTime int      `json:"estimated_time_s"`
	FilamentMM    float64  `json:"filament_mm"`
	FilamentG     float64  `json:"filament_g"`
	Layers        int      `json:"layers"`
	MaxZ          float64  `json:"max_z"`
	GcodeSize     int64    `json:"gcode_size"`
	GcodeName     string   `json:"gcode_name"`
	BedType       string   `json:"bed_type"`
	BedTemp       string   `json:"bed_temp"`
	BedTempFirst  string   `json:"bed_temp_initial_layer"`
	Warnings      []string `json:"warnings"`
}

// record is what slice.json holds: the result and the plate hash it was made from.
type record struct {
	Result
	Hash string `json:"hash"`
}

// Options configure a Slicer.
type Options struct {
	Binary   string        // orca-slicer, or the AppRun launcher next to it
	DataDir  string        // Orca's config (--datadir) and scratch space live under it
	Timeout  time.Duration // per slice
	Profiles *profiles.Library
	Models   *models.Store
	Plates   *plates.Store
	Version  string // OrcaSlicer version, part of the plate hash
	// OnChange is called after every status change (queued, slicing, done, error).
	OnChange func(plateID string, r Result)
}

// Slicer is the slicing queue and worker.
type Slicer struct {
	opt  Options
	jobs chan string
	mu   sync.Mutex // serialises slice.json writes
}

// New returns a slicer. Call Recover once, then Run in a goroutine.
func New(opt Options) *Slicer {
	return &Slicer{opt: opt, jobs: make(chan string, 64)}
}

// SetOnChange sets the status-change callback (the app needs the slicer before it can wire it).
func (s *Slicer) SetOnChange(fn func(plateID string, r Result)) { s.opt.OnChange = fn }

// Recover marks slices that were queued or running when the server stopped as errors.
func (s *Slicer) Recover() error {
	list, err := s.opt.Plates.List()
	if err != nil {
		return err
	}
	for _, p := range list {
		r, hash, err := s.read(p.ID)
		if err != nil {
			continue
		}
		if r.Status == StatusQueued || r.Status == StatusSlicing {
			r.Status, r.Error = StatusError, "interrupted: the server restarted during the slice"
			r.Finished = stamp(time.Now())
			s.write(p.ID, r, hash)
		}
	}
	return os.MkdirAll(filepath.Join(s.opt.DataDir, "orca-home"), 0o755)
}

// Run works through the queue until ctx is done.
func (s *Slicer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.jobs:
			s.run(ctx, id)
		}
	}
}

// Enqueue asks for a slice of a plate. A slice already queued or running is returned as it is.
func (s *Slicer) Enqueue(id string) (Result, error) {
	if !s.opt.Plates.Exists(id) {
		return Result{}, plates.ErrNotFound
	}
	s.mu.Lock()
	cur, _, _ := s.readLocked(id)
	if cur.Status == StatusQueued || cur.Status == StatusSlicing {
		s.mu.Unlock()
		return cur, nil
	}
	res := Result{Status: StatusQueued, Warnings: []string{}}
	s.writeLocked(id, res, "")
	s.mu.Unlock()
	s.notify(id, res)
	select {
	case s.jobs <- id:
	default:
		res = Result{Status: StatusError, Error: "too many slices are waiting; try again later", Warnings: []string{}}
		s.update(id, res, "")
		return res, errors.New(res.Error)
	}
	return res, nil
}

// Get returns the stored slice of a plate and the hash it was made from ("" if none).
func (s *Slicer) Get(id string) (Result, string, error) {
	if !s.opt.Plates.Exists(id) {
		return Result{}, "", plates.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id)
}

func (s *Slicer) read(id string) (Result, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id)
}

func (s *Slicer) readLocked(id string) (Result, string, error) {
	var rec record
	if err := fsutil.ReadJSON(filepath.Join(s.opt.Plates.Dir(id), "slice.json"), &rec); err != nil {
		if os.IsNotExist(err) {
			return Result{Status: StatusIdle, Warnings: []string{}}, "", nil
		}
		return Result{}, "", err
	}
	if rec.Warnings == nil {
		rec.Warnings = []string{}
	}
	return rec.Result, rec.Hash, nil
}

func (s *Slicer) write(id string, r Result, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(id, r, hash)
}

func (s *Slicer) writeLocked(id string, r Result, hash string) {
	if !s.opt.Plates.Exists(id) {
		return
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	fsutil.WriteJSON(filepath.Join(s.opt.Plates.Dir(id), "slice.json"), record{Result: r, Hash: hash})
}

func (s *Slicer) update(id string, r Result, hash string) {
	s.write(id, r, hash)
	s.notify(id, r)
}

func (s *Slicer) notify(id string, r Result) {
	if s.opt.OnChange != nil {
		s.opt.OnChange(id, r)
	}
}

func (s *Slicer) run(ctx context.Context, id string) {
	started := time.Now()
	s.update(id, Result{Status: StatusSlicing, Started: stamp(started), Warnings: []string{}}, "")
	res, hash := s.slice(ctx, id)
	res.Started = stamp(started)
	res.Finished = stamp(time.Now())
	s.update(id, res, hash)
}

// slice does the work of one slice and returns its result and the plate hash it used.
func (s *Slicer) slice(ctx context.Context, id string) (Result, string) {
	fail := func(hash string, err error) (Result, string) {
		return Result{Status: StatusError, Error: err.Error(), Warnings: []string{}}, hash
	}
	p, err := s.opt.Plates.Get(id)
	if err != nil {
		return fail("", err)
	}
	hash, err := plates.Hash(p, s.opt.Models, s.opt.Version)
	if err != nil {
		return fail("", err)
	}
	if len(p.Objects) == 0 {
		return fail(hash, errors.New("the plate has no objects"))
	}
	bed, err := s.opt.Profiles.Bed(p.Machine)
	if err != nil {
		return fail(hash, err)
	}

	// place every object: world-space triangles for the 3MF, boxes for the checks
	objects := make([]Object, 0, len(p.Objects))
	boxes := map[string]geom.Box{}
	for _, o := range p.Objects {
		tris, m, err := s.opt.Models.Mesh(o.Model)
		if err != nil {
			return fail(hash, fmt.Errorf("object %s: %w", o.ID, err))
		}
		tr, box := plates.PlaceObject(o, tris)
		world := make([]geom.Triangle, len(tris))
		for i, t := range tris {
			for k, v := range t {
				world[i][k] = tr.Apply(v)
			}
		}
		name := o.Name
		if name == "" {
			name = m.Name
		}
		objects = append(objects, Object{Name: name, Tris: world})
		boxes[o.ID] = box
	}
	warnings, blocking := plates.Check(bed, p.Objects, boxes)
	if warnings == nil {
		warnings = []string{}
	}
	if blocking {
		return Result{Status: StatusError, Warnings: warnings,
			Error: "some objects are not on the print bed: move them onto it before slicing"}, hash
	}

	machine, process, filament, err := s.presets(p)
	if err != nil {
		return fail(hash, err)
	}

	work := filepath.Join(s.opt.Plates.Dir(id), "work")
	os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return fail(hash, err)
	}
	defer os.RemoveAll(work)
	machinePath := filepath.Join(work, "machine.json")
	processPath := filepath.Join(work, "process.json")
	filamentPath := filepath.Join(work, "filament.json")
	threeMF := filepath.Join(work, "plate.3mf")
	out := filepath.Join(work, "out")
	for path, v := range map[string]map[string]any{machinePath: machine, processPath: process, filamentPath: filament} {
		if err := fsutil.WriteJSON(path, v); err != nil {
			return fail(hash, err)
		}
	}
	if err := WriteThreeMF(threeMF, objects); err != nil {
		return fail(hash, err)
	}

	args := []string{
		"--datadir", filepath.Join(s.opt.DataDir, "orca-config"),
		"--slice", "0", // every plate in the file (there is one)
		"--arrange", "0", // keep the positions we computed
		"--load-settings", machinePath + ";" + processPath,
		"--load-filaments", filamentPath,
		"--outputdir", out,
		threeMF,
	}
	log, runErr := s.runOrca(ctx, work, args)
	res := Result{Warnings: warnings, Log: log}
	rr := readOrcaResult(out)
	for _, sp := range rr.SlicedPlates {
		if sp.WarningMessage != "" {
			res.Warnings = append(res.Warnings, sp.WarningMessage)
		}
	}
	if runErr != nil {
		res.Status = StatusError
		res.Error = orcaError(rr, runErr)
		return res, hash
	}
	gcodes, _ := filepath.Glob(filepath.Join(out, "*.gcode"))
	if len(gcodes) == 0 {
		res.Status = StatusError
		res.Error = orcaError(rr, errors.New("OrcaSlicer wrote no G-code"))
		return res, hash
	}
	f, err := os.Open(gcodes[0])
	if err != nil {
		return fail(hash, err)
	}
	stats, err := ParseGcode(f)
	info, _ := f.Stat()
	f.Close()
	if err != nil {
		res.Status = StatusError
		res.Error = err.Error()
		return res, hash
	}

	// keep the G-code and the 3MF next to the plate
	dir := s.opt.Plates.Dir(id)
	if err := os.Rename(threeMF, filepath.Join(dir, "plate.3mf")); err != nil {
		return fail(hash, err)
	}
	if err := os.Rename(gcodes[0], filepath.Join(dir, "plate.gcode")); err != nil {
		return fail(hash, err)
	}
	layerH, _ := parseLayerHeight(profiles.Str(process, "layer_height"))
	res.Status = StatusDone
	res.EstimatedTime = stats.Seconds
	res.FilamentMM = stats.FilamentMM
	res.FilamentG = stats.FilamentG
	res.Layers = stats.Layers
	res.MaxZ = stats.MaxZ
	res.GcodeSize = info.Size()
	res.GcodeName = gcodeName(p.Name, profiles.Str(filament, "filament_type"), layerH, stats.Seconds)
	res.BedType = profiles.Str(process, "curr_bed_type")
	key := plates.BedTemps[res.BedType]
	res.BedTemp = profiles.Str(filament, key)
	res.BedTempFirst = profiles.Str(filament, key+"_initial_layer")
	return res, hash
}

// presets returns the flattened machine, process and filament settings with the plate's
// overrides applied. Each override goes to the preset that defines its key.
func (s *Slicer) presets(p plates.Plate) (machine, process, filament map[string]any, err error) {
	if machine, err = s.opt.Profiles.Flatten(profiles.Machine, p.Machine); err != nil {
		return
	}
	if process, err = s.opt.Profiles.Flatten(profiles.Process, p.Process); err != nil {
		return
	}
	if filament, err = s.opt.Profiles.Flatten(profiles.Filament, p.Filament); err != nil {
		return
	}
	for k, v := range p.Overrides {
		switch {
		case profiles.Has(process, k):
			profiles.Set(process, k, v)
		case profiles.Has(filament, k):
			profiles.Set(filament, k, v)
		default:
			err = fmt.Errorf("override %q: no such process or filament setting", k)
			return
		}
	}
	// the bed type follows the printer profile (see plates.EffectiveBed); OrcaSlicer takes the bed
	// temperature from the filament's setting for that bed type
	process["curr_bed_type"] = plates.EffectiveBed(machine, p.Bed)
	// OrcaSlicer reads these as user presets built on a system preset
	for _, m := range []map[string]any{machine, process, filament} {
		m["from"] = "system"
	}
	return
}

func (s *Slicer) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "DISPLAY", "WAYLAND_DISPLAY", "HOME":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+filepath.Join(s.opt.DataDir, "orca-home"))
}

// runOrca runs the command with the timeout and returns the last part of its output.
func (s *Slicer) runOrca(ctx context.Context, dir string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opt.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.opt.Binary, args...)
	cmd.Dir = dir
	cmd.Env = s.env()
	tail := &tailBuffer{max: logTail}
	cmd.Stdout, cmd.Stderr = tail, tail
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return tail.String(), fmt.Errorf("slicing took longer than %s and was stopped", s.opt.Timeout)
	}
	return tail.String(), err
}

// orcaResult is the result.json OrcaSlicer writes next to the G-code.
type orcaResult struct {
	ReturnCode   int    `json:"return_code"`
	ErrorString  string `json:"error_string"`
	SlicedPlates []struct {
		WarningMessage string `json:"warning_message"`
	} `json:"sliced_plates"`
}

func readOrcaResult(dir string) orcaResult {
	var rr orcaResult
	fsutil.ReadJSON(filepath.Join(dir, "result.json"), &rr)
	return rr
}

func orcaError(rr orcaResult, fallback error) string {
	if rr.ErrorString != "" && rr.ErrorString != "Success." {
		return "OrcaSlicer: " + rr.ErrorString
	}
	return "OrcaSlicer failed: " + fallback.Error()
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buf
	if len(b) > t.max {
		b = b[len(b)-t.max:]
	}
	return string(b)
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9+-]+`)

// gcodeName builds the download name, for example xmas-light-case_PETG_0.20_44m45s.gcode.
func gcodeName(plate, filamentType string, layerHeight float64, seconds int) string {
	ft := unsafeName.ReplaceAllString(filamentType, "")
	if ft == "" {
		ft = "filament"
	}
	return fmt.Sprintf("%s_%s_%.2f_%s.gcode", fsutil.Slug(plate), ft, layerHeight, DurationLabel(seconds))
}

func parseLayerHeight(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
