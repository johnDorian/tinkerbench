package plates

import (
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

var bed = profiles.Bed{Width: 255, Depth: 255, MaxHeight: 260, Shape: [][2]float64{{0, 0}, {255, 0}, {255, 255}, {0, 255}}}

func TestArrangeFitsAndDoesNotOverlap(t *testing.T) {
	var items []Item
	for i := 0; i < 12; i++ {
		items = append(items, Item{ID: string(rune('a' + i)), W: 30 + float64(i%3)*10, D: 40 + float64(i%4)*5})
	}
	centres, err := Arrange(bed, items, 6)
	if err != nil {
		t.Fatal(err)
	}
	boxes := map[string]geom.Box{}
	for _, it := range items {
		c := centres[it.ID]
		boxes[it.ID] = geom.Box{Min: geom.Vec3{c[0] - it.W/2, c[1] - it.D/2, 0}, Max: geom.Vec3{c[0] + it.W/2, c[1] + it.D/2, 1}}
	}
	for id, b := range boxes {
		if b.Min[0] < 0 || b.Min[1] < 0 || b.Max[0] > 255 || b.Max[1] > 255 {
			t.Fatalf("%s outside bed: %v", id, b)
		}
	}
	for a, ba := range boxes {
		for c, bc := range boxes {
			if a < c && ba.Min[0] < bc.Max[0] && bc.Min[0] < ba.Max[0] && ba.Min[1] < bc.Max[1] && bc.Min[1] < ba.Max[1] {
				t.Fatalf("%s and %s overlap", a, c)
			}
		}
	}
	if _, err := Arrange(bed, []Item{{ID: "huge", W: 300, D: 10}}, 6); err == nil {
		t.Fatal("an object wider than the bed should not fit")
	}
}

func TestAutoPlaceAvoidsPlaced(t *testing.T) {
	placed := map[string]geom.Box{"o1": {Min: geom.Vec3{100, 100, 0}, Max: geom.Vec3{160, 160, 10}}}
	x, y := AutoPlace(bed, placed, 50, 50)
	b := geom.Box{Min: geom.Vec3{x - 25, y - 25, 0}, Max: geom.Vec3{x + 25, y + 25, 1}}
	if b.Min[0] < 160 && 100 < b.Max[0] && b.Min[1] < 160 && 100 < b.Max[1] {
		t.Fatalf("auto-placed object overlaps o1: centre (%v,%v)", x, y)
	}
}

func TestCheckBlocksOutsideAndTall(t *testing.T) {
	objs := []Object{{ID: "o1"}, {ID: "o2"}, {ID: "o3"}}
	boxes := map[string]geom.Box{
		"o1": {Min: geom.Vec3{0, 0, 0}, Max: geom.Vec3{10, 10, 10}},
		"o2": {Min: geom.Vec3{250, 0, 0}, Max: geom.Vec3{270, 10, 10}},
		"o3": {Min: geom.Vec3{5, 5, 0}, Max: geom.Vec3{15, 15, 300}},
	}
	warn, blocking := Check(bed, objs, boxes)
	if !blocking {
		t.Fatal("outside and tall objects should block")
	}
	want := map[string]bool{"object o2 outside bed": true, "object o3 taller than the printer (260 mm)": true, "objects o1 and o3 overlap": true}
	for _, w := range warn {
		if !want[w] {
			t.Errorf("unexpected warning %q", w)
		}
		delete(want, w)
	}
	if len(want) != 0 {
		t.Errorf("missing warnings: %v", want)
	}
}

func TestHashFollowsModelChecksum(t *testing.T) {
	store, err := models.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Create(models.Input{Name: "cube", Data: testkit.Cube([3]float64{}, 10)})
	if err != nil {
		t.Fatal(err)
	}
	p := Plate{ID: "p", Name: "p", Machine: "m", Process: "pr", Filament: "f", Overrides: map[string]string{"wall_loops": "3"},
		Objects: []Object{{ID: "o1", Model: m.ID, Scale: 1}}}
	h1, err := Hash(p, store, "2.4.2")
	if err != nil {
		t.Fatal(err)
	}
	if h2, _ := Hash(p, store, "2.4.2"); h2 != h1 {
		t.Fatal("hash should be stable")
	}
	p.Name = "renamed" // names don't change the G-code
	if h3, _ := Hash(p, store, "2.4.2"); h3 != h1 {
		t.Fatal("renaming should not change the hash")
	}
	p.Objects[0].X = 5
	if h4, _ := Hash(p, store, "2.4.2"); h4 == h1 {
		t.Fatal("moving an object should change the hash")
	}
	p.Objects[0].X = 0
	if _, err := store.Replace(m.ID, models.Input{Name: "cube", Data: testkit.Cube([3]float64{}, 11)}); err != nil {
		t.Fatal(err)
	}
	if h5, _ := Hash(p, store, "2.4.2"); h5 == h1 {
		t.Fatal("new geometry under the same id should change the hash")
	}
}
