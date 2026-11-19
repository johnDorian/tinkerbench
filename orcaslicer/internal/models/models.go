// Package models stores the STL files that go on plates: uploads and the renders of OpenSCAD
// projects. Each model is <id>.stl plus <id>.json with its size and checksum.
package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/fsutil"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/stl"
)

// ErrNotFound is returned for an unknown model id.
var ErrNotFound = errors.New("model not found")

// ErrBadModel is returned when the data is not a usable STL.
var ErrBadModel = errors.New("not a usable STL")

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,100}$`)

// ValidID reports whether id has the shape of a model id (also safe as a file name).
func ValidID(id string) bool { return validID.MatchString(id) }

// Model is one STL with its geometry in millimetres, as the STL has it.
type Model struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Source          string     `json:"source"` // upload or openscad
	OpenSCADProject string     `json:"openscad_project,omitempty"`
	OpenSCADVersion string     `json:"openscad_version,omitempty"`
	Size            [3]float64 `json:"size"`
	Min             [3]float64 `json:"min"`
	Triangles       int        `json:"triangles"`
	Created         time.Time  `json:"created"`
	Updated         time.Time  `json:"updated"`
	SHA256          string     `json:"sha256"`
}

// Input is a new model, or the new geometry of an existing one.
type Input struct {
	Name            string
	Source          string
	OpenSCADProject string
	OpenSCADVersion string
	Data            []byte
}

type cached struct {
	sha  string
	tris []geom.Triangle
}

// Store is the models directory.
type Store struct {
	dir   string
	mu    sync.Mutex
	cache map[string]cached
}

// Open creates the models directory under dir if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: filepath.Join(dir, "models"), cache: map[string]cached{}}, nil
}

func (s *Store) meta(id string) string { return filepath.Join(s.dir, id+".json") }
func (s *Store) stl(id string) string  { return filepath.Join(s.dir, id+".stl") }

func (s *Store) get(id string) (Model, error) {
	if !ValidID(id) {
		return Model{}, ErrNotFound
	}
	var m Model
	if err := fsutil.ReadJSON(s.meta(id), &m); err != nil {
		if os.IsNotExist(err) {
			return Model{}, ErrNotFound
		}
		return Model{}, err
	}
	return m, nil
}

// Get returns one model's metadata.
func (s *Store) Get(id string) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(id)
}

// List returns every model, newest first.
func (s *Store) List() ([]Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := []Model{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		m, err := s.get(strings.TrimSuffix(f.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// STLPath returns the file holding a model's STL.
func (s *Store) STLPath(id string) (string, error) {
	if _, err := s.Get(id); err != nil {
		return "", err
	}
	return s.stl(id), nil
}

// Mesh returns a model's triangles (cached by checksum) together with its metadata.
func (s *Store) Mesh(id string) ([]geom.Triangle, Model, error) {
	m, err := s.Get(id)
	if err != nil {
		return nil, Model{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[id]; ok && c.sha == m.SHA256 {
		return c.tris, m, nil
	}
	data, err := os.ReadFile(s.stl(id))
	if err != nil {
		return nil, Model{}, err
	}
	tris, err := stl.Parse(data)
	if err != nil {
		return nil, Model{}, fmt.Errorf("model %s: %w", id, err)
	}
	if len(s.cache) >= 16 {
		for k := range s.cache {
			delete(s.cache, k)
			break
		}
	}
	s.cache[id] = cached{sha: m.SHA256, tris: tris}
	return tris, m, nil
}

// Create stores a new model.
func (s *Store) Create(in Input) (Model, error) {
	tris, err := stl.Parse(in.Data)
	if err != nil {
		return Model{}, fmt.Errorf("%w: %v", ErrBadModel, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "model"
	}
	now := time.Now().UTC()
	m := describe(tris, in, now)
	m.ID = fsutil.NewID(name)
	m.Name = name
	m.Created, m.Updated = now, now
	if err := s.write(m, in.Data); err != nil {
		return Model{}, err
	}
	return m, nil
}

// Replace gives an existing model new geometry. Its id, name and creation time are kept.
func (s *Store) Replace(id string, in Input) (Model, error) {
	tris, err := stl.Parse(in.Data)
	if err != nil {
		return Model{}, fmt.Errorf("%w: %v", ErrBadModel, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.get(id)
	if err != nil {
		return Model{}, err
	}
	m := describe(tris, in, time.Now().UTC())
	m.ID, m.Name, m.Created = old.ID, old.Name, old.Created
	m.Updated = time.Now().UTC()
	if in.Name != "" {
		m.Name = in.Name
	}
	if err := s.write(m, in.Data); err != nil {
		return Model{}, err
	}
	delete(s.cache, id)
	return m, nil
}

func describe(tris []geom.Triangle, in Input, now time.Time) Model {
	box := geom.Bounds(tris)
	sum := sha256.Sum256(in.Data)
	return Model{
		Source:          in.Source,
		OpenSCADProject: in.OpenSCADProject,
		OpenSCADVersion: in.OpenSCADVersion,
		Size:            box.Size(),
		Min:             box.Min,
		Triangles:       len(tris),
		SHA256:          hex.EncodeToString(sum[:]),
	}
}

func (s *Store) write(m Model, data []byte) error {
	if err := fsutil.WriteFile(s.stl(m.ID), data); err != nil {
		return err
	}
	return fsutil.WriteJSON(s.meta(m.ID), m)
}

// FindOpenSCAD returns the model imported from an OpenSCAD project, if there is one.
func (s *Store) FindOpenSCAD(project string) (Model, error) {
	list, err := s.List()
	if err != nil {
		return Model{}, err
	}
	for _, m := range list {
		if m.Source == "openscad" && m.OpenSCADProject == project {
			return m, nil
		}
	}
	return Model{}, ErrNotFound
}

// Delete removes a model. Callers check first that no plate uses it.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.get(id); err != nil {
		return err
	}
	delete(s.cache, id)
	if err := os.Remove(s.stl(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(s.meta(id))
}
