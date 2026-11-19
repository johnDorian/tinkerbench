package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/app"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

func testServer(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	store, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("tag", "", "cube(1);\n", "test"); err != nil {
		t.Fatal(err)
	}
	a := app.New(store, render.New("openscad-not-used", 0, 1), events.NewHub())
	srv := httptest.NewServer(New(a, "http://localhost:8020", "220x220", "test"))
	t.Cleanup(srv.Close)
	return srv, a
}

// call makes one JSON-RPC request and returns the decoded result.
func call(t *testing.T, srv *httptest.Server, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	res, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Result map[string]any  `json:"result"`
		Error  *rpcErr         `json:"error"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("%s: rpc error %d %s", method, out.Error.Code, out.Error.Message)
	}
	return out.Result
}

// callTool runs a tool and returns its content blocks, failing on a tool error.
func callTool(t *testing.T, srv *httptest.Server, name string, args map[string]any) []any {
	t.Helper()
	res := call(t, srv, "tools/call", map[string]any{"name": name, "arguments": args})
	content, _ := res["content"].([]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("%s failed: %v", name, textOf(content))
	}
	return content
}

func textOf(content []any) string {
	var b strings.Builder
	for _, c := range content {
		m, _ := c.(map[string]any)
		if m["type"] == "text" {
			b.WriteString(m["text"].(string))
		}
	}
	return b.String()
}

func imagesIn(content []any) int {
	n := 0
	for _, c := range content {
		if m, _ := c.(map[string]any); m["type"] == "image" {
			n++
		}
	}
	return n
}

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
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestToolsListIncludesMeasuring(t *testing.T) {
	srv, _ := testServer(t)
	res := call(t, srv, "tools/list", nil)
	tools, _ := res["tools"].([]any)
	have := map[string]bool{}
	for _, raw := range tools {
		m := raw.(map[string]any)
		name := m["name"].(string)
		have[name] = true
		if m["description"] == "" || m["inputSchema"] == nil {
			t.Errorf("tool %s is missing a description or schema", name)
		}
		// Every schema has to be valid JSON Schema the client can send against.
		s := m["inputSchema"].(map[string]any)
		if s["type"] != "object" || s["properties"] == nil {
			t.Errorf("tool %s has a malformed schema: %v", name, s)
		}
	}
	for _, want := range []string{"list_images", "read_image", "upload_image", "delete_image", "measure_part"} {
		if !have[want] {
			t.Errorf("tool %s is not listed", want)
		}
	}
}

func TestInstructionsMentionMeasuring(t *testing.T) {
	srv, _ := testServer(t)
	res := call(t, srv, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	ins, _ := res["instructions"].(string)
	for _, want := range []string{"measure_part", "scan", "caliper"} {
		if !strings.Contains(ins, want) {
			t.Errorf("the server instructions never mention %q", want)
		}
	}
}

func TestImageToolsRoundTrip(t *testing.T) {
	srv, a := testServer(t)
	data := barPNG(t)

	// upload
	callTool(t, srv, "upload_image", map[string]any{
		"project": "tag", "name": "scan.png", "data": base64.StdEncoding.EncodeToString(data), "by": "test"})
	if list, _ := a.Images("tag"); len(list) != 1 || list[0].Width != 400 {
		t.Fatalf("after upload: %+v", list)
	}

	// list
	if out := textOf(callTool(t, srv, "list_images", map[string]any{"project": "tag"})); !strings.Contains(out, "scan.png") {
		t.Errorf("list_images returned %s", out)
	}

	// read: comes back as an image block, shrunk to the requested size
	content := callTool(t, srv, "read_image", map[string]any{"project": "tag", "image": "scan.png", "max_px": 100})
	if imagesIn(content) != 1 {
		t.Fatalf("read_image returned %d images", imagesIn(content))
	}
	if !strings.Contains(textOf(content), "100x50") {
		t.Errorf("read_image did not say what size it sent: %s", textOf(content))
	}

	// measure: numbers and the check image together
	content = callTool(t, srv, "measure_part", map[string]any{
		"project": "tag", "image": "scan.png", "scale_ref_mm": 30.0})
	if imagesIn(content) != 1 {
		t.Errorf("measure_part returned %d overlay images, want 1", imagesIn(content))
	}
	out := textOf(content)
	for _, want := range []string{"overall_length_mm", "steps", "openscad", "caliper"} {
		if !strings.Contains(out, want) {
			t.Errorf("measure_part output is missing %q:\n%s", want, out)
		}
	}

	// measure with no image named falls back to the most recent upload
	if c := callTool(t, srv, "measure_part", map[string]any{"project": "tag", "scale_ref_mm": 30.0}); imagesIn(c) != 1 {
		t.Error("measure_part without an image name should still work")
	}

	// delete
	callTool(t, srv, "delete_image", map[string]any{"project": "tag", "image": "scan.png"})
	if list, _ := a.Images("tag"); len(list) != 0 {
		t.Errorf("after delete: %+v", list)
	}
}

func TestToolErrorsAreReportedNotThrown(t *testing.T) {
	srv, _ := testServer(t)
	for _, c := range []struct {
		name, want string
		args       map[string]any
	}{
		{"measure_part", "no images", map[string]any{"project": "tag"}},
		{"upload_image", "base64", map[string]any{"project": "tag", "name": "a.png", "data": "!!!not base64!!!"}},
		{"upload_image", "PNG or JPEG", map[string]any{"project": "tag", "name": "a.png", "data": base64.StdEncoding.EncodeToString([]byte("nope"))}},
		{"read_image", "not found", map[string]any{"project": "tag", "image": "missing.png"}},
	} {
		res := call(t, srv, "tools/call", map[string]any{"name": c.name, "arguments": c.args})
		if isErr, _ := res["isError"].(bool); !isErr {
			t.Errorf("%s with %v should have failed", c.name, c.args)
			continue
		}
		content, _ := res["content"].([]any)
		if got := textOf(content); !strings.Contains(got, c.want) {
			t.Errorf("%s error = %q, want it to mention %q", c.name, got, c.want)
		}
	}
}
