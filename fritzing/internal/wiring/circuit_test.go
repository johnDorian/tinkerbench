package wiring

import (
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

func circuitLib() fake {
	l := lib()
	led := &parts.FZP{ModuleID: "led", Title: "Red LED - 5mm", Connectors: []parts.FZPConnector{
		conn("connector0", "cathode", "cathode pin"), conn("connector1", "anode", "anode pin")}}
	usb := &parts.FZP{ModuleID: "usb", Title: "USB", Connectors: []parts.FZPConnector{
		conn("connector4", "GND", ""), conn("connector5", "VBUS", "")}}
	mos := &parts.FZP{ModuleID: "mosfet", Title: "N-channel MOSFET", Connectors: []parts.FZPConnector{
		conn("connector0", "G", "Gate"), conn("connector1", "D", "Drain"), conn("connector2", "S", "Source")}}
	d3 := &parts.FZP{ModuleID: "wemos", Title: "WeMos D1 Mini", Connectors: []parts.FZPConnector{
		conn("d3", "D3", ""), conn("g", "G", ""), conn("v", "5V", ""), conn("t", "3V3", "")}}
	for _, f := range []*parts.FZP{led, usb, mos, d3} {
		f.Views.Breadboard.Layers.Image = "x.svg"
		l[f.ModuleID] = f
	}
	return l
}

func check(t *testing.T, ps []sketch.PartInfo, nets ...[]string) []Warning {
	t.Helper()
	var ns []sketch.Net
	for _, n := range nets {
		ns = append(ns, sketch.Net{View: "breadboard", Pins: n})
	}
	return Circuit(Input{Summary: sketch.Summary{Parts: ps, Nets: ns}, Res: circuitLib()}, "breadboard")
}

func kindsOf(ws []Warning, level string) string {
	var k []string
	for _, w := range ws {
		if w.Level == level {
			k = append(k, w.Kind)
		}
	}
	return strings.Join(k, ",")
}

func TestCircuitPowerShort(t *testing.T) {
	ps := []sketch.PartInfo{{Title: "B", ModuleID: "wemos"}, {Title: "U", ModuleID: "usb"}}
	// 5V joined to ground through a wire: a short
	ws := check(t, ps, []string{"B.v", "B.g"})
	if kindsOf(ws, "error") != "power-short" {
		t.Fatalf("want power-short: %+v", ws)
	}
	// 5V and 3V3 on one net
	ws = check(t, ps, []string{"B.v", "B.t"})
	if kindsOf(ws, "error") != "rail-short" {
		t.Fatalf("want rail-short: %+v", ws)
	}
	// 5V to VBUS and G to GND is fine
	ws = check(t, ps, []string{"B.v", "U.connector5"}, []string{"B.g", "U.connector4"})
	if kindsOf(ws, "error") != "" || kindsOf(ws, "warn") != "" {
		t.Fatalf("a correct hookup: %+v", ws)
	}
}

func TestCircuitMosfetGateIsNotGround(t *testing.T) {
	ps := []sketch.PartInfo{{Title: "B", ModuleID: "wemos"}, {Title: "M", ModuleID: "mosfet"}}
	// the MOSFET's G is its gate: joining it to a supply pin is not a ground short
	ws := check(t, ps, []string{"B.v", "M.connector0"}, []string{"B.g", "M.connector2"})
	if kindsOf(ws, "error") != "" {
		t.Fatalf("gate mistaken for ground: %+v", ws)
	}
}

func TestCircuitNoGroundAndBootPin(t *testing.T) {
	ps := []sketch.PartInfo{{Title: "B", ModuleID: "wemos"}, {Title: "U", ModuleID: "usb"}}
	// the board only has a supply and a boot pin wired: its ground is missing
	ws := check(t, ps, []string{"B.v", "U.connector5"}, []string{"B.d3", "U.connector4"})
	if !strings.Contains(kindsOf(ws, "warn"), "no-ground") {
		t.Fatalf("want no-ground: %+v", ws)
	}
	if !strings.Contains(kindsOf(ws, "info"), "boot-pin") {
		t.Fatalf("want boot-pin info: %+v", ws)
	}
}

func TestCircuitLEDWithoutResistor(t *testing.T) {
	ps := []sketch.PartInfo{{Title: "B", ModuleID: "wemos"}, {Title: "L", ModuleID: "led"}, {Title: "R1", ModuleID: "res"}}
	direct := check(t, ps, []string{"B.v", "L.connector1"}, []string{"B.g", "L.connector0"})
	if !strings.Contains(kindsOf(direct, "warn"), "led-no-resistor") {
		t.Fatalf("want led-no-resistor: %+v", direct)
	}
	// a resistor between supply and the anode puts the anode on its own net
	withR := check(t, ps, []string{"B.v", "R1.connector0"}, []string{"R1.connector1", "L.connector1"}, []string{"B.g", "L.connector0"})
	if strings.Contains(kindsOf(withR, "warn"), "led-no-resistor") {
		t.Fatalf("resistor present: %+v", withR)
	}
}

func TestBuildIncludesCircuitErrors(t *testing.T) {
	s := sketch.Summary{
		Parts: []sketch.PartInfo{{Title: "B", ModuleID: "wemos", Positions: both("")}},
		Nets:  []sketch.Net{{View: "breadboard", Pins: []string{"B.v", "B.g"}}},
	}
	w := Build(Input{Summary: s, Res: circuitLib()})
	var got bool
	for _, x := range w.Warnings {
		got = got || (x.Level == "error" && x.Kind == "circuit-power-short")
	}
	if !got {
		t.Fatalf("get_wiring should list the short: %+v", w.Warnings)
	}
}
