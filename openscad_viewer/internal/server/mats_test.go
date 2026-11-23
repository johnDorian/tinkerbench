package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/mat"
)

func TestListMats(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + "/api/mats")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var list []struct {
		Name    string  `json:"name"`
		Title   string  `json:"title"`
		Paper   string  `json:"paper"`
		InnerMM float64 `json:"inner_mm"`
		URL     string  `json:"url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name, paper string
		inner       float64
	}{{"a4-100", "A4", 100}, {"a4-150", "A4", 150}, {"letter-100", "Letter", 100}, {"letter-150", "Letter", 150}}
	if len(list) != len(want) {
		t.Fatalf("%d mats, want %d: %+v", len(list), len(want), list)
	}
	for i, w := range want {
		g := list[i]
		if g.Name != w.name || g.Paper != w.paper || g.InnerMM != w.inner {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
		if g.URL != "/api/mats/"+w.name+".pdf" {
			t.Errorf("entry %d url = %q", i, g.URL)
		}
		if g.Title == "" {
			t.Errorf("entry %d has no title", i)
		}
	}
}

func TestGetMat(t *testing.T) {
	srv := newTestServer(t)
	res, err := http.Get(srv.URL + "/api/mats/letter-150.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="measuring-mat-letter-150.pdf"` {
		t.Errorf("Content-Disposition %q", cd)
	}
	m, _ := mat.ByName("letter-150")
	if !bytes.Equal(body, m.PDF()) {
		t.Error("body is not the letter-150 mat")
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Errorf("body starts %q", body[:min(len(body), 8)])
	}

	// The bytes never change, so a repeat with the ETag gets no body.
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/mats/letter-150.pdf", nil)
	req.Header.Set("If-None-Match", etag)
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d, want 304", again.StatusCode)
	}
}

func TestGetMatNotFound(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"a4-200.pdf", "a4-100", "a4-100.txt", "../a4-100.pdf", "A4-100.pdf", ".pdf"} {
		res, err := http.Get(srv.URL + "/api/mats/" + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("%s: status %d, want 404", path, res.StatusCode)
		}
	}
}
