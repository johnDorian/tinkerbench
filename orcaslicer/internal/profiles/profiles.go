// Package profiles reads the OrcaSlicer system presets (resources/profiles/<vendor>/<kind>/*.json),
// flattens their `inherits` chains and lists the presets that work with a given machine.
package profiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/fsutil"
)

// Kind is one of the three preset kinds.
type Kind string

const (
	Machine  Kind = "machine"
	Process  Kind = "process"
	Filament Kind = "filament"
)

// ErrNotFound is returned for an unknown preset.
var ErrNotFound = errors.New("preset not found")

// ParseKind checks a preset kind from a URL or a tool argument.
func ParseKind(s string) (Kind, error) {
	switch k := Kind(s); k {
	case Machine, Process, Filament:
		return k, nil
	}
	return "", fmt.Errorf("kind must be machine, process or filament, not %q", s)
}

type entry struct {
	kind           Kind
	name           string
	vendor         string
	path           string
	instantiable   bool // a preset the user can pick, not an abstract parent
	compat         []string
	compatCond     string
	printerModel   string
	layerHeight    float64
	filamentType   string
	filamentVendor string
	inherits       string
}

// Library is the set of system presets under one resources/profiles directory. It is indexed on
// first use, because reading a few thousand JSON files takes a moment on the server.
type Library struct {
	root    string
	once    sync.Once
	loadErr error
	all     map[Kind][]*entry
	byName  map[Kind]map[string][]*entry
}

// Open returns a library over root (the resources/profiles directory of OrcaSlicer).
func Open(root string) *Library { return &Library{root: root} }

// Root returns the directory the presets are read from.
func (l *Library) Root() string { return l.root }

func (l *Library) load() error {
	l.once.Do(func() {
		l.all = map[Kind][]*entry{}
		l.byName = map[Kind]map[string][]*entry{Machine: {}, Process: {}, Filament: {}}
		vendors, err := os.ReadDir(l.root)
		if err != nil {
			l.loadErr = fmt.Errorf("profiles: %w", err)
			return
		}
		for _, v := range vendors {
			if !v.IsDir() {
				continue
			}
			for _, kind := range []Kind{Machine, Process, Filament} {
				dir := filepath.Join(l.root, v.Name(), string(kind))
				files, err := os.ReadDir(dir)
				if err != nil {
					continue // this vendor has no presets of this kind
				}
				for _, f := range files {
					if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
						continue
					}
					e := readEntry(kind, v.Name(), filepath.Join(dir, f.Name()))
					if e == nil {
						continue
					}
					l.all[kind] = append(l.all[kind], e)
					l.byName[kind][e.name] = append(l.byName[kind][e.name], e)
				}
			}
		}
	})
	return l.loadErr
}

func readEntry(kind Kind, vendor, path string) *entry {
	var m map[string]any
	if err := fsutil.ReadJSON(path, &m); err != nil {
		return nil
	}
	name := Str(m, "name")
	if name == "" {
		return nil
	}
	lh, _ := parseFloat(Str(m, "layer_height"))
	return &entry{
		kind:           kind,
		name:           name,
		vendor:         vendor,
		path:           path,
		instantiable:   instantiable(m),
		compat:         Strings(m, "compatible_printers"),
		compatCond:     Str(m, "compatible_printers_condition"),
		printerModel:   Str(m, "printer_model"),
		layerHeight:    lh,
		filamentType:   Str(m, "filament_type"),
		filamentVendor: Str(m, "filament_vendor"),
		inherits:       Str(m, "inherits"),
	}
}

// find returns the preset called name, preferring the given vendor and then an instantiable one.
func (l *Library) find(kind Kind, name, vendor string) *entry {
	var best *entry
	score := func(e *entry) int {
		s := 0
		if e.vendor == vendor {
			s += 2
		}
		if e.instantiable {
			s++
		}
		return s
	}
	for _, e := range l.byName[kind][name] {
		if best == nil || score(e) > score(best) {
			best = e
		}
	}
	return best
}

// Flatten returns the settings of a preset with its inherits chain merged in: the child's keys
// win over its parents'. The keys inherits, from and instantiation are dropped.
func (l *Library) Flatten(kind Kind, name string) (map[string]any, error) {
	if err := l.load(); err != nil {
		return nil, err
	}
	e := l.find(kind, name, "")
	if e == nil {
		return nil, fmt.Errorf("%s %q: %w", kind, name, ErrNotFound)
	}
	return l.flatten(e, 0)
}

func (l *Library) flatten(e *entry, depth int) (map[string]any, error) {
	if depth > 8 {
		return nil, fmt.Errorf("%s: inherits chain too deep", e.name)
	}
	var m map[string]any
	if err := fsutil.ReadJSON(e.path, &m); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if e.inherits != "" {
		parent := l.find(e.kind, e.inherits, e.vendor)
		if parent == nil {
			return nil, fmt.Errorf("%s inherits %q: %w", e.name, e.inherits, ErrNotFound)
		}
		pm, err := l.flatten(parent, depth+1)
		if err != nil {
			return nil, err
		}
		out = pm
	}
	for k, v := range m {
		switch k {
		case "inherits", "from", "instantiation":
			continue
		}
		out[k] = v
	}
	return out, nil
}

// MachineInfo, ProcessInfo and FilamentInfo are the list entries the UI shows.
type MachineInfo struct {
	Name string `json:"name"`
}

type ProcessInfo struct {
	Name        string  `json:"name"`
	LayerHeight float64 `json:"layer_height"`
}

type FilamentInfo struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Vendor string `json:"vendor"`
}

// Listing is what a machine can print with.
type Listing struct {
	Machines  []MachineInfo  `json:"machines"`
	Processes []ProcessInfo  `json:"processes"`
	Filaments []FilamentInfo `json:"filaments"`
}

// List returns the machines of the machine's vendor and the processes and filaments compatible
// with it. A preset is compatible when compatible_printers names the machine; without a list,
// when its compatible_printers_condition mentions the machine's model; without either, when it
// comes from the same vendor.
func (l *Library) List(machine string) (Listing, error) {
	if err := l.load(); err != nil {
		return Listing{}, err
	}
	m := l.find(Machine, machine, "")
	if m == nil {
		return Listing{}, fmt.Errorf("machine %q: %w", machine, ErrNotFound)
	}
	var out Listing
	seen := map[string]bool{}
	machines := []string{}
	for _, e := range l.all[Machine] {
		if e.vendor == m.vendor && e.instantiable && !seen[e.name] {
			seen[e.name] = true
			machines = append(machines, e.name)
		}
	}
	sort.Strings(machines)
	for _, n := range machines {
		out.Machines = append(out.Machines, MachineInfo{Name: n})
	}

	seen = map[string]bool{}
	for _, e := range sortedEntries(l.all[Process]) {
		if compatible(e, m) && !seen[e.name] {
			seen[e.name] = true
			out.Processes = append(out.Processes, ProcessInfo{Name: e.name, LayerHeight: e.layerHeight})
		}
	}
	seen = map[string]bool{}
	for _, e := range sortedEntries(l.all[Filament]) {
		if compatible(e, m) && !seen[e.name] {
			seen[e.name] = true
			// type and vendor may be inherited, so read them from the flattened preset
			fl, err := l.flatten(e, 0)
			if err != nil {
				continue
			}
			out.Filaments = append(out.Filaments, FilamentInfo{Name: e.name, Type: Str(fl, "filament_type"), Vendor: Str(fl, "filament_vendor")})
		}
	}
	return out, nil
}

func sortedEntries(es []*entry) []*entry {
	c := slices.Clone(es)
	sort.SliceStable(c, func(i, j int) bool { return c[i].name < c[j].name })
	return c
}

func compatible(e *entry, m *entry) bool {
	if !e.instantiable {
		return false
	}
	switch {
	case len(e.compat) > 0:
		return slices.Contains(e.compat, m.name)
	case e.compatCond != "":
		return m.printerModel != "" && strings.Contains(e.compatCond, m.printerModel)
	}
	return e.vendor == m.vendor
}

// CheckNames reports an error unless machine, process and filament are presets that exist.
func (l *Library) CheckNames(machine, process, filament string) error {
	if err := l.load(); err != nil {
		return err
	}
	for _, c := range []struct {
		kind Kind
		name string
	}{{Machine, machine}, {Process, process}, {Filament, filament}} {
		if l.find(c.kind, c.name, "") == nil {
			return fmt.Errorf("%s %q: %w", c.kind, c.name, ErrNotFound)
		}
	}
	return nil
}

// Bed is the printable area of a machine, with its lower-left corner at the origin.
type Bed struct {
	Width     float64      `json:"width"`
	Depth     float64      `json:"depth"`
	MaxHeight float64      `json:"max_height"`
	Shape     [][2]float64 `json:"shape"`
}

// Bed returns the printable area and height of a machine preset.
func (l *Library) Bed(machine string) (Bed, error) {
	m, err := l.Flatten(Machine, machine)
	if err != nil {
		return Bed{}, err
	}
	pts := [][2]float64{}
	for _, s := range Strings(m, "printable_area") {
		xs, ys, ok := strings.Cut(s, "x")
		x, err1 := strconv.ParseFloat(xs, 64)
		y, err2 := strconv.ParseFloat(ys, 64)
		if !ok || err1 != nil || err2 != nil {
			return Bed{}, fmt.Errorf("machine %q: printable_area %q is not XxY", machine, s)
		}
		pts = append(pts, [2]float64{x, y})
	}
	if len(pts) < 3 {
		return Bed{}, fmt.Errorf("machine %q has no printable_area", machine)
	}
	minX, minY := pts[0][0], pts[0][1]
	maxX, maxY := minX, minY
	for _, p := range pts {
		minX, maxX = min(minX, p[0]), max(maxX, p[0])
		minY, maxY = min(minY, p[1]), max(maxY, p[1])
	}
	shape := make([][2]float64, len(pts))
	for i, p := range pts {
		shape[i] = [2]float64{p[0] - minX, p[1] - minY}
	}
	h, err := parseFloat(Str(m, "printable_height"))
	if err != nil {
		return Bed{}, fmt.Errorf("machine %q: printable_height: %w", machine, err)
	}
	return Bed{Width: maxX - minX, Depth: maxY - minY, MaxHeight: h, Shape: shape}, nil
}

// DefaultVendor returns the vendor folder of a machine preset.
func (l *Library) DefaultVendor(machine string) string {
	if l.load() != nil {
		return ""
	}
	if e := l.find(Machine, machine, ""); e != nil {
		return e.vendor
	}
	return ""
}

// PrinterModel returns the printer_model of a machine (used by compatibility checks).
func (l *Library) PrinterModel(machine string) string {
	if l.load() != nil {
		return ""
	}
	if e := l.find(Machine, machine, ""); e != nil {
		return e.printerModel
	}
	return ""
}

// Str returns the value of key as one string: a scalar as it is, an array by its first element.
func Str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
			return fmt.Sprint(v[0])
		}
	case bool:
		if v {
			return "1"
		}
		return "0"
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%g", v), ".0")
	case nil:
	default:
		return fmt.Sprint(v)
	}
	return ""
}

// Strings returns the values of key as a list (a scalar becomes a list of one).
func Strings(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

// Set writes a setting value, keeping the shape the preset uses: array-valued settings (filament
// temperatures, for example) stay arrays.
func Set(m map[string]any, key, value string) {
	if _, ok := m[key].([]any); ok {
		m[key] = []any{value}
		return
	}
	m[key] = value
}

// Has reports whether a preset defines the key.
func Has(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func instantiable(m map[string]any) bool {
	switch v := m["instantiation"].(type) {
	case string:
		return v == "true"
	case bool:
		return v
	}
	return false
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
