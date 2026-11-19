package sketch

import (
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

func TestCompare(t *testing.T) {
	s, _ := Parse(project.Template)
	placeIn(t, s, "schematic", "A", 0, 0)
	placeIn(t, s, "schematic", "B", 40, 0)
	wire(t, s, "A", "connector1", "B", "connector0")
	before, err := Parse(s.String())
	if err != nil {
		t.Fatal(err)
	}

	if d := Compare(fakeLib{}, before, s); !d.Unchanged {
		t.Fatalf("same sketch: %+v", d)
	}

	// move B, add C, add a second wire
	x, y := 50.0, 10.0
	if err := s.Move(fakeLib{}, "B", "schematic", &x, &y, nil); err != nil {
		t.Fatal(err)
	}
	placeIn(t, s, "schematic", "C", 0, 40)
	wire(t, s, "A", "connector3", "C", "connector0")
	d := Compare(fakeLib{}, before, s)
	if d.Unchanged {
		t.Fatal("changed sketch reported as unchanged")
	}
	if len(d.Parts.Added) != 1 || !strings.HasPrefix(d.Parts.Added[0], "C ") {
		t.Errorf("added: %v", d.Parts.Added)
	}
	if len(d.Parts.Changed) != 1 || !strings.Contains(d.Parts.Changed[0], "B moved in schematic") {
		t.Errorf("changed: %v", d.Parts.Changed)
	}
	if c := d.Connections["schematic"]; len(c.Added) != 1 || !strings.Contains(c.Added[0], "A.connector3 + C.connector0") || len(c.Removed) != 0 {
		t.Errorf("connections: %+v", d.Connections)
	}
	rs := d.Routes["schematic"]
	var gotNew, gotRerouted bool
	for _, r := range rs {
		gotNew = gotNew || strings.HasPrefix(r, "new wire")
		gotRerouted = gotRerouted || strings.HasPrefix(r, "rerouted")
	}
	if !gotNew || !gotRerouted { // the first wire follows B to its new place
		t.Errorf("routes: %v", rs)
	}
	if ld, ok := d.Layout["schematic"]; !ok || ld.Before.Wires != 1 || ld.After.Wires != 2 {
		t.Errorf("layout: %+v", d.Layout)
	}
}
