package printer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStatusParsesAndCaches(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasPrefix(r.URL.Path, "/printer/objects/query") {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"result":{"status":{
			"print_stats":{"state":"printing","filename":"part.gcode","print_duration":600,"message":""},
			"display_status":{"progress":0.25,"message":""},
			"virtual_sdcard":{"progress":0.25},
			"extruder":{"temperature":230.1,"target":230},
			"heater_bed":{"temperature":69.8,"target":70}}}}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	st := c.Status(context.Background())
	if !st.Online || st.State != "printing" || st.Filename != "part.gcode" || st.Progress != 0.25 {
		t.Fatalf("status = %+v", st)
	}
	if st.ETA != 1800 || st.Extruder.Target != 230 || st.Bed.Temp != 69.8 {
		t.Fatalf("eta/temps = %+v", st)
	}
	c.Status(context.Background())
	if hits != 1 {
		t.Fatalf("status should be cached: %d requests", hits)
	}
}

func TestOfflineIsNotAnError(t *testing.T) {
	c := New("http://127.0.0.1:1")
	if st := c.Status(context.Background()); st.Online || st.State != "offline" {
		t.Fatalf("status = %+v", st)
	}
	if New("").Status(context.Background()).State != "offline" {
		t.Fatal("disabled printer should report offline")
	}
}

func TestUploadAndActions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/server/files/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			if r.FormValue("root") != "gcodes" || r.FormValue("print") != "true" {
				t.Errorf("form = %v", r.MultipartForm.Value)
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("file: %v", err)
				return
			}
			b, _ := io.ReadAll(f)
			if string(b) != "G28\n" {
				t.Errorf("uploaded %q", b)
			}
			w.Write([]byte(`{"result":"ok"}`))
		case "/printer/print/pause", "/printer/print/resume", "/printer/print/cancel":
			w.Write([]byte(`{"result":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	if err := c.Upload(context.Background(), "x.gcode", strings.NewReader("G28\n")); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"pause", "resume", "cancel"} {
		if err := c.Action(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Action(context.Background(), "explode"); err == nil {
		t.Fatal("unknown action should fail")
	}
}
