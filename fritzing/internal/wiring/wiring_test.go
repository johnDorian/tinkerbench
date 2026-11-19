package wiring

import (
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

type fake map[string]*parts.FZP

func (f fake) FZP(id string) (*parts.FZP, string, error) {
	if x, ok := f[id]; ok {
		return x, "", nil
	}
	return nil, "", parts.ErrNotFound
}
func (f fake) SVG(string, string) ([]byte, error) { return nil, nil }

func conn(id, name, desc string) parts.FZPConnector {
	return parts.FZPConnector{ID: id, Name: name, Description: desc}
}

func lib() fake {
	d1 := &parts.FZP{ModuleID: "d1mini", Title: "Wemos D1 mini", Connectors: []parts.FZPConnector{
		conn("d1", "D1", ""), conn("g", "G", "Ground"), conn("v", "5V", "")}}
	q := &parts.FZP{ModuleID: "npn", Title: "NPN transistor", Connectors: []parts.FZPConnector{
		conn("connector0", "E", "Emitter"), conn("connector1", "B", "Base"), conn("connector2", "C", "Collector")}}
	r := &parts.FZP{ModuleID: "res", Title: "Resistor", Connectors: []parts.FZPConnector{
		conn("connector0", "", "pin 0"), conn("connector1", "1", "")}}
	for _, f := range []*parts.FZP{d1, q, r} {
		f.Views.Breadboard.Layers.Image = "x.svg"
		f.Views.Schematic.Layers.Image = "x.svg"
	}
	return fake{"d1mini": d1, "npn": q, "res": r}
}

func both(title string) map[string]sketch.Placing {
	return map[string]sketch.Placing{"breadboard": {}, "schematic": {}}
}

func summary() sketch.Summary {
	net := func(v string, pins ...string) sketch.Net { return sketch.Net{View: v, Pins: pins} }
	return sketch.Summary{
		Parts: []sketch.PartInfo{
			{Title: "D1MINI", ModuleID: "d1mini", Positions: both("")},
			{Title: "Q1", ModuleID: "npn", Positions: both("")},
			{Title: "R1", ModuleID: "res", Properties: map[string]string{"resistance": "1000", "package": "THT"}, Positions: both("")},
			{Title: "R2", ModuleID: "res", Properties: map[string]string{"resistance": "1000", "package": "THT"}, Positions: both("")},
		},
		Wires: []sketch.WireInfo{
			{Title: "Wire1", View: "breadboard", From: "D1MINI.d1", To: "R1.connector0", Color: "#cc1414"},
			{Title: "Wire2", View: "breadboard", From: "R1.connector1", To: "Q1.connector1", Color: "#418dd9"},
			{Title: "Wire3", View: "breadboard", From: "Q1.connector0", To: "D1MINI.g", Color: "#404040"},
		},
		Nets: []sketch.Net{
			net("breadboard", "D1MINI.d1", "R1.connector0"),
			net("breadboard", "Q1.connector0", "D1MINI.g"),
			net("breadboard", "Q1.connector1", "R1.connector1"),
			net("schematic", "D1MINI.d1", "R1.connector0"),
			net("schematic", "Q1.connector0", "D1MINI.g"),
			net("schematic", "Q1.connector1", "R1.connector1"),
		},
	}
}

func TestBuild(t *testing.T) {
	w := Build(Input{Project: "demo", Summary: summary(), Res: lib(), Notes: "Switches a lamp.", NotesVersion: 3, SketchVersion: 3})
	if len(w.Connections) != 3 {
		t.Fatalf("connections: %+v", w.Connections)
	}
	var gnd bool
	for _, c := range w.Connections {
		if c.Net == "GND" {
			gnd = true
			if c.From != "D1MINI.G (ground)" && c.To != "D1MINI.G (ground)" {
				t.Errorf("gnd row: %+v", c)
			}
			if c.Color != "black" {
				t.Errorf("colour: %+v", c)
			}
		}
	}
	if !gnd {
		t.Errorf("no GND net: %+v", w.Nets)
	}
	if len(w.Warnings) != 1 || w.Warnings[0].Kind != "unconnected" { // R2 is deliberately loose
		t.Errorf("warnings: %+v", w.Warnings)
	}
	if w.Stale {
		t.Error("stale")
	}
	if len(w.Parts) != 3 {
		t.Errorf("bom: %+v", w.Parts)
	}
	for _, p := range w.Parts {
		if p.Name == "Resistor" && (p.Count != 2 || p.Properties != "1 kΩ" || p.Package != "THT") {
			t.Errorf("resistor line: %+v", p)
		}
	}
	md := w.Markdown()
	for _, want := range []string{"Q1.B (base)", "| GND |", "| 2 | Resistor | 1 kΩ | THT | R1, R2 |", "Switches a lamp."} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if len(w.PinUsage) != 3 {
		t.Errorf("pin usage: %+v", w.PinUsage) // R2 is unused and not listed
	}
}

func TestSchematicOnly(t *testing.T) {
	s := summary()
	s.Wires = s.Wires[:2] // Wire3 gone, and with it the breadboard GND net
	s.Nets = append(s.Nets[:1], s.Nets[2:]...)
	w := Build(Input{Summary: s, Res: lib()})
	var n int
	for _, x := range w.Warnings {
		if x.Kind == "view-mismatch" {
			n++
			if !strings.Contains(x.Text, "GND") || !strings.Contains(x.Text, "schematic") {
				t.Errorf("text: %s", x.Text)
			}
		}
	}
	if n != 1 {
		t.Errorf("want 1 mismatch, got %d: %+v", n, w.Warnings)
	}
}

func TestLooseEnds(t *testing.T) {
	s := summary()
	s.Wires = append(s.Wires, sketch.WireInfo{Title: "Wire9", View: "breadboard", From: "Q1.connector2"})
	w := Build(Input{Summary: s, Res: lib()})
	var open, unconnected bool
	for _, x := range w.Warnings {
		open = open || (x.Kind == "open-wire" && strings.Contains(x.Text, "Wire9"))
		unconnected = unconnected || (x.Kind == "unconnected" && strings.Contains(x.Text, "R2"))
	}
	if !open || !unconnected {
		t.Errorf("warnings: %+v", w.Warnings)
	}
}

func TestEmpty(t *testing.T) {
	w := Build(Input{Project: "x"})
	if len(w.Parts) != 0 || len(w.Warnings) != 0 {
		t.Errorf("%+v", w)
	}
	if md := w.Markdown(); !strings.Contains(md, "no parts") {
		t.Errorf("md: %s", md)
	}
	if b, err := jsonOf(w); err != nil || strings.Contains(b, "null") {
		t.Errorf("json has null: %s %v", b, err)
	}
}

func TestStale(t *testing.T) {
	stored := project.StampNotes("## Purpose\nLamp.", 3)
	body, v := project.ParseNotes(stored)
	if v != 3 || body != "## Purpose\nLamp." {
		t.Fatalf("parse: %q %d", body, v)
	}
	if w := Build(Input{Notes: body, NotesVersion: v, SketchVersion: 5}); !w.Stale || !strings.Contains(w.Markdown(), "v3") {
		t.Errorf("not stale: %+v", w)
	}
	if w := Build(Input{SketchVersion: 5}); w.Stale {
		t.Error("empty notes are never stale")
	}
	if project.StampNotes("  ", 3) != "" {
		t.Error("empty notes stamped")
	}
}

func TestUnits(t *testing.T) {
	for in, want := range map[string]string{"1000": "1 kΩ", "220Ω": "220 Ω", "4.7k": "4.7 kΩ", "2200000": "2.2 MΩ", "weird": "weird"} {
		if got := FormatResistance(in); got != want {
			t.Errorf("R %q = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"100nF": "100 nF", "0.1uF": "100 nF", "1000000pF": "1 µF", "22": "22"} {
		if got := FormatCapacitance(in); got != want {
			t.Errorf("C %q = %q, want %q", in, got, want)
		}
	}
}

func TestColorName(t *testing.T) {
	for in, want := range map[string]string{"#d0021b": "red", "#f5a623": "orange", "#000000": "black", "#418dd9": "blue", "#7ed321": "green", "#ffffff": "white", "#888888": "grey"} {
		if got := colorName(in); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}

func TestLayoutWarnings(t *testing.T) {
	rep := sketch.LayoutReport{View: "breadboard", Wires: 3, WireLength: 120, Crossings: 1, Issues: []sketch.LayoutIssue{
		{Level: sketch.LevelError, Kind: "pin-contact", Text: "R1.1 overlaps D1MINI.D2 (nets differ)"},
		{Level: sketch.LevelInfo, Kind: "crossing", Text: "harmless"},
	}}
	w := Build(Input{Summary: summary(), Res: lib(), Layout: &rep})
	if w.Layout == nil || w.Layout.Errors != 1 || w.Layout.Crossings != 1 {
		t.Fatalf("layout stats: %+v", w.Layout)
	}
	md := w.Markdown()
	if !strings.Contains(md, "**ERROR:** R1.1 overlaps") || strings.Contains(md, "harmless") || !strings.Contains(md, "120 mm") {
		t.Errorf("markdown:\n%s", md)
	}
}
