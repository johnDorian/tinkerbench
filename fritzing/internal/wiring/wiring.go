// Package wiring turns a sketch summary into a readable description of the circuit: parts list,
// connections with human pin names, nets, per-part pin usage and warnings. It is derived on demand
// and never stored; the written notes that go with it are passed in.
package wiring

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

// Input is everything Build needs.
type Input struct {
	Project string
	View    string // view for the connections table; default breadboard
	Summary sketch.Summary
	Res     sketch.Resolver // optional; without it pins keep their ids and parts their module ids

	Layout *sketch.LayoutReport // geometry checks of View, optional

	Notes         string // body of notes.md without the version stamp
	NotesVersion  int    // sketch version the notes were written against, 0 if unknown
	SketchVersion int
}

// Wiring is the description.
type Wiring struct {
	Project       string       `json:"project"`
	View          string       `json:"view"`
	Parts         []BOMLine    `json:"parts"`
	Nets          []Net        `json:"nets"`
	Connections   []Connection `json:"connections"`
	PinUsage      []PartPins   `json:"pin_usage"`
	Warnings      []Warning    `json:"warnings"`
	Notes         string       `json:"notes"`
	NotesVersion  int          `json:"notes_version"`
	SketchVersion int          `json:"sketch_version"`
	Stale         bool         `json:"stale"`
	// Layout is the tidiness of View: wire count, total length and crossings.
	Layout *LayoutStats `json:"layout,omitempty"`
}

// LayoutStats are the numbers that tell whether a layout got better or worse after a move.
type LayoutStats struct {
	Wires     int     `json:"wires"`
	LengthMM  float64 `json:"wire_length_mm"`
	Crossings int     `json:"crossings"`
	Errors    int     `json:"errors"`
	Warnings  int     `json:"warnings"`
	Bends     int     `json:"bends"`
	Tidiness  float64 `json:"tidiness"`
}

// BOMLine is one type of part with all its instances.
type BOMLine struct {
	Name       string   `json:"name"` // library name, e.g. Resistor
	ModuleID   string   `json:"module_id"`
	Titles     []string `json:"titles"` // R1, R2
	Count      int      `json:"count"`
	Properties string   `json:"properties,omitempty"` // "1 kΩ, THT"
	Package    string   `json:"package,omitempty"`
}

// NetPin is a pin with its human label.
type NetPin struct {
	Pin   string `json:"pin"`   // "Q1.connector1"
	Label string `json:"label"` // "Q1.B (base)"
}

// Net is a group of joined pins in one view.
type Net struct {
	Name  string   `json:"name"`
	View  string   `json:"view"`
	Pins  []NetPin `json:"pins"`
	Wires []string `json:"wires,omitempty"` // wire titles in the net's view, only for display
}

// Connection is one row of the connections table.
type Connection struct {
	Net   string `json:"net"`
	From  string `json:"from"`
	To    string `json:"to"`
	Color string `json:"color,omitempty"` // wire colour name, breadboard only
}

// PartPins lists the connected pins of a part and where they go.
type PartPins struct {
	Part string    `json:"part"`
	Pins []PinLink `json:"pins"`
}

// PinLink is one connected pin.
type PinLink struct {
	Pin  string   `json:"pin"` // label without part, "B (base)"
	Net  string   `json:"net"`
	Goes []string `json:"goes_to"`
}

// Warning is something to check. Level is "warn" or "info".
type Warning struct {
	Level string `json:"level"`
	Kind  string `json:"kind"` // view-mismatch, open-wire, unconnected, missing-in-view
	Text  string `json:"text"`
}

// vnet is a net of one view before naming is turned into output.
type vnet struct {
	pins []string
	name string
}

// pinInfo is the human naming of one connector.
type pinInfo struct{ label, role string }

type namer struct {
	res   sketch.Resolver
	fzp   map[string]*parts.FZP
	pins  map[string]map[string]pinInfo // module -> connector id -> info
	title map[string]string             // part title -> module
}

func (n *namer) load(module string) *parts.FZP {
	if f, ok := n.fzp[module]; ok {
		return f
	}
	var f *parts.FZP
	if n.res != nil {
		if x, _, err := n.res.FZP(module); err == nil {
			f = x
		}
	}
	n.fzp[module] = f
	return f
}

var numeric = regexp.MustCompile(`^[0-9]*$`)

func (n *namer) pinTable(module string) map[string]pinInfo {
	if t, ok := n.pins[module]; ok {
		return t
	}
	t := map[string]pinInfo{}
	n.pins[module] = t
	f := n.load(module)
	if f == nil {
		return t
	}
	count := map[string]int{}
	for _, c := range f.Connectors {
		count[strings.ToLower(c.Name)]++
	}
	for _, c := range f.Connectors {
		name, desc := strings.TrimSpace(c.Name), strings.TrimSpace(c.Description)
		pi := pinInfo{label: name}
		switch {
		case numeric.MatchString(name) && desc != "":
			pi.label = desc
		case desc != "" && !strings.EqualFold(desc, name) && len(name) <= 4:
			// short names (B, E, VCC) get their role: "B (base)"
			pi.label = name + " (" + strings.ToLower(desc[:1]) + desc[1:] + ")"
		}
		if pi.label == "" {
			pi.label = c.ID
		} else if count[strings.ToLower(c.Name)] > 1 || (numeric.MatchString(name) && desc == "") {
			pi.label += " [" + c.ID + "]"
		}
		t[c.ID] = pi
	}
	return t
}

// label turns "Q1.connector1" into "Q1.B (base)".
func (n *namer) label(pin string) string {
	part, id, _ := strings.Cut(pin, ".")
	if m, ok := n.title[part]; ok {
		if pi, ok := n.pinTable(m)[id]; ok {
			return part + "." + pi.label
		}
	}
	return pin
}

// short is the pin's bare name (for net naming): "GND", "5V".
func (n *namer) short(pin string) string {
	part, id, _ := strings.Cut(pin, ".")
	if m, ok := n.title[part]; ok {
		if f := n.load(m); f != nil {
			for _, c := range f.Connectors {
				if c.ID == id {
					return strings.TrimSpace(c.Name)
				}
			}
		}
	}
	return ""
}

func (n *namer) isBreadboard(module string) bool {
	if strings.Contains(strings.ToLower(module), "breadboard") {
		return true
	}
	if f := n.load(module); f != nil {
		return strings.Contains(strings.ToLower(f.Title), "breadboard")
	}
	return false
}

// netName names a net after a rail pin it contains, or returns "".
func netName(shorts []string) string {
	for _, s := range shorts {
		u := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
		switch {
		case u == "G" || u == "GND" || u == "GROUND" || u == "VSS" || strings.HasPrefix(u, "GND"):
			return "GND"
		}
	}
	for _, s := range shorts {
		u := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
		switch u {
		case "5V", "VBUS", "VIN", "+5V", "5V0":
			return "5 V"
		case "3V3", "3.3V", "+3V3", "3V":
			return "3.3 V"
		}
	}
	return ""
}

// Build assembles the wiring description.
func Build(in Input) Wiring {
	view := in.View
	if !parts.ValidView(view) {
		view = parts.Breadboard
	}
	w := Wiring{
		Project: in.Project, View: view, Parts: []BOMLine{}, Nets: []Net{}, Connections: []Connection{},
		PinUsage: []PartPins{}, Warnings: []Warning{},
		Notes: in.Notes, NotesVersion: in.NotesVersion, SketchVersion: in.SketchVersion,
	}
	w.Stale = strings.TrimSpace(in.Notes) != "" && in.NotesVersion != in.SketchVersion

	nm := &namer{res: in.Res, fzp: map[string]*parts.FZP{}, pins: map[string]map[string]pinInfo{}, title: map[string]string{}}
	for _, p := range in.Summary.Parts {
		nm.title[p.Title] = p.ModuleID
	}
	skip := func(pin string) bool { // breadboard holes are not interesting
		part, _, _ := strings.Cut(pin, ".")
		return nm.isBreadboard(nm.title[part])
	}

	w.Parts = bom(in.Summary.Parts, nm)

	// wires by view, for colours and open ends
	wires := map[string][]sketch.WireInfo{}
	for _, wi := range in.Summary.Wires {
		wires[wi.View] = append(wires[wi.View], wi)
	}

	// nets of every view, breadboard holes removed
	byView := map[string][]vnet{}
	for _, v := range parts.Views {
		var list []vnet
		for _, sn := range in.Summary.Nets {
			if sn.View != v {
				continue
			}
			var pins, shorts []string
			for _, p := range sn.Pins {
				if skip(p) {
					continue
				}
				pins = append(pins, p)
				shorts = append(shorts, nm.short(p))
			}
			if len(pins) < 2 {
				continue
			}
			list = append(list, vnet{pins: pins, name: netName(shorts)})
		}
		// number the unnamed ones
		n := 0
		seen := map[string]int{}
		for i := range list {
			if list[i].name == "" {
				n++
				list[i].name = "Net " + strconv.Itoa(n)
			} else if seen[list[i].name]++; seen[list[i].name] > 1 {
				list[i].name += " (" + strconv.Itoa(seen[list[i].name]) + ")"
			}
		}
		byView[v] = list
	}

	// the chosen view: nets, connections, pin usage
	pinNet := map[string]string{}
	pinsOf := map[string][]string{}
	for _, n := range byView[view] {
		wn := Net{Name: n.name, View: view}
		for _, p := range n.pins {
			wn.Pins = append(wn.Pins, NetPin{Pin: p, Label: nm.label(p)})
			pinNet[p] = n.name
		}
		// wires touching the net, with their colour
		colors := map[string]bool{}
		for _, wi := range wires[view] {
			if inNet(n.pins, wi.From) || inNet(n.pins, wi.To) {
				wn.Wires = append(wn.Wires, wi.Title)
				if view == parts.Breadboard && wi.Color != "" {
					colors[colorName(wi.Color)] = true
				}
			}
		}
		w.Nets = append(w.Nets, wn)
		col := joinKeys(colors)
		for _, p := range n.pins[1:] {
			w.Connections = append(w.Connections, Connection{Net: n.name, From: nm.label(n.pins[0]), To: nm.label(p), Color: col})
		}
		for _, p := range n.pins {
			for _, q := range n.pins {
				if p != q {
					pinsOf[p] = append(pinsOf[p], q)
				}
			}
		}
	}
	for _, p := range in.Summary.Parts {
		if skip(p.Title + ".") {
			continue
		}
		pp := PartPins{Part: p.Title}
		for _, pin := range sortedPins(pinsOf, p.Title) {
			_, id, _ := strings.Cut(pin, ".")
			l := nm.label(pin)
			l = strings.TrimPrefix(l, p.Title+".")
			link := PinLink{Pin: l, Net: pinNet[pin]}
			for _, q := range pinsOf[pin] {
				if qp, _, _ := strings.Cut(q, "."); qp != p.Title {
					link.Goes = append(link.Goes, nm.label(q))
				}
			}
			_ = id
			pp.Pins = append(pp.Pins, link)
		}
		if len(pp.Pins) > 0 {
			w.PinUsage = append(w.PinUsage, pp)
		}
	}

	w.Warnings = warnings(in, nm, byView, wires, skip)
	for _, c := range Circuit(in, view) {
		if c.Level != "info" { // the info findings are for check_circuit
			c.Kind = "circuit-" + c.Kind
			w.Warnings = append(w.Warnings, c)
		}
	}
	if l := in.Layout; l != nil && l.View == view {
		w.Layout = &LayoutStats{Wires: l.Wires, LengthMM: l.WireLength, Crossings: l.Crossings,
			Errors: l.Count(sketch.LevelError), Warnings: l.Count(sketch.LevelWarn), Bends: l.Bends, Tidiness: l.Tidiness}
		for _, i := range l.Issues {
			if i.Level == sketch.LevelInfo {
				continue
			}
			w.Warnings = append(w.Warnings, Warning{Level: i.Level, Kind: "layout-" + i.Kind, Text: i.Text + " (" + view + ")"})
		}
	}
	return w
}

func inNet(pins []string, end string) bool {
	if end == "" {
		return false
	}
	for _, p := range pins {
		if p == end {
			return true
		}
	}
	return false
}

func sortedPins(pinsOf map[string][]string, title string) []string {
	var out []string
	for p := range pinsOf {
		if t, _, _ := strings.Cut(p, "."); t == title {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func joinKeys(m map[string]bool) string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return strings.Join(k, ", ")
}

var wireColors = map[string]string{
	"#418dd9": "blue", "#cc1414": "red", "#404040": "black", "#ffe24d": "yellow", "#47cc79": "green",
	"#999999": "grey", "#ffffff": "white", "#ff7033": "orange", "#8c3b00": "brown", "#ab58a2": "purple",
	"#33ffc4": "cyan", "#ff7aa8": "pink",
}

// colorName gives a hex colour a plain name: exact Fritzing palette entries first, then by hue.
func colorName(hex string) string {
	if n, ok := wireColors[strings.ToLower(hex)]; ok {
		return n
	}
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return hex
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return hex
	}
	r, g, b := float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l, d := (mx+mn)/2, mx-mn
	switch {
	case l < 0.18:
		return "black"
	case l > 0.92:
		return "white"
	case d < 0.12:
		return "grey"
	}
	var hue float64
	switch mx {
	case r:
		hue = math.Mod((g-b)/d, 6)
	case g:
		hue = (b-r)/d + 2
	default:
		hue = (r-g)/d + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	switch {
	case hue < 15 || hue >= 340:
		return "red"
	case hue < 45:
		if l < 0.35 {
			return "brown"
		}
		return "orange"
	case hue < 70:
		return "yellow"
	case hue < 170:
		return "green"
	case hue < 200:
		return "cyan"
	case hue < 260:
		return "blue"
	case hue < 300:
		return "purple"
	}
	return "pink"
}
