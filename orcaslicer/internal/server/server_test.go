package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testapp"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	a := testapp.NewApp(t)
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ui</html>")}}
	var f fs.FS = web
	srv := httptest.NewServer(New(a, http.NotFoundHandler(), f).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func TestAPIBasics(t *testing.T) {
	srv := testServer(t)

	res, _ := http.Get(srv.URL + "/api/config")
	var cfg struct {
		Bed struct {
			Width     float64 `json:"width"`
			MaxHeight float64 `json:"max_height"`
		} `json:"bed"`
		CuratedSettings []map[string]any `json:"curated_settings"`
		PrinterEnabled  bool             `json:"printer_enabled"`
	}
	json.NewDecoder(res.Body).Decode(&cfg)
	res.Body.Close()
	if cfg.Bed.Width != 255 || cfg.Bed.MaxHeight != 260 || cfg.PrinterEnabled || len(cfg.CuratedSettings) == 0 {
		t.Fatalf("config = %+v", cfg)
	}

	// errors are JSON {"error": ...} with the right status
	res, _ = http.Get(srv.URL + "/api/plates/nope")
	var e map[string]string
	json.NewDecoder(res.Body).Decode(&e)
	res.Body.Close()
	if res.StatusCode != 404 || e["error"] == "" {
		t.Fatalf("missing plate: %d %v", res.StatusCode, e)
	}

	body := `{"name":"Case","machine":"No Such Machine"}`
	res, _ = http.Post(srv.URL+"/api/plates", "application/json", strings.NewReader(body))
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("bad preset: %d", res.StatusCode)
	}

	res, _ = http.Post(srv.URL+"/api/plates", "application/json", strings.NewReader(`{"name":"Case"}`))
	var p struct {
		ID string `json:"id"`
	}
	json.NewDecoder(res.Body).Decode(&p)
	res.Body.Close()
	if res.StatusCode != 201 || p.ID == "" {
		t.Fatalf("create plate: %d %+v", res.StatusCode, p)
	}
	res, _ = http.Get(srv.URL + "/api/plates/" + p.ID + "/slice")
	var sl map[string]any
	json.NewDecoder(res.Body).Decode(&sl)
	res.Body.Close()
	if sl["status"] != "idle" {
		t.Fatalf("slice before any slice = %v", sl)
	}

	res, _ = http.Get(srv.URL + "/api/printer")
	var pr map[string]any
	json.NewDecoder(res.Body).Decode(&pr)
	res.Body.Close()
	if pr["online"] != false {
		t.Fatalf("printer = %v", pr)
	}
	res, _ = http.Get(srv.URL + "/api/profiles?machine=Anycubic%20Kobra%203%200.4%20nozzle")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("profiles: %d", res.StatusCode)
	}
}

func TestUploadAndPatchObject(t *testing.T) {
	srv := testServer(t)
	var buf bytes.Buffer
	mw := newMultipart(&buf, "file", "cube.stl", testkit.ASCIICube([3]float64{}, 10))
	res, _ := http.Post(srv.URL+"/api/models", mw.contentType, &buf)
	var m struct {
		ID string `json:"id"`
	}
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	if res.StatusCode != 201 || m.ID == "" {
		t.Fatalf("upload: %d %+v", res.StatusCode, m)
	}
	res, _ = http.Post(srv.URL+"/api/plates", "application/json", strings.NewReader(`{"name":"P"}`))
	var p struct {
		ID string `json:"id"`
	}
	json.NewDecoder(res.Body).Decode(&p)
	res.Body.Close()
	res, _ = http.Post(srv.URL+"/api/plates/"+p.ID+"/objects", "application/json", strings.NewReader(`{"model":"`+m.ID+`","x":100,"y":50}`))
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("add object: %d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/plates/"+p.ID+"/objects/o1", strings.NewReader(`{"rotation":[0,0,90],"scale":2}`))
	res, _ = http.DefaultClient.Do(req)
	var view struct {
		Objects []struct {
			X        float64       `json:"x"`
			Rotation [3]float64    `json:"rotation"`
			Scale    float64       `json:"scale"`
			Bounds   [2][3]float64 `json:"bounds"`
		} `json:"objects"`
	}
	json.NewDecoder(res.Body).Decode(&view)
	res.Body.Close()
	if res.StatusCode != 200 || view.Objects[0].Scale != 2 || view.Objects[0].X != 100 || view.Objects[0].Rotation[2] != 90 {
		t.Fatalf("patch: %d %+v", res.StatusCode, view)
	}
	// 10 mm cube scaled 2: bounds span 20 mm
	b := view.Objects[0].Bounds
	if d := b[1][0] - b[0][0]; d < 19.9 || d > 20.1 {
		t.Fatalf("bounds width after rotate 90 & scale 2 = %v", d)
	}
}

type multipartBody struct{ contentType string }

func newMultipart(buf *bytes.Buffer, field, name string, data []byte) multipartBody {
	w := multipartWriter(buf)
	fw, _ := w.CreateFormFile(field, name)
	fw.Write(data)
	w.Close()
	return multipartBody{contentType: w.FormDataContentType()}
}

var _ = multipart.NewWriter

func multipartWriter(buf *bytes.Buffer) *multipart.Writer { return multipart.NewWriter(buf) }
