package mcp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// toolError runs a tool that is expected to fail and returns the error text.
func toolError(t *testing.T, srv *httptest.Server, name string, args map[string]any) string {
	t.Helper()
	res := call(t, srv, "tools/call", map[string]any{"name": name, "arguments": args})
	isErr, _ := res["isError"].(bool)
	if !isErr {
		content, _ := json.Marshal(res["content"])
		t.Fatalf("%s: want a tool error, got %s", name, content)
	}
	content, _ := res["content"].([]any)
	return textOf(content)
}

// TestUploadByURL fetches an image from an address with no file extension: the name comes from
// the path, and the extension from the bytes.
func TestUploadByURL(t *testing.T) {
	srv, a := testServer(t)
	photos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shots/part-photo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(barPNG(t))
	}))
	t.Cleanup(photos.Close)

	callTool(t, srv, "upload_image", map[string]any{
		"project": "tag", "url": photos.URL + "/shots/part-photo", "by": "test"})
	list, _ := a.Images("tag")
	if len(list) != 1 || list[0].Name != "part-photo.png" {
		t.Fatalf("after url upload: %+v, want part-photo.png", list)
	}
	if list[0].Width != 400 || list[0].Height != 200 {
		t.Errorf("size = %dx%d, want 400x200", list[0].Width, list[0].Height)
	}
}

// TestUploadArguments: exactly one of data and url, a name with data, http or https only, and
// a failed fetch is reported.
func TestUploadArguments(t *testing.T) {
	srv, _ := testServer(t)
	data := base64.StdEncoding.EncodeToString(barPNG(t))
	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)

	for name, args := range map[string]map[string]any{
		"both":       {"project": "tag", "name": "a.png", "data": data, "url": missing.URL + "/a.png"},
		"neither":    {"project": "tag", "name": "a.png"},
		"no name":    {"project": "tag", "data": data},
		"file url":   {"project": "tag", "url": "file:///etc/hosts"},
		"not found":  {"project": "tag", "url": missing.URL + "/nothing.png"},
		"bad base64": {"project": "tag", "name": "a.png", "data": "%%%"},
	} {
		if msg := toolError(t, srv, "upload_image", args); msg == "" {
			t.Errorf("%s: empty error", name)
		}
	}
	if msg := toolError(t, srv, "upload_image", map[string]any{"project": "tag", "url": "file:///etc/hosts"}); !strings.Contains(msg, "http") {
		t.Errorf("file url error should say which schemes work: %q", msg)
	}
}

func TestNameFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.test/a/b/IMG_0882.jpg?x=1": "IMG_0882.jpg",
		"https://example.test/":                     "image",
		"https://example.test":                      "image",
	} {
		if got := nameFromURL(in); got != want {
			t.Errorf("nameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
