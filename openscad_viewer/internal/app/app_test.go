package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

func TestFormatParams(t *testing.T) {
	got, err := FormatParams(map[string]any{"w": 40.5, "on": true, "label": `say "hi"`, "v": []any{1.0, 2.0}, "u": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"w": "40.5", "on": "true", "label": `"say \"hi\""`, "v": "[1, 2]", "u": "undef"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
}

// TestRender runs the real openscad binary; skipped when it isn't installed.
func TestRender(t *testing.T) {
	bin, err := exec.LookPath("openscad")
	if err != nil {
		t.Skip("openscad not installed")
	}
	store, _ := project.Open(t.TempDir())
	a := New(store, render.New(bin, 2*time.Minute, 1), events.NewHub())
	ctx := context.Background()
	sub := a.Hub.Subscribe()

	_, res, err := a.Create(ctx, "plate", "", "w = 10;\ncube([w, 20, 3]);\n", "test", RenderSync)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Stats == nil || res.Stats.Size != [3]float64{10, 20, 3} {
		t.Fatalf("render = %+v", res)
	}

	r, err := a.Render(ctx, "plate", map[string]string{"w": "50"}, "test")
	if err != nil || !r.OK || r.Stats.Size[0] != 50 {
		t.Fatalf("render with params = %+v, %v", r, err)
	}
	if _, err := a.Render(ctx, "plate", map[string]string{"bad name": "1"}, "test"); err == nil {
		t.Fatal("want error for invalid param name")
	}

	// A syntax error keeps the previous model and reports the line.
	s, err := a.WriteFile(ctx, "plate", project.MainFile, "cube([10, 10;\n", "test", "", RenderSync)
	if err != nil {
		t.Fatal(err)
	}
	if s.Render.OK || len(s.Render.Errors) == 0 || !strings.Contains(s.Render.Errors[0], "line") {
		t.Fatalf("broken render = %+v", s.Render)
	}
	if s.Render.Stats == nil || s.Render.Stats.Size[0] != 50 {
		t.Fatalf("previous model should be kept: %+v", s.Render.Stats)
	}

	kinds := []string{}
	for len(sub) > 0 {
		kinds = append(kinds, (<-sub).Kind)
	}
	if !strings.Contains(strings.Join(kinds, ","), "project_created,file_changed,render_started,render_done") {
		t.Fatalf("events = %v", kinds)
	}
}
