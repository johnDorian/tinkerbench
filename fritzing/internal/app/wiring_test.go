package app

import (
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/events"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

func newApp(t *testing.T) *App {
	t.Helper()
	st, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &App{Store: st, Hub: events.NewHub()}
}

func TestNotes(t *testing.T) {
	a := newApp(t)
	if _, err := a.Store.Create("lamp", "", "", "test"); err != nil {
		t.Fatal(err)
	}
	// empty sketch: a page, no error
	w, err := a.Wiring("lamp", "")
	if err != nil || w.Stale || w.Notes != "" {
		t.Fatalf("empty: %+v %v", w, err)
	}
	// notes do not need a sketch with parts and never render (no runner here would panic)
	if _, err := a.SetNotes("lamp", "## Purpose\nSwitches a lamp.", "test"); err != nil {
		t.Fatal(err)
	}
	w, _ = a.Wiring("lamp", "")
	if w.Notes != "## Purpose\nSwitches a lamp." || w.Stale || w.NotesVersion != w.SketchVersion {
		t.Fatalf("saved: %+v", w)
	}
	info, _ := a.Store.Get("lamp")
	if !info.HasNotes || info.NotesStale {
		t.Errorf("info: %+v", info)
	}

	// the sketch moves on: notes are stale
	src, _ := a.Store.ReadFile("lamp", project.MainFile)
	if _, err := a.Store.WriteFile("lamp", project.MainFile, src+"\n", "test", "touch"); err != nil {
		t.Fatal(err)
	}
	w, _ = a.Wiring("lamp", "")
	if !w.Stale || !strings.Contains(w.Markdown(), "check them") {
		t.Errorf("not stale: %+v", w)
	}
	if info, _ := a.Store.Get("lamp"); !info.NotesStale {
		t.Errorf("info not stale: %+v", info)
	}

	// notes.md stays out of what Fritzing gets and of the download
	files, err := a.bundleFiles("lamp")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == project.NotesFile {
			t.Error("notes.md in the bundle")
		}
	}
	if len(files) == 0 {
		t.Error("bundle empty")
	}

	// empty text removes them again
	if _, err := a.SetNotes("lamp", "  ", "test"); err != nil {
		t.Fatal(err)
	}
	if info, _ := a.Store.Get("lamp"); info.HasNotes {
		t.Error("notes not removed")
	}
	if _, err := a.SetNotes("nope", "x", "test"); err == nil {
		t.Error("unknown project accepted")
	}
}
