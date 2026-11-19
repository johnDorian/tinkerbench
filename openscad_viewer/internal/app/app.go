// Package app ties the project store, the renderer and the event hub together. The web UI
// and MCP both go through it, so every change is versioned, rendered and announced the same way.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/parts"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// RenderMode says what happens after a source change.
type RenderMode int

const (
	NoRender    RenderMode = iota
	RenderSync             // render before returning (MCP: the agent gets errors right away)
	RenderAsync            // render in the background (web UI: the result arrives as an event)
)

type App struct {
	Store    *project.Store
	Renderer *render.Renderer
	Hub      *events.Hub
	parts    *parts.Manager
}

// New wires the store, renderer and event hub together.
func New(store *project.Store, r *render.Renderer, hub *events.Hub) *App {
	a := &App{Store: store, Renderer: r, Hub: hub}
	a.parts = parts.NewManager(r, a.partsDone)
	return a
}

// SaveResult is returned by every source change.
type SaveResult struct {
	Version project.Version `json:"version"`
	Render  *render.Result  `json:"render,omitempty"`
}

func (a *App) publish(kind, name, file, by string) {
	a.Hub.Publish(events.Event{Kind: kind, Project: name, File: file, Source: by})
}

func (a *App) after(ctx context.Context, name, file, by string, v project.Version, mode RenderMode) (SaveResult, error) {
	a.publish(events.FileChanged, name, file, by)
	res := SaveResult{Version: v}
	switch mode {
	case RenderSync:
		r, err := a.Render(ctx, name, nil, by)
		if err != nil {
			return res, err
		}
		res.Render = &r
	case RenderAsync:
		go func() {
			if _, err := a.Render(context.Background(), name, nil, by); err != nil {
				log.Printf("render %s: %v", name, err)
			}
		}()
	}
	return res, nil
}

// Create makes a project and renders it.
func (a *App) Create(ctx context.Context, name, description, source, by string, mode RenderMode) (project.Info, *render.Result, error) {
	info, err := a.Store.Create(name, description, source, by)
	if err != nil {
		return info, nil, err
	}
	a.publish(events.ProjectCreated, name, "", by)
	res, err := a.after(ctx, name, project.MainFile, by, project.Version{}, mode)
	return info, res.Render, err
}

func (a *App) WriteFile(ctx context.Context, name, file, content, by, note string, mode RenderMode) (SaveResult, error) {
	v, err := a.Store.WriteFile(name, file, content, by, note)
	if err != nil {
		return SaveResult{}, err
	}
	return a.after(ctx, name, file, by, v, mode)
}

func (a *App) EditFile(ctx context.Context, name, file, old, new string, all bool, by, note string, mode RenderMode) (SaveResult, error) {
	v, err := a.Store.EditFile(name, file, old, new, all, by, note)
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
	info, err := a.Store.UpdateMeta(name, &description, nil)
	if err == nil {
		a.publish(events.ProjectUpdated, name, "", by)
	}
	return info, err
}

// Render renders main.scad. params replace the project's stored customizer params
// (an empty map clears them); nil keeps the stored ones.
func (a *App) Render(ctx context.Context, name string, params map[string]string, by string) (render.Result, error) {
	info, err := a.Store.Get(name)
	if err != nil {
		return render.Result{}, err
	}
	if params != nil {
		for k := range params {
			if !render.ValidParamName(k) {
				return render.Result{}, fmt.Errorf("invalid parameter name %q", k)
			}
		}
		if info, err = a.Store.UpdateMeta(name, nil, params); err != nil {
			return render.Result{}, err
		}
		a.publish(events.ProjectUpdated, name, "", by)
	}
	a.publish(events.RenderStarted, name, "", by)
	dir := a.Store.Dir(name)
	a.parts.Cancel(dir) // part renders of the previous model are no longer wanted
	res, err := a.Renderer.Render(ctx, dir, project.MainFile, info.Params)
	ok := err == nil && res.OK
	a.Hub.Publish(events.Event{Kind: events.RenderDone, Project: name, Source: by, OK: &ok})
	if ok {
		a.parts.Start(name, dir, res) // the listing is written now; the part renders follow
		a.recheck(name)
	}
	return res, err
}

// partsDone runs when a model's part renders have committed (or it has no parts).
func (a *App) partsDone(name string, ok bool) {
	a.recheck(name)
	a.Hub.Publish(events.Event{Kind: events.PartsDone, Project: name, OK: &ok})
}

// recheck re-tests the anchors of a project's comments against the current model: an anchor
// on a part is checked against that part's mesh when it is current, otherwise against
// model.stl. Anchors that no longer have geometry under them are flagged stale.
func (a *App) recheck(name string) {
	dir := a.Store.Dir(name)
	st, err := os.Stat(render.STLPath(dir))
	if err != nil {
		return
	}
	cache := map[string][]render.Triangle{}
	load := func(key, path string) []render.Triangle {
		if t, ok := cache[key]; ok {
			return t
		}
		t, err := render.ReadTrianglesFile(path)
		if err != nil {
			t = nil
		}
		cache[key] = t
		return t
	}
	lst, _ := parts.Read(dir)
	current := lst != nil && lst.ModelTime != nil && lst.ModelTime.Equal(st.ModTime())
	fits := func(an project.Anchor) bool {
		if current && an.Part != "" {
			for i, p := range lst.Parts {
				if p.Name == an.Part && p.OK && p.Triangles > 0 {
					return parts.Fits(load("part"+strconv.Itoa(i), parts.PartPath(dir, i)), an)
				}
			}
		}
		return parts.Fits(load("model", render.STLPath(dir)), an)
	}
	changed, err := a.Store.UpdateAnchors(name, fits)
	if err == nil && changed > 0 {
		a.commentEvent(name, "", "", fmt.Sprintf("re-checked anchors: %d changed", changed))
	}
}

// Parts is the parts listing of a project (the same data as GET /parts and list_parts).
func (a *App) Parts(name string) (parts.Listing, error) {
	if _, err := a.Store.Get(name); err != nil {
		return parts.Listing{}, err
	}
	dir := a.Store.Dir(name)
	l, err := parts.Read(dir)
	if err != nil {
		return parts.Listing{}, err
	}
	out := parts.Listing{Parts: []parts.Part{}, Features: []parts.Feature{}}
	if l != nil {
		out = *l
		if out.Parts == nil {
			out.Parts = []parts.Part{}
		}
		if out.Features == nil {
			out.Features = []parts.Feature{}
		}
	}
	out.Pending = a.parts.Active(dir)
	return out, nil
}

// PartFile is the STL file of a part, by name.
func (a *App) PartFile(name, part string) (string, error) {
	l, err := a.Parts(name)
	if err != nil {
		return "", err
	}
	dir := a.Store.Dir(name)
	for i, p := range l.Parts {
		if p.Name != part {
			continue
		}
		path := parts.PartPath(dir, i)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("part %q is not rendered: %w", part, project.ErrNotFound)
		}
		return path, nil
	}
	return "", fmt.Errorf("part %q: %w", part, project.ErrNotFound)
}

func (a *App) commentEvent(name, file, by, detail string) {
	a.Hub.Publish(events.Event{Kind: events.CommentsChanged, Project: name, File: file, Source: by, Detail: detail})
}

// AddComment comments on lines of a file. With an anchor on the model, file and line may be
// left out: the comment goes on the line where the anchored part is declared, or stays a
// model-only comment when there is no such line.
func (a *App) AddComment(name, file string, line, endLine int, body string, suggestion *string, anchor *project.Anchor, by string) (project.Comment, error) {
	if _, err := a.Store.Get(name); err != nil {
		return project.Comment{}, err
	}
	if anchor != nil && line == 0 {
		// The anchored part's declaration line, if it is in the file asked for (or in any file).
		f, l := parts.Locate(a.Store.Dir(name), "part", anchor.Part)
		if l > 0 && (file == "" || f == file) {
			file, line = f, l
		} else {
			file, line = "", 0 // model-only
		}
	} else if anchor != nil && file == "" {
		file = project.MainFile
	}
	c, err := a.Store.AddComment(name, file, line, endLine, body, suggestion, anchor, by)
	if err == nil && anchor != nil {
		a.recheck(name) // the anchor is judged against the current model right away
		if all, lerr := a.Store.Comments(name, "", true); lerr == nil {
			for _, o := range all {
				if o.ID == c.ID {
					c = o
				}
			}
		}
	}
	if err == nil {
		what := "commented"
		if c.Suggestion != nil {
			what = "suggested a change"
		}
		if c.File == "" {
			a.commentEvent(name, "", by, fmt.Sprintf("%s on the model (#%d)", what, c.ID))
		} else {
			a.commentEvent(name, file, by, fmt.Sprintf("%s on %s:%d (#%d)", what, c.File, c.Line, c.ID))
		}
	}
	return c, err
}

func (a *App) ReplyComment(name string, id int, body, by string) (project.Comment, error) {
	c, err := a.Store.ReplyComment(name, id, by, body)
	if err == nil {
		a.commentEvent(name, c.File, by, fmt.Sprintf("replied to #%d", id))
	}
	return c, err
}

func (a *App) ResolveComment(name string, id int, resolved bool, by string) (project.Comment, error) {
	c, err := a.Store.ResolveComment(name, id, resolved, by)
	if err == nil {
		what := "resolved"
		if !resolved {
			what = "reopened"
		}
		a.commentEvent(name, c.File, by, fmt.Sprintf("%s #%d", what, id))
	}
	return c, err
}

func (a *App) DeleteComment(name string, id int, by string) error {
	err := a.Store.DeleteComment(name, id)
	if err == nil {
		a.commentEvent(name, "", by, fmt.Sprintf("deleted #%d", id))
	}
	return err
}

// ApplySuggestion writes a comment's suggested code into the file (a new version), resolves
// the comment and renders.
func (a *App) ApplySuggestion(ctx context.Context, name string, id int, by string, mode RenderMode) (project.Comment, SaveResult, error) {
	c, v, err := a.Store.ApplySuggestion(name, id, by)
	if err != nil {
		return c, SaveResult{}, err
	}
	a.commentEvent(name, c.File, by, fmt.Sprintf("applied suggestion #%d", id))
	res, err := a.after(ctx, name, c.File, by, v, mode)
	return c, res, err
}

// LastRender is the stored result of the last render, or nil.
func (a *App) LastRender(name string) *render.Result {
	return render.Last(a.Store.Dir(name))
}

// Snapshot renders PNGs of the model for the given views.
func (a *App) Snapshot(ctx context.Context, name string, views []string, w, h int, fromSource bool) ([][]byte, error) {
	info, err := a.Store.Get(name)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, v := range views {
		png, err := a.Renderer.Snapshot(ctx, a.Store.Dir(name), project.MainFile, render.SnapshotOptions{
			View: v, Width: w, Height: h, FromSource: fromSource, Params: info.Params})
		if err != nil {
			return nil, err
		}
		out = append(out, png)
	}
	return out, nil
}

// FormatParams turns JSON values into OpenSCAD expressions for -D: numbers and booleans as
// they are, strings quoted, arrays as vectors.
func FormatParams(in map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s, err := formatValue(in[k])
		if err != nil {
			return nil, fmt.Errorf("param %s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func formatValue(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "undef", nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return "", fmt.Errorf("invalid number")
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case string:
		b, _ := json.Marshal(x) // JSON string escaping is valid OpenSCAD string syntax
		return string(b), nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			s, err := formatValue(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	}
	return "", fmt.Errorf("unsupported value %v (%T)", v, v)
}
