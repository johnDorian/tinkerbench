// Package plates stores print plates: which models sit where on which machine, with which
// presets and overrides. A plate is plates/<id>/plate.json; the slicer adds its files next to it.
package plates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/fsutil"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
)

// ErrNotFound is returned for an unknown plate id.
var ErrNotFound = errors.New("plate not found")

// Object is one model on a plate. X, Y is the centre of its footprint on the bed.
type Object struct {
	ID       string     `json:"id"`
	Model    string     `json:"model"`
	Name     string     `json:"name"`
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	Rotation [3]float64 `json:"rotation"`
	Scale    float64    `json:"scale"`
}

// Plate is what gets printed together in one slice.
type Plate struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Machine   string            `json:"machine"`
	Process   string            `json:"process"`
	Filament  string            `json:"filament"`
	Bed       string            `json:"bed"`
	Overrides map[string]string `json:"overrides"`
	Objects   []Object          `json:"objects"`
	Created   time.Time         `json:"created"`
	Updated   time.Time         `json:"updated"`
}

// Store is the plates directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates the plates directory under dir if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "plates"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: filepath.Join(dir, "plates")}, nil
}

// Dir is the folder of one plate (plate.json, slice.json, plate.gcode, plate.3mf).
func (s *Store) Dir(id string) string { return filepath.Join(s.dir, id) }

// Exists reports whether a plate with this id is stored.
func (s *Store) Exists(id string) bool {
	if !models.ValidID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.Dir(id), "plate.json"))
	return err == nil
}

// Get returns one plate.
func (s *Store) Get(id string) (Plate, error) {
	if !models.ValidID(id) {
		return Plate{}, ErrNotFound
	}
	var p Plate
	if err := fsutil.ReadJSON(filepath.Join(s.Dir(id), "plate.json"), &p); err != nil {
		if os.IsNotExist(err) {
			return Plate{}, ErrNotFound
		}
		return Plate{}, err
	}
	return p, nil
}

// List returns every plate, most recently changed first.
func (s *Store) List() ([]Plate, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := []Plate{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Save writes a plate.
func (s *Store) Save(p Plate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fsutil.WriteJSON(filepath.Join(s.Dir(p.ID), "plate.json"), p)
}

// Delete removes a plate and its files.
func (s *Store) Delete(id string) error {
	if !models.ValidID(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(filepath.Join(s.Dir(id), "plate.json")); err != nil {
		return ErrNotFound
	}
	return os.RemoveAll(s.Dir(id))
}

// Hash identifies everything that changes the G-code: the presets, overrides, object placement
// and the geometry of the models (through their checksums). A slice is stale when the plate's
// hash no longer matches the one it was sliced from.
func Hash(p Plate, store *models.Store, version string) (string, error) {
	type obj struct {
		Model    string     `json:"model"`
		X        float64    `json:"x"`
		Y        float64    `json:"y"`
		Rotation [3]float64 `json:"rotation"`
		Scale    float64    `json:"scale"`
	}
	shas := map[string]string{}
	objs := make([]obj, 0, len(p.Objects))
	for _, o := range p.Objects {
		objs = append(objs, obj{o.Model, o.X, o.Y, o.Rotation, o.Scale})
		if _, ok := shas[o.Model]; ok {
			continue
		}
		m, err := store.Get(o.Model)
		if err != nil {
			return "", err
		}
		shas[o.Model] = m.SHA256
	}
	b, err := json.Marshal(struct {
		Version   string            `json:"version"`
		Machine   string            `json:"machine"`
		Process   string            `json:"process"`
		Filament  string            `json:"filament"`
		Bed       string            `json:"bed"`
		Overrides map[string]string `json:"overrides"`
		Objects   []obj             `json:"objects"`
		Models    map[string]string `json:"models"`
	}{version, p.Machine, p.Process, p.Filament, p.Bed, p.Overrides, objs, shas})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// BedTemps maps each bed type to the filament setting that holds its bed temperature (its
// first-layer value is the same key with _initial_layer appended).
var BedTemps = map[string]string{
	"Cool Plate":         "cool_plate_temp",
	"Engineering Plate":  "eng_plate_temp",
	"High Temp Plate":    "hot_plate_temp",
	"Textured PEI Plate": "textured_plate_temp",
}

// ValidBed reports whether bed is a bed type a plate can use.
func ValidBed(bed string) bool {
	_, ok := BedTemps[bed]
	return ok
}

// Beds returns the bed types a plate can use, sorted.
func Beds() []string {
	out := make([]string, 0, len(BedTemps))
	for b := range BedTemps {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

// SingleBed is the bed type for a printer whose profile has support_multi_bed_types off (the Kobra 3):
// the desktop OrcaSlicer hides the bed choice for those and slices as High Temp Plate, so the
// filament's hot_plate_temp is the bed temperature. The CLI does not do this by itself (it falls back
// to Cool Plate), so the server sets it.
const SingleBed = "High Temp Plate"

// MultiBed reports whether a machine profile lets a plate choose its bed type.
func MultiBed(machine map[string]any) bool {
	return profiles.Str(machine, "support_multi_bed_types") == "1"
}

// EffectiveBed is the bed type a plate is sliced with: the plate's own choice only on printers that
// support several bed types, otherwise SingleBed.
func EffectiveBed(machine map[string]any, plateBed string) string {
	if MultiBed(machine) && ValidBed(plateBed) {
		return plateBed
	}
	return SingleBed
}
