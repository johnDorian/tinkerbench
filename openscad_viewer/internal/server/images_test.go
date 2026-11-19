package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/app"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/measure"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// barPNG draws a dark bar on a light background: a part with one step, enough to measure.
func barPNG(t *testing.T) []byte {
	t.Helper()
	const w, h = 400, 200
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			v := uint8(235)
			if x >= 50 && x < 350 && y >= 80 && y < 120 {
				v = 25
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("tag", "a test part", "cube(1);\n", "test"); err != nil {
		t.Fatal(err)
	}
	a := app.New(store, render.New("openscad-not-used", 0, 1), events.NewHub())
	web := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ui")}}
	srv := httptest.NewServer(New(a, http.NotFoundHandler(), web, Config{}).Routes())
	t.Cleanup(srv.Close)
	return srv
}

// upload posts files as a browser's file picker would.
func upload(t *testing.T, url string, names []string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, n := range names {
		w, err := mw.CreateFormFile("file", n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	mw.Close()
	res, err := http.Post(url, mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decode[T any](t *testing.T, res *http.Response, want int) T {
	t.Helper()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s: status %d, want %d: %s", res.Request.URL.Path, res.StatusCode, want, b)
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decoding %s: %v (%s)", res.Request.URL.Path, err, b)
	}
	return v
}

func TestImageEndpoints(t *testing.T) {
	srv := newTestServer(t)
	base := srv.URL + "/api/projects/tag"
	data := barPNG(t)

	// Two files in one upload; the second keeps the first rather than replacing it.
	got := decode[[]project.Image](t, upload(t, base+"/images", []string{"my scan.png", "my scan.png"}, data), 201)
	if len(got) != 2 || got[0].Name != "my-scan.png" || got[1].Name != "my-scan-2.png" {
		t.Fatalf("uploaded %+v", got)
	}
	if got[0].Width != 400 || got[0].Height != 200 {
		t.Errorf("pixel size not reported: %+v", got[0])
	}

	list := decode[[]project.Image](t, mustGet(t, base+"/images"), 200)
	if len(list) != 2 {
		t.Fatalf("listed %+v", list)
	}

	// The bytes come back unchanged, with an image content type.
	res := mustGet(t, base+"/images/my-scan.png")
	defer res.Body.Close()
	back, _ := io.ReadAll(res.Body)
	if !bytes.Equal(back, data) {
		t.Errorf("downloaded %d bytes, uploaded %d", len(back), len(data))
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type %q", ct)
	}

	// Measuring: a bar 300 px long and 40 px wide, anchored to a known 30 mm length, is
	// one step of 30 x 4 mm.
	body, _ := json.Marshal(map[string]any{"image": "my-scan.png", "scale_ref_mm": 30.0})
	m := decode[measureResult](t, mustPost(t, base+"/measure", body), 200)
	if len(m.Steps) != 1 {
		t.Fatalf("found %d steps, want 1: %+v", len(m.Steps), m.Steps)
	}
	if d := m.Steps[0].WidthMM - 4; d > 0.2 || d < -0.2 {
		t.Errorf("width = %.2f mm, want 4", m.Steps[0].WidthMM)
	}
	if m.Overlay == "" {
		t.Error("no overlay returned")
	} else if b, err := base64.StdEncoding.DecodeString(m.Overlay); err != nil || len(b) < 100 {
		t.Errorf("overlay is not a usable PNG: %v, %d bytes", err, len(b))
	}
	if m.OpenSCAD == "" {
		t.Error("no OpenSCAD block returned")
	}

	// Measuring with no scale at all is an error, not a guess.
	bad, _ := json.Marshal(map[string]any{"image": "my-scan.png"})
	res = mustPost(t, base+"/measure", bad)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("measuring without a scale: status %d, want 400", res.StatusCode)
	}

	// Deleting.
	req, _ := http.NewRequest("DELETE", base+"/images/my-scan-2.png", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("delete: status %d", res.StatusCode)
	}
	if list := decode[[]project.Image](t, mustGet(t, base+"/images"), 200); len(list) != 1 {
		t.Errorf("after deleting, %d images remain", len(list))
	}
	res = mustGet(t, base+"/images/my-scan-2.png")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("a deleted image gave status %d, want 404", res.StatusCode)
	}
}

func TestUploadRejectsNonImages(t *testing.T) {
	srv := newTestServer(t)
	res := upload(t, srv.URL+"/api/projects/tag/images", []string{"notes.png"}, []byte("not an image"))
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("status %d, want 400", res.StatusCode)
	}
}

func TestMeasureUnknownProject(t *testing.T) {
	srv := newTestServer(t)
	res := mustPost(t, srv.URL+"/api/projects/nope/measure", []byte(`{}`))
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status %d, want 404", res.StatusCode)
	}
}

// TestMeasureOptionsAreParsed checks the flat JSON body maps onto the options.
func TestMeasureOptionsAreParsed(t *testing.T) {
	var body struct {
		Image string `json:"image"`
		measure.Options
	}
	raw := `{"image":"a.png","dpi":1200,"grid_mm":1,"scale_ref_mm":26.95,"invert":true,"channel":"sat","tol_mm":0.2,"min_step_mm":1,"flip":true,"overlay":false}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if body.Image != "a.png" || body.DPI != 1200 || body.GridMM != 1 || body.ScaleRefMM != 26.95 ||
		!body.Invert || body.Channel != "sat" || body.TolMM != 0.2 || body.MinStepMM != 1 || !body.Flip || body.Overlay {
		t.Fatalf("parsed %+v", body)
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustPost(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	res, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}
