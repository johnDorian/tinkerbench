// Package testapp builds a complete app for tests. It is separate from testkit so that the
// packages under test can use testkit without an import cycle.
package testapp

import (
	"testing"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/app"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/events"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/openscad"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/printer"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/slicer"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

// NewApp returns an app over a temp data dir and the synthetic profiles. The slicer is not
// started, and no printer is configured, so nothing talks to OrcaSlicer or a printer.
func NewApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	ms, err := models.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := plates.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	profs := profiles.Open(testkit.WriteProfiles(t))
	a := &app.App{
		Cfg: app.Config{
			PublicURL: "http://orcaslicer.test", OpenSCADPublicURL: "http://openscad.test", OrcaVersion: "test",
			Machine: testkit.Machine, Process: testkit.Process, Filament: testkit.Filament,
		},
		Profiles: profs,
		Models:   ms,
		Plates:   ps,
		OpenSCAD: openscad.New("http://127.0.0.1:1"),
		Printer:  printer.New(""),
		Hub:      events.NewHub(),
	}
	a.Slicer = slicer.New(slicer.Options{Binary: "/nonexistent", DataDir: dir, Timeout: time.Minute,
		Profiles: profs, Models: ms, Plates: ps, Version: "test"})
	a.Wire()
	return a
}
