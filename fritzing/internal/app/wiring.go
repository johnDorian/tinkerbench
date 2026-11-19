package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/fritzing/internal/events"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
	"github.com/johnDorian/tinkerbench/fritzing/internal/wiring"
)

// Wiring builds the wiring description of a project: generated tables plus the stored notes.
func (a *App) Wiring(name, view string) (wiring.Wiring, error) {
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return wiring.Wiring{}, err
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return wiring.Wiring{}, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return wiring.Wiring{}, err
	}
	in := wiring.Input{Project: name, View: view, Summary: s.Describe(res), Res: res, SketchVersion: a.Store.SketchVersion(name)}
	if !parts.ValidView(view) {
		view = parts.Breadboard
	}
	lay := s.CheckLayout(res, view)
	in.Layout = &lay
	if stored, err := a.Store.ReadFile(name, project.NotesFile); err == nil {
		in.Notes, in.NotesVersion = project.ParseNotes(stored)
	} else if !errors.Is(err, project.ErrNotFound) {
		return wiring.Wiring{}, err
	}
	return wiring.Build(in), nil
}

// SetNotes saves the notes of a project, stamped with the current sketch version. Empty text
// removes them. It never renders.
func (a *App) SetNotes(name, text, by string) (project.Version, error) {
	if _, err := a.Store.Get(name); err != nil {
		return project.Version{}, err
	}
	stamped := project.StampNotes(text, a.Store.SketchVersion(name))
	var v project.Version
	var err error
	if strings.TrimSpace(text) == "" {
		if _, rerr := a.Store.ReadFile(name, project.NotesFile); rerr != nil {
			return project.Version{}, nil // nothing to remove
		}
		v, err = a.Store.DeleteFile(name, project.NotesFile, by)
	} else {
		v, err = a.Store.WriteFile(name, project.NotesFile, stamped, by, "notes")
	}
	if err == nil {
		a.Hub.Publish(events.Event{Kind: events.FileChanged, Project: name, File: project.NotesFile, Source: by})
	}
	return v, err
}

// CheckLayout runs the geometry checks on one view (default breadboard).
func (a *App) CheckLayout(name, view string) (sketch.LayoutReport, error) {
	if view == "" {
		view = parts.Breadboard
	}
	if !parts.ValidView(view) {
		return sketch.LayoutReport{}, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return sketch.LayoutReport{}, err
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return sketch.LayoutReport{}, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return sketch.LayoutReport{}, err
	}
	return s.CheckLayout(res, view), nil
}

// RenderStale says whether the rendered pictures are older than the latest sketch version, and
// when each happened. A project that was never rendered is stale too.
func (a *App) RenderStale(name string) (stale bool, rendered, changed time.Time) {
	vs, err := a.Store.History(name, project.MainFile)
	if err != nil || len(vs) == 0 {
		return false, time.Time{}, time.Time{}
	}
	changed = vs[len(vs)-1].Time
	if r := a.LastRender(name); r != nil {
		rendered = r.Time
	}
	return rendered.Before(changed), rendered, changed
}

// Preview runs an edit on the current sketch without saving it and returns what fn returned.
func (a *App) Preview(name string, fn func(*sketch.Sketch, sketch.Resolver) (any, error)) (any, error) {
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return nil, err
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return nil, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return nil, err
	}
	return fn(s, res)
}

// DiffResult is what DiffVersions returns: the two sketch versions compared and what changed.
type DiffResult struct {
	From int `json:"from"`
	To   int `json:"to"`
	sketch.Diff
}

// DiffVersions compares two saved versions of sketch.fz in words. to defaults to the latest
// version of the sketch, from to the sketch version saved just before to.
func (a *App) DiffVersions(name string, from, to int) (DiffResult, error) {
	hist, err := a.Store.History(name, project.MainFile)
	if err != nil {
		return DiffResult{}, err
	}
	if len(hist) == 0 {
		return DiffResult{}, fmt.Errorf("%s has no saved sketch versions", name)
	}
	if to == 0 {
		to = hist[len(hist)-1].N
	}
	if from == 0 {
		for _, v := range hist {
			if v.N < to {
				from = v.N
			}
		}
		if from == 0 {
			return DiffResult{}, fmt.Errorf("no sketch version before %d to compare with", to)
		}
	}
	load := func(n int) (*sketch.Sketch, error) {
		v, content, err := a.Store.GetVersion(name, n)
		if err != nil {
			return nil, err
		}
		if v.File != project.MainFile {
			return nil, fmt.Errorf("version %d saved %s, not %s: pick a version from list_versions with file %s", n, v.File, project.MainFile, project.MainFile)
		}
		return sketch.Parse(content)
	}
	sa, err := load(from)
	if err != nil {
		return DiffResult{}, err
	}
	sb, err := load(to)
	if err != nil {
		return DiffResult{}, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{From: from, To: to, Diff: sketch.Compare(res, sa, sb)}, nil
}

// CircuitCheck is the result of the electrical sanity checks, per view that has nets.
type CircuitCheck struct {
	Views  map[string][]wiring.Warning `json:"views"`
	Errors int                         `json:"errors"`
	Warns  int                         `json:"warnings"`
}

// CheckCircuit runs wiring.Circuit on one view, or on every view that has wires when view is empty.
func (a *App) CheckCircuit(name, view string) (CircuitCheck, error) {
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return CircuitCheck{}, err
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return CircuitCheck{}, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return CircuitCheck{}, err
	}
	sum := s.Describe(res)
	in := wiring.Input{Project: name, Summary: sum, Res: res}
	out := CircuitCheck{Views: map[string][]wiring.Warning{}}
	views := parts.Views
	if view != "" {
		if !parts.ValidView(view) {
			return CircuitCheck{}, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
		}
		views = []string{view}
	}
	for _, v := range views {
		has := false
		for _, n := range sum.Nets {
			has = has || n.View == v
		}
		if !has && view == "" {
			continue
		}
		ws := wiring.Circuit(in, v)
		out.Views[v] = ws
		for _, w := range ws {
			switch w.Level {
			case "error":
				out.Errors++
			case "warn":
				out.Warns++
			}
		}
	}
	return out, nil
}
