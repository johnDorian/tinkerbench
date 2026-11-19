package openscad

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAndSTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects":
			w.Write([]byte(`[{"name":"bracket","description":"d","updated":"2026-01-01T00:00:00Z","version":3,"files":["main.scad"]},
				{"name":"empty","description":"","updated":null,"version":1,"files":[]}]`))
		case "/api/projects/bracket/model.stl":
			if r.Method == http.MethodHead {
				w.WriteHeader(200)
				return
			}
			w.Write([]byte("solid x"))
		case "/api/projects/empty/model.stl":
			http.Error(w, "not rendered yet", 404)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL + "/")
	list, err := c.List(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !list[0].Rendered || list[1].Rendered {
		t.Fatalf("rendered flags = %v %v", list[0].Rendered, list[1].Rendered)
	}
	if _, err := c.STL(context.Background(), "empty"); !errors.Is(err, ErrNotRendered) {
		t.Fatalf("empty STL err = %v, want ErrNotRendered", err)
	}
	data, err := c.STL(context.Background(), "bracket")
	if err != nil || string(data) != "solid x" {
		t.Fatalf("STL = %q, %v", data, err)
	}
	if _, err := c.Project(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project err = %v", err)
	}
}
