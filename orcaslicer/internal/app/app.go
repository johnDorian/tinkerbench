// Package app is the one place the REST API and the MCP tools do their work: models, plates,
// placement, slicing and the printer. Each change is saved and announced on the event hub.
package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/events"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/fsutil"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/openscad"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/printer"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/slicer"
)

// Config is what the app is set up with, from the environment.
type Config struct {
	PublicURL         string // how people reach the UI (http://localhost:8050)
	OpenSCADPublicURL string // links to the OpenSCAD workbench for people
	WebcamURL         string // optional
	OrcaVersion       string
	Machine           string // defaults for new plates
	Process           string
	Filament          string
}

// App holds the services. Mutating methods take mu, so a read-modify-write of a plate is atomic
// with respect to the UI and the agent.
type App struct {
	Cfg      Config
	Profiles *profiles.Library
	Models   *models.Store
	Plates   *plates.Store
	Slicer   *slicer.Slicer
	OpenSCAD *openscad.Client
	Printer  *printer.Client
	Hub      *events.Hub

	mu sync.Mutex
}

// Error is a failure with the HTTP status it should be reported with.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// StatusOf returns the HTTP status for an error from this package.
func StatusOf(err error) int {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e.Code
	case errors.Is(err, plates.ErrNotFound), errors.Is(err, models.ErrNotFound),
		errors.Is(err, profiles.ErrNotFound), errors.Is(err, openscad.ErrNotFound):
		return 404
	case errors.Is(err, models.ErrBadModel):
		return 400
	case errors.Is(err, openscad.ErrNotRendered), errors.Is(err, printer.ErrDisabled):
		return 409
	case errors.Is(err, plates.ErrNoRoom):
		return 422
	}
	return 500
}

// ObjectView is an object with what the UI needs to draw it: its world bounds, and the transform
// that puts the model's STL (in its own coordinates) on the bed, as 16 column-major numbers
// (three.js Matrix4.fromArray).
type ObjectView struct {
	plates.Object
	Bounds    [2][3]float64 `json:"bounds"`
	Transform [16]float64   `json:"transform"`
}

// PlateView is a plate with its objects' geometry.
type PlateView struct {
	plates.Plate
	Objects      []ObjectView `json:"objects"`
	MultiBed     bool         `json:"multi_bed"`     // the printer profile lets the plate choose its bed type
	EffectiveBed string       `json:"effective_bed"` // the bed type the plate is sliced with
}

// ConfigView is what the UI needs to know about the server.
type ConfigView struct {
	Bed               profiles.Bed       `json:"bed"`
	OrcaVersion       string             `json:"orca_version"`
	PrinterEnabled    bool               `json:"printer_enabled"`
	WebcamURL         string             `json:"webcam_url"`
	OpenSCADURLPublic string             `json:"openscad_url_public"`
	MCPURL            string             `json:"mcp_url"`
	Defaults          Defaults           `json:"defaults"`
	CuratedSettings   []profiles.Setting `json:"curated_settings"`
	Beds              []string           `json:"beds"`
}

// Defaults are the presets new plates start with.
type Defaults struct {
	Machine  string `json:"machine"`
	Process  string `json:"process"`
	Filament string `json:"filament"`
}

// ObjectSpec is a new object on a plate; nil fields take their defaults.
type ObjectSpec struct {
	Model    string
	X, Y     *float64
	Rotation *[3]float64
	Scale    *float64
}

// ObjectPatch changes an object; nil fields are left alone.
type ObjectPatch struct {
	Name     *string
	X, Y     *float64
	Rotation *[3]float64
	Scale    *float64
}

// PlatePatch changes a plate; nil fields are left alone. An override set to nil is removed.
type PlatePatch struct {
	Name, Machine, Process, Filament *string
	Overrides                        map[string]*string
}

func (a *App) publish(kind, id string, v any) {
	a.Hub.Publish(events.Merge(kind, id, v))
}

// Config returns the server settings for the UI, including the default presets' values for the
// curated settings.
func (a *App) Config() (ConfigView, error) {
	bed, err := a.Profiles.Bed(a.Cfg.Machine)
	if err != nil {
		return ConfigView{}, err
	}
	proc, err := a.Profiles.Flatten(profiles.Process, a.Cfg.Process)
	if err != nil {
		return ConfigView{}, err
	}
	fil, err := a.Profiles.Flatten(profiles.Filament, a.Cfg.Filament)
	if err != nil {
		return ConfigView{}, err
	}
	curated := make([]profiles.Setting, len(profiles.Curated))
	for i, s := range profiles.Curated {
		src := proc
		if s.Source == profiles.Filament {
			src = fil
		}
		s.Default = profiles.Str(src, s.Key)
		curated[i] = s
	}
	return ConfigView{
		Bed:               bed,
		OrcaVersion:       a.Cfg.OrcaVersion,
		PrinterEnabled:    a.Printer.Enabled(),
		WebcamURL:         a.Cfg.WebcamURL,
		OpenSCADURLPublic: a.Cfg.OpenSCADPublicURL,
		MCPURL:            a.Cfg.PublicURL + "/mcp",
		Defaults:          Defaults{Machine: a.Cfg.Machine, Process: a.Cfg.Process, Filament: a.Cfg.Filament},
		CuratedSettings:   curated,
		Beds:              plates.Beds(),
	}, nil
}

// ListProfiles lists the machines and the presets compatible with one (default: the default machine).
func (a *App) ListProfiles(machine string) (profiles.Listing, error) {
	if machine == "" {
		machine = a.Cfg.Machine
	}
	return a.Profiles.List(machine)
}

// Profile returns the flattened settings of one preset.
func (a *App) Profile(kind, name string) (map[string]any, error) {
	k, err := profiles.ParseKind(kind)
	if err != nil {
		return nil, errf(400, "%v", err)
	}
	return a.Profiles.Flatten(k, name)
}

// ---------- models ----------

// ListModels returns every model, newest first.
func (a *App) ListModels() ([]models.Model, error) { return a.Models.List() }

// GetModel returns one model's metadata.
func (a *App) GetModel(id string) (models.Model, error) { return a.Models.Get(id) }

// UploadModel stores an uploaded STL (ASCII or binary).
func (a *App) UploadModel(name string, data []byte) (models.Model, error) {
	if len(data) == 0 {
		return models.Model{}, errf(400, "the file is empty")
	}
	m, err := a.Models.Create(models.Input{Name: name, Source: "upload", Data: data})
	if err != nil {
		return models.Model{}, err
	}
	a.publish("models", m.ID, nil)
	return m, nil
}

// DeleteModel removes a model that no plate uses.
func (a *App) DeleteModel(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	list, err := a.Plates.List()
	if err != nil {
		return err
	}
	for _, p := range list {
		for _, o := range p.Objects {
			if o.Model == id {
				return errf(409, "model %s is on plate %q; remove it from the plate first", id, p.Name)
			}
		}
	}
	if err := a.Models.Delete(id); err != nil {
		return err
	}
	a.publish("models", id, nil)
	return nil
}

// ModelSTLPath returns the file of a model's STL.
func (a *App) ModelSTLPath(id string) (string, error) { return a.Models.STLPath(id) }

// ---------- OpenSCAD ----------

// OpenSCADProjects lists the workbench's projects.
func (a *App) OpenSCADProjects(ctx context.Context) ([]openscad.Project, error) {
	list, err := a.OpenSCAD.List(ctx)
	if err != nil {
		return nil, errf(502, "%v", err)
	}
	return list, nil
}

// ImportOpenSCAD takes the last rendered model of a project. A project imported before replaces
// its model in place (same id), so plates using it get the new geometry and show stale slices.
// With plateID the model is also placed on that plate.
func (a *App) ImportOpenSCAD(ctx context.Context, project, plateID string) (models.Model, error) {
	p, err := a.OpenSCAD.Project(ctx, project)
	if errors.Is(err, openscad.ErrNotFound) {
		return models.Model{}, errf(404, "no OpenSCAD project called %q", project)
	}
	if err != nil {
		return models.Model{}, errf(502, "%v", err)
	}
	data, err := a.OpenSCAD.STL(ctx, project)
	if errors.Is(err, openscad.ErrNotRendered) {
		return models.Model{}, errf(409, "OpenSCAD project %q has no rendered model yet; render it first", project)
	}
	if err != nil {
		return models.Model{}, errf(502, "%v", err)
	}
	in := models.Input{Name: project, Source: "openscad", OpenSCADProject: project,
		OpenSCADVersion: fmt.Sprint(p.Version), Data: data}
	m, err := a.Models.FindOpenSCAD(project)
	switch {
	case err == nil:
		m, err = a.Models.Replace(m.ID, in)
	case errors.Is(err, models.ErrNotFound):
		m, err = a.Models.Create(in)
	}
	if err != nil {
		return models.Model{}, err
	}
	a.publish("models", m.ID, nil)
	if plateID != "" {
		if _, err := a.AddObject(plateID, ObjectSpec{Model: m.ID}); err != nil {
			return m, err
		}
	}
	return m, nil
}

// ---------- plates ----------

// ListPlates returns every plate, most recently changed first.
func (a *App) ListPlates() ([]plates.Plate, error) { return a.Plates.List() }

// GetPlate returns a plate with its objects' geometry.
func (a *App) GetPlate(id string) (PlateView, error) {
	p, err := a.Plates.Get(id)
	if err != nil {
		return PlateView{}, err
	}
	return a.view(p)
}

func (a *App) view(p plates.Plate) (PlateView, error) {
	views := []ObjectView{}
	for _, o := range p.Objects {
		tris, _, err := a.Models.Mesh(o.Model)
		if err != nil {
			return PlateView{}, fmt.Errorf("object %s: %w", o.ID, err)
		}
		tr, box := plates.PlaceObject(o, tris)
		views = append(views, ObjectView{
			Object:    o,
			Bounds:    [2][3]float64{box.Min, box.Max},
			Transform: transform16(tr),
		})
	}
	v := PlateView{Plate: p, Objects: views, EffectiveBed: plates.SingleBed}
	if m, err := a.Profiles.Flatten(profiles.Machine, p.Machine); err == nil {
		v.MultiBed = plates.MultiBed(m)
		v.EffectiveBed = plates.EffectiveBed(m, p.Bed)
	}
	return v, nil
}

func transform16(t geom.Transform) [16]float64 {
	a := t.A
	return [16]float64{
		a[0][0], a[1][0], a[2][0], 0,
		a[0][1], a[1][1], a[2][1], 0,
		a[0][2], a[1][2], a[2][2], 0,
		t.B[0], t.B[1], t.B[2], 1,
	}
}

// DropBedOverrides removes the bed_type / curr_bed_type overrides an agent used to set (OrcaSlicer
// ignores them; the bed type follows the printer profile). Called once at start-up.
func (a *App) DropBedOverrides() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	list, err := a.Plates.List()
	if err != nil {
		return err
	}
	for _, p := range list {
		changed := false
		for _, k := range []string{"bed_type", "curr_bed_type"} {
			if _, ok := p.Overrides[k]; ok {
				delete(p.Overrides, k)
				changed = true
			}
		}
		if changed {
			if err := a.Plates.Save(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// CreatePlate makes a plate; presets default to the configured ones.
func (a *App) CreatePlate(name, machine, process, filament string) (plates.Plate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return plates.Plate{}, errf(400, "name is required")
	}
	if machine == "" {
		machine = a.Cfg.Machine
	}
	if process == "" {
		process = a.Cfg.Process
	}
	if filament == "" {
		filament = a.Cfg.Filament
	}
	now := time.Now().UTC()
	p := plates.Plate{
		ID: fsutil.NewID(name), Name: name, Machine: machine, Process: process, Filament: filament,
		Overrides: map[string]string{}, Objects: []plates.Object{}, Created: now, Updated: now,
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.validate(&p); err != nil {
		return plates.Plate{}, err
	}
	if err := a.Plates.Save(p); err != nil {
		return plates.Plate{}, err
	}
	a.publish("plate", p.ID, nil)
	return p, nil
}

// PutPlate replaces a plate's content (the id and creation time are kept).
func (a *App) PutPlate(id string, body plates.Plate) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, err := a.Plates.Get(id)
	if err != nil {
		return PlateView{}, err
	}
	body.ID, body.Created, body.Updated = id, cur.Created, time.Now().UTC()
	if err := a.validate(&body); err != nil {
		return PlateView{}, err
	}
	if err := a.Plates.Save(body); err != nil {
		return PlateView{}, err
	}
	a.publish("plate", id, nil)
	return a.view(body)
}

// UpdatePlate changes a plate's name, presets or overrides.
func (a *App) UpdatePlate(id string, patch PlatePatch) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(id)
	if err != nil {
		return PlateView{}, err
	}
	if patch.Name != nil {
		p.Name = strings.TrimSpace(*patch.Name)
	}
	for _, f := range []struct {
		dst *string
		src *string
	}{{&p.Machine, patch.Machine}, {&p.Process, patch.Process}, {&p.Filament, patch.Filament}} {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if p.Overrides == nil {
		p.Overrides = map[string]string{}
	}
	for k, v := range patch.Overrides {
		if v == nil {
			delete(p.Overrides, k)
		} else {
			p.Overrides[k] = *v
		}
	}
	p.Updated = time.Now().UTC()
	if err := a.validate(&p); err != nil {
		return PlateView{}, err
	}
	if err := a.Plates.Save(p); err != nil {
		return PlateView{}, err
	}
	a.publish("plate", id, nil)
	return a.view(p)
}

// DeletePlate removes a plate. A plate being sliced can't be deleted.
func (a *App) DeletePlate(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.Plates.Get(id); err != nil {
		return err
	}
	if r, _, _ := a.Slicer.Get(id); r.Status == slicer.StatusQueued || r.Status == slicer.StatusSlicing {
		return errf(409, "the plate is being sliced; try again when it is done")
	}
	if err := a.Plates.Delete(id); err != nil {
		return err
	}
	a.publish("plate", id, nil)
	return nil
}

// validate checks a plate's presets, overrides and objects, and fills in missing object ids,
// names and scales.
func (a *App) validate(p *plates.Plate) error {
	if strings.TrimSpace(p.Name) == "" {
		return errf(400, "name is required")
	}
	if err := a.Profiles.CheckNames(p.Machine, p.Process, p.Filament); err != nil {
		return errf(400, "%v", err)
	}
	if p.Bed != "" && !plates.ValidBed(p.Bed) {
		return errf(400, "bed %q is not a bed type (use one of Cool Plate, Engineering Plate, High Temp Plate, Textured PEI Plate)", p.Bed)
	}
	proc, err := a.Profiles.Flatten(profiles.Process, p.Process)
	if err != nil {
		return errf(400, "%v", err)
	}
	fil, err := a.Profiles.Flatten(profiles.Filament, p.Filament)
	if err != nil {
		return errf(400, "%v", err)
	}
	for k := range p.Overrides {
		if k == "bed_type" || k == "curr_bed_type" {
			return errf(400, "override %q is not allowed; the bed type follows the printer profile", k)
		}
		if !profiles.Has(proc, k) && !profiles.Has(fil, k) {
			return errf(400, "override %q is not a process or filament setting", k)
		}
	}
	seen := map[string]bool{}
	for i := range p.Objects {
		o := &p.Objects[i]
		if o.ID == "" || seen[o.ID] {
			o.ID = a.freeObjectID(*p, seen)
		}
		seen[o.ID] = true
		if o.Scale == 0 {
			o.Scale = 1
		}
		if o.Scale < 0 || math.IsNaN(o.Scale) || math.IsInf(o.Scale, 0) {
			return errf(400, "object %s: scale must be positive", o.ID)
		}
		m, err := a.Models.Get(o.Model)
		if err != nil {
			return errf(400, "object %s: model %q not found", o.ID, o.Model)
		}
		if o.Name == "" {
			o.Name = m.Name
		}
	}
	return nil
}

// freeObjectID returns "o<n>" with the next n not used on the plate (or in seen).
func (a *App) freeObjectID(p plates.Plate, seen map[string]bool) string {
	used := map[string]bool{}
	for _, o := range p.Objects {
		used[o.ID] = true
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("o%d", n)
		if !used[id] && !seen[id] {
			return id
		}
	}
}

// ---------- objects ----------

// AddObject puts a model on a plate. Without x and y it is placed in free space.
func (a *App) AddObject(plateID string, spec ObjectSpec) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return PlateView{}, err
	}
	m, err := a.Models.Get(spec.Model)
	if err != nil {
		return PlateView{}, errf(400, "model %q not found", spec.Model)
	}
	o := plates.Object{ID: a.freeObjectID(p, nil), Model: m.ID, Name: m.Name, Scale: 1}
	if spec.Rotation != nil {
		o.Rotation = *spec.Rotation
	}
	if spec.Scale != nil {
		if *spec.Scale <= 0 {
			return PlateView{}, errf(400, "scale must be positive")
		}
		o.Scale = *spec.Scale
	}
	if spec.X == nil || spec.Y == nil {
		if err := a.autoPlace(p, &o); err != nil {
			return PlateView{}, err
		}
	}
	if spec.X != nil {
		o.X = *spec.X
	}
	if spec.Y != nil {
		o.Y = *spec.Y
	}
	p.Objects = append(p.Objects, o)
	return a.savePlate(p)
}

// autoPlace sets o's position to free space on the plate's bed.
func (a *App) autoPlace(p plates.Plate, o *plates.Object) error {
	bed, err := a.Profiles.Bed(p.Machine)
	if err != nil {
		return err
	}
	tris, _, err := a.Models.Mesh(o.Model)
	if err != nil {
		return err
	}
	size := plates.Footprint(tris, o.Rotation, o.Scale).Size()
	placed, err := plates.Boxes(p, a.Models)
	if err != nil {
		return err
	}
	o.X, o.Y = plates.AutoPlace(bed, placed, size[0], size[1])
	return nil
}

// PatchObject changes one object's name, position, rotation or scale.
func (a *App) PatchObject(plateID, objectID string, patch ObjectPatch) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return PlateView{}, err
	}
	i := objectIndex(p, objectID)
	if i < 0 {
		return PlateView{}, errf(404, "object %q not on plate %q", objectID, plateID)
	}
	o := &p.Objects[i]
	if patch.Name != nil {
		o.Name = *patch.Name
	}
	if patch.X != nil {
		o.X = *patch.X
	}
	if patch.Y != nil {
		o.Y = *patch.Y
	}
	if patch.Rotation != nil {
		o.Rotation = *patch.Rotation
	}
	if patch.Scale != nil {
		if *patch.Scale <= 0 {
			return PlateView{}, errf(400, "scale must be positive")
		}
		o.Scale = *patch.Scale
	}
	return a.savePlate(p)
}

// RemoveObject takes an object off a plate.
func (a *App) RemoveObject(plateID, objectID string) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return PlateView{}, err
	}
	i := objectIndex(p, objectID)
	if i < 0 {
		return PlateView{}, errf(404, "object %q not on plate %q", objectID, plateID)
	}
	p.Objects = append(p.Objects[:i], p.Objects[i+1:]...)
	return a.savePlate(p)
}

// DuplicateObject copies an object into free space on the plate.
func (a *App) DuplicateObject(plateID, objectID string) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return PlateView{}, err
	}
	i := objectIndex(p, objectID)
	if i < 0 {
		return PlateView{}, errf(404, "object %q not on plate %q", objectID, plateID)
	}
	o := p.Objects[i]
	o.ID = a.freeObjectID(p, nil)
	if err := a.autoPlace(p, &o); err != nil {
		return PlateView{}, err
	}
	p.Objects = append(p.Objects, o)
	return a.savePlate(p)
}

// Arrange packs the plate's objects onto the bed (spacing in mm, default 6). Rotations and
// scales are kept.
func (a *App) Arrange(plateID string, spacing *float64) (PlateView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return PlateView{}, err
	}
	gap := 6.0
	if spacing != nil {
		if *spacing < 0 {
			return PlateView{}, errf(400, "spacing must be 0 or more")
		}
		gap = *spacing
	}
	bed, err := a.Profiles.Bed(p.Machine)
	if err != nil {
		return PlateView{}, err
	}
	items := make([]plates.Item, 0, len(p.Objects))
	for _, o := range p.Objects {
		tris, _, err := a.Models.Mesh(o.Model)
		if err != nil {
			return PlateView{}, err
		}
		size := plates.Footprint(tris, o.Rotation, o.Scale).Size()
		items = append(items, plates.Item{ID: o.ID, W: size[0], D: size[1]})
	}
	centres, err := plates.Arrange(bed, items, gap)
	if err != nil {
		return PlateView{}, err
	}
	for i := range p.Objects {
		c := centres[p.Objects[i].ID]
		p.Objects[i].X, p.Objects[i].Y = c[0], c[1]
	}
	return a.savePlate(p)
}

func objectIndex(p plates.Plate, id string) int {
	for i, o := range p.Objects {
		if o.ID == id {
			return i
		}
	}
	return -1
}

// savePlate stores a changed plate, announces it and returns its view.
func (a *App) savePlate(p plates.Plate) (PlateView, error) {
	p.Updated = time.Now().UTC()
	if err := a.Plates.Save(p); err != nil {
		return PlateView{}, err
	}
	a.publish("plate", p.ID, nil)
	return a.view(p)
}

// ---------- slicing ----------

// Slice queues a slice of a plate and returns its state (queued or slicing).
func (a *App) Slice(plateID string) (slicer.Result, error) {
	if !a.Plates.Exists(plateID) {
		return slicer.Result{}, plates.ErrNotFound
	}
	if _, err := a.Slicer.Enqueue(plateID); err != nil {
		if errors.Is(err, plates.ErrNotFound) {
			return slicer.Result{}, err
		}
		return slicer.Result{}, errf(503, "%v", err)
	}
	return a.SliceStatus(plateID)
}

// SliceStatus returns the stored slice of a plate, with Stale set when the plate has changed
// since it was sliced.
func (a *App) SliceStatus(plateID string) (slicer.Result, error) {
	res, hash, err := a.Slicer.Get(plateID)
	if err != nil {
		return slicer.Result{}, err
	}
	res.Stale = a.stale(plateID, res, hash)
	return res, nil
}

func (a *App) stale(plateID string, res slicer.Result, hash string) bool {
	if res.Status != slicer.StatusDone {
		return false
	}
	p, err := a.Plates.Get(plateID)
	if err != nil {
		return false
	}
	cur, err := plates.Hash(p, a.Models, a.Cfg.OrcaVersion)
	return err != nil || cur != hash
}

// onSliceChange announces a slice status change.
func (a *App) onSliceChange(plateID string, r slicer.Result) {
	_, hash, _ := a.Slicer.Get(plateID)
	r.Stale = a.stale(plateID, r, hash)
	a.publish("slice", plateID, r)
}

// PrintFile returns the G-code and its download name of a plate that can be printed, or the
// reason it can't.
func (a *App) PrintFile(plateID string) (string, string, error) {
	res, hash, err := a.Slicer.Get(plateID)
	if err != nil {
		return "", "", err
	}
	if res.Status != slicer.StatusDone {
		return "", "", errf(409, "the plate has not been sliced (status %s)", res.Status)
	}
	if a.stale(plateID, res, hash) {
		return "", "", errf(409, "the plate changed since it was sliced; slice it again")
	}
	path := filepath.Join(a.Plates.Dir(plateID), "plate.gcode")
	if _, err := os.Stat(path); err != nil {
		return "", "", errf(409, "no G-code for this plate; slice it again")
	}
	return path, res.GcodeName, nil
}

// PlateFile returns the path of a plate's plate.gcode or plate.3mf.
func (a *App) PlateFile(plateID, name string) (string, error) {
	if !a.Plates.Exists(plateID) {
		return "", plates.ErrNotFound
	}
	path := filepath.Join(a.Plates.Dir(plateID), name)
	if _, err := os.Stat(path); err != nil {
		return "", errf(404, "%s not made yet; slice the plate", name)
	}
	return path, nil
}

// ---------- printer ----------

// PrinterStatus returns the printer's state (cached about two seconds).
func (a *App) PrinterStatus(ctx context.Context) printer.Status {
	return a.Printer.Status(ctx)
}

// PrintPlate uploads a plate's G-code to the printer and starts it.
func (a *App) PrintPlate(ctx context.Context, plateID string) (string, error) {
	path, name, err := a.PrintFile(plateID)
	if err != nil {
		return "", err
	}
	if !a.Printer.Enabled() {
		return "", errf(409, "printing is disabled on this server")
	}
	st := a.Printer.Status(ctx)
	switch {
	case !st.Online:
		return "", errf(409, "the printer is offline")
	case st.State == "printing" || st.State == "paused":
		return "", errf(409, "the printer is busy (%s %s)", st.State, st.Filename)
	case st.State == "error":
		return "", errf(409, "the printer is in an error state: %s", st.Message)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := a.Printer.Upload(ctx, name, f); err != nil {
		return "", errf(502, "%v", err)
	}
	a.publish("printer", "", a.Printer.Status(ctx))
	return name, nil
}

// PrinterAction pauses, resumes or cancels the current print.
func (a *App) PrinterAction(ctx context.Context, action string) error {
	if !a.Printer.Enabled() {
		return printer.ErrDisabled
	}
	st := a.Printer.Status(ctx)
	if !st.Online {
		return errf(409, "the printer is offline")
	}
	ok := map[string][]string{"pause": {"printing"}, "resume": {"paused"}, "cancel": {"printing", "paused"}}
	states, known := ok[action]
	if !known {
		return errf(400, "unknown action %q", action)
	}
	if !containsState(states, st.State) {
		return errf(409, "cannot %s: the printer is %s", action, st.State)
	}
	if err := a.Printer.Action(ctx, action); err != nil {
		return errf(502, "%v", err)
	}
	a.publish("printer", "", a.Printer.Status(ctx))
	return nil
}

func containsState(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Poll publishes the printer state every interval while someone is listening.
func (a *App) Poll(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.Hub.Subscribers() > 0 {
				a.publish("printer", "", a.Printer.Status(ctx))
			}
		}
	}
}

// Wire connects the slicer's change notifications to the event hub. Call once after New.
func (a *App) Wire() {
	a.Slicer.SetOnChange(a.onSliceChange)
}
