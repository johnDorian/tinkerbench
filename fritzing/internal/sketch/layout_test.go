package sketch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

// fakeLib has one part, "chip": a 40x40 px box with four 6x6 px pins (connector0..3) in the corners.
type fakeLib struct{}

const chipFZP = `<module moduleId="chip"><title>Chip</title><label>U</label>
<views>
<breadboardView><layers image="chip.svg"><layer layerId="breadboard"/></layers></breadboardView>
<schematicView><layers image="chip.svg"><layer layerId="schematic"/></layers></schematicView>
</views>
<connectors>%s</connectors></module>`

func chipConnectors() string {
	var b strings.Builder
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, `<connector id="connector%d" name="P%d"><views>`, i, i)
		for _, v := range []string{"breadboardView", "schematicView"} {
			fmt.Fprintf(&b, `<%s><p layer="%s" svgId="pin%d"/></%s>`, v, strings.TrimSuffix(v, "View"), i, v)
		}
		b.WriteString(`</views></connector>`)
	}
	return b.String()
}

const chipSVG = `<svg width="40" height="40" viewBox="0 0 40 40">
<rect id="body" x="0" y="0" width="40" height="40"/>
<rect id="pin0" x="0" y="0" width="6" height="6"/><rect id="pin1" x="34" y="0" width="6" height="6"/>
<rect id="pin2" x="0" y="34" width="6" height="6"/><rect id="pin3" x="34" y="34" width="6" height="6"/></svg>`

func (fakeLib) FZP(id string) (*parts.FZP, string, error) {
	if id != "chip" {
		return nil, "", parts.ErrNotFound
	}
	f, err := parts.ParseFZP([]byte(fmt.Sprintf(chipFZP, chipConnectors())))
	return f, "chip.fzp", err
}
func (fakeLib) SVG(id, view string) ([]byte, error) { return []byte(chipSVG), nil }

func place(t *testing.T, s *Sketch, title string, x, y float64) {
	t.Helper()
	if _, err := s.AddPart(fakeLib{}, "chip", AddOptions{Title: title, Position: map[string]XY{"schematic": {X: x, Y: y}}}); err != nil {
		t.Fatal(err)
	}
}

func wire(t *testing.T, s *Sketch, a, ac, b, bc string) {
	t.Helper()
	if _, err := s.Connect(fakeLib{}, "schematic", Endpoint{a, ac}, Endpoint{b, bc}, ConnectOptions{}); err != nil {
		t.Fatal(err)
	}
}

func kinds(r LayoutReport, level string) string {
	var k []string
	for _, i := range r.Issues {
		if i.Level == level {
			k = append(k, i.Kind)
		}
	}
	return strings.Join(k, ",")
}

func TestLayoutTidy(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0) // 30 mm to the right: far apart
	wire(t, s, "A", "connector1", "B", "connector0")
	wire(t, s, "A", "connector3", "B", "connector2")
	r := s.CheckLayout(fakeLib{}, "schematic")
	if len(r.Issues) != 0 || r.Crossings != 0 || r.Wires != 2 || r.WireLength <= 0 {
		t.Errorf("tidy layout: %+v", r)
	}
}

func TestLayoutCrossing(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0)
	wire(t, s, "A", "connector1", "B", "connector2") // top-right to bottom-left
	wire(t, s, "A", "connector3", "B", "connector0") // bottom-right to top-left: they cross
	// breadboard/schematic: wires only join at their ends, so this is info, not a warning
	r := s.CheckLayout(fakeLib{}, "schematic")
	if r.Crossings != 1 || !strings.Contains(kinds(r, LevelInfo), "crossing") || kinds(r, LevelWarn) != "" {
		t.Fatalf("crossing: %+v", r)
	}
	if txt := r.Issues[len(r.Issues)-1].Text; !strings.Contains(txt, "crosses") || !strings.Contains(txt, "Wire") {
		t.Errorf("text: %s", txt)
	}
	if len(r.Brief()) != 0 {
		t.Errorf("brief: %v", r.Brief())
	}
	if r.Tidiness != 93 { // 100 - 1 crossing - 2 diagonals * 3
		t.Errorf("a different-net crossing costs 1 tidiness point: %v", r.Tidiness)
	}
}

func TestLayoutPinContact(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	// B placed so that its top-left pin sits on A's top-right pin: 34 px = 8.99 mm
	place(t, s, "B", 8.9, 0)
	r := s.CheckLayout(fakeLib{}, "schematic")
	if kinds(r, LevelError) != "pin-contact,pin-contact" { // top and bottom pin pairs both touch
		t.Fatalf("want a pin-contact error: %+v", r.Issues)
	}
	if !strings.Contains(r.Issues[0].Text, "A.P1") || !strings.Contains(r.Issues[0].Text, "B.P0") {
		t.Errorf("text: %s", r.Issues[0].Text)
	}
	// once the two pins are wired together they are one net: no error
	wire(t, s, "A", "connector1", "B", "connector0")
	wire(t, s, "A", "connector3", "B", "connector2")
	if r := s.CheckLayout(fakeLib{}, "schematic"); kinds(r, LevelError) != "" {
		t.Errorf("same net still an error: %+v", r.Issues)
	}
}

func TestLayoutWireOverPin(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0)
	place(t, s, "C", 15, 0) // C sits between A and B, on the line of the wire A.P1 -> B.P0
	wire(t, s, "A", "connector1", "B", "connector0")
	r := s.CheckLayout(fakeLib{}, "schematic")
	if kinds(r, LevelError) == "" || !strings.Contains(kinds(r, LevelError), "wire-over-pin") {
		t.Errorf("want wire-over-pin: %+v", r.Issues)
	}
	if !strings.Contains(kinds(r, LevelWarn), "wire-over-part") {
		t.Errorf("want wire-over-part: %+v", r.Issues)
	}
}

func TestLayoutPartsOverlap(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 5, 5) // boxes overlap, no pins touch
	r := s.CheckLayout(fakeLib{}, "schematic")
	if kinds(r, LevelWarn) != "parts-overlap" || kinds(r, LevelError) != "" {
		t.Errorf("%+v", r.Issues)
	}
}

func TestLayoutReportsCoordinates(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 10, 20)
	r := s.CheckLayout(fakeLib{}, "schematic")
	if len(r.Parts) != 1 || r.Parts[0].Title != "A" || r.Parts[0].X != 10 || r.Parts[0].Y != 20 {
		t.Fatalf("parts: %+v", r.Parts)
	}
	p1, ok := r.Parts[0].Pins["P1"] // top-right pin: 34..40 px of a 40 px box
	if !ok || p1.X < 20.4 || p1.X > 20.5 || p1.Y < 20.8 || p1.Y > 20.9 {
		t.Errorf("pin P1: %+v", r.Parts[0].Pins)
	}
}

func placeIn(t *testing.T, s *Sketch, view, title string, x, y float64) {
	t.Helper()
	if _, err := s.AddPart(fakeLib{}, "chip", AddOptions{Title: title, Position: map[string]XY{view: {X: x, Y: y}}}); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutParallelClose(t *testing.T) {
	s, _ := Parse(project.Template)
	// two horizontal wires 1.6 mm apart running 70 mm side by side
	placeIn(t, s, "schematic", "A", 0, 0)
	placeIn(t, s, "schematic", "B", 80, 0)
	placeIn(t, s, "schematic", "C", 0, 1.6)
	placeIn(t, s, "schematic", "D", 80, 1.6)
	wire(t, s, "A", "connector1", "B", "connector0")
	wire(t, s, "C", "connector1", "D", "connector0")
	r := s.CheckLayout(fakeLib{}, "schematic")
	if got := kinds(r, LevelInfo); !strings.Contains(got, "parallel-close") {
		t.Fatalf("want parallel-close info: %+v", r.Issues)
	}
	// ... and on top of each other (0.4 mm) it is a warning
	s2, _ := Parse(project.Template)
	placeIn(t, s2, "schematic", "A", 0, 0)
	placeIn(t, s2, "schematic", "B", 80, 0)
	placeIn(t, s2, "schematic", "C", 0, 0.4)
	placeIn(t, s2, "schematic", "D", 80, 0.4)
	wire(t, s2, "A", "connector1", "B", "connector0")
	wire(t, s2, "C", "connector1", "D", "connector0")
	r = s2.CheckLayout(fakeLib{}, "schematic")
	if got := kinds(r, LevelWarn); !strings.Contains(got, "parallel-close") {
		t.Fatalf("want parallel-close warning: %+v", r.Issues)
	}
}

func TestLayoutParallelFarAndSameNetAreFine(t *testing.T) {
	s, _ := Parse(project.Template)
	placeIn(t, s, "schematic", "A", 0, 0)
	placeIn(t, s, "schematic", "B", 80, 0)
	wire(t, s, "A", "connector1", "B", "connector0") // y 0.85+
	wire(t, s, "A", "connector3", "B", "connector2") // 9.6 mm lower
	r := s.CheckLayout(fakeLib{}, "schematic")
	if strings.Contains(kinds(r, LevelWarn)+kinds(r, LevelInfo), "parallel-close") {
		t.Fatalf("9 mm apart is fine: %+v", r.Issues)
	}
}

func TestLayoutSameColourCrossing(t *testing.T) {
	build := func(c1, c2 string) LayoutReport {
		s, _ := Parse(project.Template)
		placeIn(t, s, "breadboard", "A", 0, 0)
		placeIn(t, s, "breadboard", "B", 80, 0)
		for _, w := range []struct{ a, ac, b, bc, c string }{
			{"A", "connector1", "B", "connector2", c1}, // top-right to bottom-left
			{"A", "connector3", "B", "connector0", c2}, // bottom-right to top-left: they cross
		} {
			if _, err := s.Connect(fakeLib{}, "breadboard", Endpoint{w.a, w.ac}, Endpoint{w.b, w.bc}, ConnectOptions{Color: w.c}); err != nil {
				t.Fatal(err)
			}
		}
		return s.CheckLayout(fakeLib{}, "breadboard")
	}
	if r := build("#ff0000", "#ff0000"); !strings.Contains(kinds(r, LevelWarn), "crossing") {
		t.Fatalf("same colour should warn: %+v", r.Issues)
	}
	if r := build("#ff0000", "#000000"); strings.Contains(kinds(r, LevelWarn), "crossing") || !strings.Contains(kinds(r, LevelInfo), "crossing") {
		t.Fatalf("different colours are fine: %+v", r.Issues)
	}
}

func TestCropView(t *testing.T) {
	svg := []byte(`<?xml version='1.0'?>
<svg xmlns='http://www.w3.org/2000/svg' x='0in' y='0in' width='3.5in' height='3in' viewBox='0 0 3500 3000' ><g stroke-width='3'/></svg>`)
	out, err := CropView(svg, 90, 180, 180, 270) // a 1 in square at (1 in, 2 in)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{"viewBox='1000.00 2000.00 1000.00 1000.00'", "width='1.0000in'", "height='1.0000in'", "stroke-width='3'"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if _, err := CropView(svg, 500, 500, 400, 400); err == nil {
		t.Error("an empty area should fail")
	}
}

func TestFocusBox(t *testing.T) {
	s, _ := Parse(project.Template)
	placeIn(t, s, "schematic", "A", 0, 0)
	placeIn(t, s, "schematic", "B", 40, 0)
	wire(t, s, "A", "connector1", "B", "connector0")
	x0, y0, x1, y1, err := s.FocusBox(fakeLib{}, "schematic", "a", 5)
	if err != nil || x1-x0 <= 0 || y1-y0 <= 0 {
		t.Fatalf("part box: %v %v %v %v %v", x0, y0, x1, y1, err)
	}
	if w, _, _, _, err := s.FocusBox(fakeLib{}, "schematic", "Wire1", 0); err != nil || w <= 0 {
		t.Fatalf("wire box: %v %v", w, err)
	}
	if _, _, _, _, err := s.FocusBox(fakeLib{}, "schematic", "nope", 5); err == nil {
		t.Error("unknown title should fail")
	}
}
