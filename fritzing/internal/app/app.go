// Package app ties the project store, Fritzing, the parts library and the event hub together. The
// web UI and MCP both go through it, so every change is versioned, rendered and announced the same
// way.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/johnDorian/tinkerbench/fritzing/internal/events"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/runner"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
	"github.com/johnDorian/tinkerbench/fritzing/internal/userparts"
)

// RenderMode says what happens after a change.
type RenderMode int

const (
	NoRender    RenderMode = iota
	RenderSync             // render before returning (MCP: the agent gets the result right away)
	RenderAsync            // render in the background (web UI: the result arrives as an event)
)

type App struct {
	Store   *project.Store
	Runner  *runner.Runner
	Catalog *parts.Catalog
	Users   *userparts.Library // imported parts, may be nil
	Hub     *events.Hub

	mu     sync.Mutex
	states map[string]*renderState // background renders per project
}

type renderState struct{ running, again bool }

// SaveResult is returned by every change to a project.
type SaveResult struct {
	Version project.Version `json:"version"`
	Render  *runner.Result  `json:"render,omitempty"`
	// Detail is what the operation did (titles of added or removed parts, ...).
	Detail any `json:"detail,omitempty"`
}

func (a *App) publish(kind, name, file, by string) {
	a.Hub.Publish(events.Event{Kind: kind, Project: name, File: file, Source: by})
}

func (a *App) after(ctx context.Context, name, file, by string, v project.Version, mode RenderMode) (SaveResult, error) {
	a.publish(events.FileChanged, name, file, by)
	res := SaveResult{Version: v}
	if file == project.NotesFile {
		mode = NoRender // notes are not part of the circuit
	}
	switch mode {
	case RenderSync:
		r, err := a.Render(ctx, name, by)
		if err != nil {
			return res, err
		}
		res.Render = &r
	case RenderAsync:
		a.renderLater(name, by)
	}
	return res, nil
}

// renderLater renders in the background; changes that arrive meanwhile cause one more run after it,
// not one run each.
func (a *App) renderLater(name, by string) {
	a.mu.Lock()
	if a.states == nil {
		a.states = map[string]*renderState{}
	}
	st := a.states[name]
	if st == nil {
		st = &renderState{}
		a.states[name] = st
	}
	if st.running {
		st.again = true
		a.mu.Unlock()
		return
	}
	st.running = true
	a.mu.Unlock()
	go func() {
		for {
			if _, err := a.Render(context.Background(), name, by); err != nil {
				log.Printf("render %s: %v", name, err)
			}
			a.mu.Lock()
			if !st.again {
				st.running = false
				a.mu.Unlock()
				return
			}
			st.again = false
			a.mu.Unlock()
		}
	}()
}

// Create makes a project and renders it.
func (a *App) Create(ctx context.Context, name, description, source, by string, mode RenderMode) (project.Info, *runner.Result, error) {
	if source != "" {
		if _, err := sketch.Parse(source); err != nil {
			return project.Info{}, nil, err
		}
	}
	info, err := a.Store.Create(name, description, source, by)
	if err != nil {
		return info, nil, err
	}
	a.publish(events.ProjectCreated, name, "", by)
	res, err := a.after(ctx, name, project.MainFile, by, project.Version{}, mode)
	return info, res.Render, err
}

// checkContent makes sure a file can be parsed before it is saved.
func checkContent(file, content string) error {
	switch {
	case strings.HasSuffix(file, ".fz"):
		_, err := sketch.Parse(content)
		return err
	case strings.HasSuffix(file, ".fzp"):
		_, err := parts.ParseFZP([]byte(content))
		return err
	}
	return nil
}

func (a *App) WriteFile(ctx context.Context, name, file, content, by, note string, mode RenderMode) (SaveResult, error) {
	if err := checkContent(file, content); err != nil {
		return SaveResult{}, fmt.Errorf("%s not saved: %w", file, err)
	}
	v, err := a.Store.WriteFile(name, file, content, by, note)
	if err != nil {
		return SaveResult{}, err
	}
	return a.after(ctx, name, file, by, v, mode)
}

func (a *App) EditFile(ctx context.Context, name, file, old, new string, all bool, by, note string, mode RenderMode) (SaveResult, error) {
	if old == "" {
		return SaveResult{}, errors.New("old_string is empty")
	}
	v, err := a.Store.Update(name, file, by, note, func(content string) (string, error) {
		switch n := strings.Count(content, old); {
		case n == 0:
			return "", fmt.Errorf("old_string not found in %s", file)
		case n > 1 && !all:
			return "", fmt.Errorf("old_string occurs %d times in %s; add context to make it unique or set replace_all", n, file)
		}
		if all {
			content = strings.ReplaceAll(content, old, new)
		} else {
			content = strings.Replace(content, old, new, 1)
		}
		if err := checkContent(file, content); err != nil {
			return "", fmt.Errorf("edit not saved, %s would no longer parse: %w", file, err)
		}
		return content, nil
	})
	if err != nil {
		return SaveResult{}, err
	}
	return a.after(ctx, name, file, by, v, mode)
}

func (a *App) DeleteFile(ctx context.Context, name, file, by string, mode RenderMode) (SaveResult, error) {
	v, err := a.Store.DeleteFile(name, file, by)
	if err != nil {
		return SaveResult{}, err
	}
	return a.after(ctx, name, file, by, v, mode)
}

// Restore writes the content of an old version back as a new version.
func (a *App) Restore(ctx context.Context, name string, n int, by string, mode RenderMode) (SaveResult, error) {
	v, content, err := a.Store.GetVersion(name, n)
	if err != nil {
		return SaveResult{}, err
	}
	return a.WriteFile(ctx, name, v.File, content, by, fmt.Sprintf("restored version %d", n), mode)
}

func (a *App) Delete(name, by string) error {
	if err := a.Store.Delete(name); err != nil {
		return err
	}
	a.publish(events.ProjectDeleted, name, "", by)
	return nil
}

// SetDescription changes a project's description.
func (a *App) SetDescription(name, description, by string) (project.Info, error) {
	info, err := a.Store.SetDescription(name, description)
	if err == nil {
		a.publish(events.ProjectUpdated, name, "", by)
	}
	return info, err
}

// Render runs Fritzing on the project.
func (a *App) Render(ctx context.Context, name, by string) (runner.Result, error) {
	files, err := a.bundleFiles(name)
	if err != nil {
		return runner.Result{}, err
	}
	a.publish(events.RenderStarted, name, "", by)
	res, err := a.Runner.Render(ctx, a.Store.Dir(name), files, a.Catalog)
	if err == nil && res.OK {
		res.Warnings = append(res.Warnings, a.artWarnings(name)...)
	}
	ok := err == nil && res.OK
	a.Hub.Publish(events.Event{Kind: events.RenderDone, Project: name, Source: by, OK: &ok})
	return res, err
}

// artWarnings compares every part's connectors in the rendered breadboard and schematic pictures
// with where the part defines them (see sketch.AuditArt), so artwork that Fritzing draws wrongly is
// reported by the render that first shows it. Failures to audit are not warnings.
func (a *App) artWarnings(name string) []string {
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return nil
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return nil
	}
	res, err := a.resolver(name)
	if err != nil {
		return nil
	}
	var out []string
	for _, v := range []string{parts.Breadboard, parts.Schematic} {
		svg, err := a.SVG(name, v)
		if err != nil {
			continue
		}
		for _, is := range s.AuditArt(res, v, svg) {
			out = append(out, is.Kind+": "+is.Text)
		}
	}
	return out
}

// LastRender is the stored result of the last render, or nil.
func (a *App) LastRender(name string) *runner.Result { return runner.Last(a.Store.Dir(name)) }

// SVG is the rendered SVG of a view.
func (a *App) SVG(name, view string) ([]byte, error) {
	if err := project.ValidName(name); err != nil {
		return nil, err
	}
	return runner.ViewSVG(a.Store.Dir(name), view)
}

// Snapshot is a PNG of a rendered view.
func (a *App) Snapshot(ctx context.Context, name, view string, width int) ([]byte, error) {
	svg, err := a.SVG(name, view)
	if err != nil {
		return nil, err
	}
	return a.Runner.PNG(ctx, svg, width)
}

// Crop picks an area of a view for a zoomed snapshot: around a part or wire title (plus
// MarginMM), or an explicit rectangle in mm (X, Y, W, H).
type Crop struct {
	Around   string
	MarginMM float64
	X, Y     float64
	W, H     float64
}

// SnapshotCrop is a PNG of one area of a rendered view.
func (a *App) SnapshotCrop(ctx context.Context, name, view string, width int, c Crop) ([]byte, error) {
	svg, err := a.SVG(name, view)
	if err != nil {
		return nil, err
	}
	var x0, y0, x1, y1 float64
	switch {
	case c.Around != "":
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
		if c.MarginMM <= 0 {
			c.MarginMM = 8
		}
		if x0, y0, x1, y1, err = s.FocusBox(res, view, c.Around, c.MarginMM); err != nil {
			return nil, err
		}
	case c.W > 0 && c.H > 0:
		x0, y0 = sketch.MMToPx(c.X), sketch.MMToPx(c.Y)
		x1, y1 = x0+sketch.MMToPx(c.W), y0+sketch.MMToPx(c.H)
	default:
		return nil, fmt.Errorf("a crop needs around (a part or wire title) or x, y, w, h in mm")
	}
	if svg, err = sketch.CropView(svg, x0, y0, x1, y1); err != nil {
		return nil, err
	}
	return a.Runner.PNG(ctx, svg, width)
}

// FZZ packs a project as a Fritzing bundle, ready to open in the desktop app.
func (a *App) FZZ(name string) ([]byte, error) {
	files, err := a.bundleFiles(name)
	if err != nil {
		return nil, err
	}
	return runner.FZZ(files)
}

// ---------- sketch operations ----------

// resolver finds parts in the project's own files first, then in the library.
type resolver struct {
	cat   *parts.Catalog
	users *userparts.Library
	files map[string]string
}

func (a *App) resolver(name string) (*resolver, error) {
	files, err := a.Store.Files(name)
	if err != nil {
		return nil, err
	}
	r := &resolver{cat: a.Catalog, users: a.Users, files: map[string]string{}}
	for _, f := range files {
		r.files[f.Name] = f.Content
	}
	return r, nil
}

func (r *resolver) own(moduleID string) (*parts.FZP, string, bool) {
	for name, c := range r.files {
		if strings.HasSuffix(name, ".fzp") {
			if f, err := parts.ParseFZP([]byte(c)); err == nil && f.ModuleID == moduleID {
				return f, name, true
			}
		}
	}
	return nil, "", false
}

func (r *resolver) FZP(moduleID string) (*parts.FZP, string, error) {
	if f, name, ok := r.own(moduleID); ok {
		return f, name, nil
	}
	if r.users.Has(moduleID) {
		return r.users.FZP(moduleID)
	}
	if r.cat == nil {
		return nil, "", errors.New("no parts library")
	}
	return r.cat.FZP(moduleID)
}

func (r *resolver) SVG(moduleID, view string) ([]byte, error) {
	if f, _, ok := r.own(moduleID); ok {
		// custom artwork travels in the .fzz as svg.<view>.<file name>
		img := f.Image(view)
		base := img[strings.LastIndex(img, "/")+1:]
		for _, cand := range []string{"svg." + view + "." + base, base} {
			if c, ok := r.files[cand]; ok {
				return []byte(c), nil
			}
		}
		return nil, fmt.Errorf("custom part %s: artwork %s for the %s view is not in the project", moduleID, img, view)
	}
	if r.users.Has(moduleID) {
		return r.users.SVG(moduleID, view)
	}
	if r.cat == nil {
		return nil, errors.New("no parts library")
	}
	return r.cat.SVG(moduleID, view)
}

// Describe lists what is in the sketch.
func (a *App) Describe(name string) (sketch.Summary, error) {
	src, err := a.Store.ReadFile(name, project.MainFile)
	if err != nil {
		return sketch.Summary{}, err
	}
	s, err := sketch.Parse(src)
	if err != nil {
		return sketch.Summary{}, err
	}
	res, err := a.resolver(name)
	if err != nil {
		return sketch.Summary{}, err
	}
	return s.Describe(res), nil
}

// Edit changes sketch.fz through fn, which gets the parsed sketch and a part resolver and returns
// what it did. The result is saved as a new version and rendered according to mode.
func (a *App) Edit(ctx context.Context, name, by, note string, mode RenderMode, fn func(*sketch.Sketch, sketch.Resolver) (any, error)) (SaveResult, error) {
	res, err := a.resolver(name)
	if err != nil {
		return SaveResult{}, err
	}
	var detail any
	v, err := a.Store.Update(name, project.MainFile, by, note, func(src string) (string, error) {
		s, err := sketch.Parse(src)
		if err != nil {
			return "", fmt.Errorf("sketch.fz does not parse (fix it with write_file): %w", err)
		}
		if detail, err = fn(s, res); err != nil {
			return "", err
		}
		return s.String(), nil
	})
	if err != nil {
		return SaveResult{}, err
	}
	out, err := a.after(ctx, name, project.MainFile, by, v, mode)
	out.Detail = detail
	return out, err
}

// ---------- parts library ----------

var errNoCatalog = errors.New("the parts library is not available on this server")

// SearchParts searches Fritzing's library and the imported parts (q.Source narrows to one of them).
// Imported parts come first: somebody put them there on purpose.
func (a *App) SearchParts(q parts.Query) ([]parts.Hit, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 25
	}
	var out []parts.Hit
	if q.Source != "library" {
		out = a.Users.Search(q)
	}
	if q.Source != "user" && a.Catalog != nil && (q.Text != "" || q.Family != "" || len(q.Properties) > 0) {
		hits, err := a.Catalog.Search(q)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	} else if q.Source == "library" && a.Catalog == nil {
		return nil, errNoCatalog
	}
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// GetPart looks a part up among the imported parts and Fritzing's library.
func (a *App) GetPart(moduleID string) (*parts.Part, error) {
	if a.Users.Has(moduleID) {
		return a.Users.Get(moduleID)
	}
	if a.Catalog == nil {
		return nil, errNoCatalog
	}
	return a.Catalog.Get(moduleID)
}

func (a *App) PartSVG(moduleID, view string) ([]byte, error) {
	if a.Users.Has(moduleID) {
		return a.Users.SVG(moduleID, view)
	}
	if a.Catalog == nil {
		return nil, errNoCatalog
	}
	return a.Catalog.SVG(moduleID, view)
}

func (a *App) Families(text string, limit int) ([]parts.Family, error) {
	if a.Catalog == nil {
		return nil, errNoCatalog
	}
	return a.Catalog.Families(text, limit)
}

func (a *App) PropertyValues(name, family string, limit int) ([]parts.Family, error) {
	if a.Catalog == nil {
		return nil, errNoCatalog
	}
	return a.Catalog.PropertyValues(name, family, limit)
}

// ---------- imported parts ----------

var errNoUsers = errors.New("importing parts is not available on this server")

// ImportPart adds one part from a .fzpz bundle: data, or the download of url if data is empty.
func (a *App) ImportPart(ctx context.Context, data []byte, url string, overwrite bool) (*userparts.Imported, error) {
	if a.Users == nil {
		return nil, errNoUsers
	}
	source := "upload"
	if len(data) == 0 {
		if url == "" {
			return nil, errors.New("give a url or the .fzpz content")
		}
		var err error
		if data, err = userparts.Fetch(ctx, url); err != nil {
			return nil, err
		}
		source = url
	}
	return a.Users.Import(data, source, overwrite, func(id string) bool {
		if a.Catalog == nil {
			return false
		}
		_, err := a.Catalog.Get(id)
		return err == nil
	})
}

// PartUsedBy lists the projects whose sketch uses a part.
func (a *App) PartUsedBy(moduleID string) []string {
	infos, _ := a.Store.List()
	var used []string
	for _, in := range infos {
		if src, err := a.Store.ReadFile(in.Name, project.MainFile); err == nil && strings.Contains(src, `moduleIdRef="`+moduleID+`"`) {
			used = append(used, in.Name)
		}
	}
	return used
}

// DeleteImportedPart removes an imported part. Sketches that use it would no longer render, so
// it is refused unless force is set.
func (a *App) DeleteImportedPart(moduleID string, force bool) error {
	if a.Users == nil {
		return errNoUsers
	}
	if used := a.PartUsedBy(moduleID); len(used) > 0 && !force {
		return fmt.Errorf("%s is used by %s; delete it anyway with force", moduleID, strings.Join(used, ", "))
	}
	return a.Users.Delete(moduleID)
}

// bundleFiles are a project's files plus the files of the imported parts its sketch uses, which
// is what Fritzing needs in the .fzz to know those parts.
func (a *App) bundleFiles(name string) ([]project.File, error) {
	all, err := a.Store.Files(name)
	if err != nil {
		return nil, err
	}
	files := all[:0:0]
	for _, f := range all {
		if f.Name != project.NotesFile { // Fritzing only expects its own files in the bundle
			files = append(files, f)
		}
	}
	if a.Users == nil || a.Users.Count() == 0 {
		return files, nil
	}
	var main string
	have := map[string]string{}
	for _, f := range files {
		have[f.Name] = f.Content
		if f.Name == project.MainFile {
			main = f.Content
		}
	}
	s, err := sketch.Parse(main)
	if err != nil {
		return files, nil // render will complain about the sketch itself
	}
	own := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f.Name, ".fzp") {
			if p, err := parts.ParseFZP([]byte(f.Content)); err == nil {
				own[p.ModuleID] = true
			}
		}
	}
	for _, id := range s.ModuleIDs() {
		if own[id] || !a.Users.Has(id) {
			continue
		}
		pf, err := a.Users.Files(id)
		if err != nil {
			return nil, err
		}
		for _, f := range pf {
			if prev, dup := have[f.Name]; dup {
				if prev == f.Content {
					continue
				}
				return nil, fmt.Errorf("imported part %s ships %s, which clashes with a different file of that name in the project or another imported part", id, f.Name)
			}
			have[f.Name] = f.Content
			files = append(files, project.File{Name: f.Name, Content: f.Content})
		}
	}
	return files, nil
}
