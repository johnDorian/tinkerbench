package sketch

import (
	"reflect"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

func TestChainViaAndRemove(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0)
	titles, err := s.Connect(fakeLib{}, "schematic", Endpoint{"A", "connector1"}, Endpoint{"B", "connector0"}, ConnectOptions{Via: []XY{{X: 20, Y: 20}}})
	if err != nil || len(titles) != 2 {
		t.Fatalf("titles %v err %v", titles, err)
	}
	sum := s.Describe(fakeLib{})
	found := false
	for _, n := range sum.Nets {
		if reflect.DeepEqual(n.Pins, []string{"A.connector1", "B.connector0"}) {
			found = true
		}
	}
	if !found {
		t.Errorf("chain does not join the pins: %+v", sum.Nets)
	}
	r := s.CheckLayout(fakeLib{}, "schematic")
	if r.Wires != 1 || r.Segments != 2 || r.Bends != 1 {
		t.Errorf("chain counts: %+v", r)
	}
	if k := kinds(r, LevelWarn) + kinds(r, LevelError); k != "" {
		t.Errorf("joint flagged: %s %+v", k, r.Issues)
	}
	gone, err := s.Remove(titles[1])
	if err != nil || len(gone) != 2 {
		t.Fatalf("remove chain: %v %v", gone, err)
	}
	if strings.Contains(s.String(), "WireModuleID") {
		t.Errorf("wire left behind:\n%s", s.String())
	}
	// one segment only
	titles, _ = s.Connect(fakeLib{}, "schematic", Endpoint{"A", "connector1"}, Endpoint{"B", "connector0"}, ConnectOptions{Via: []XY{{X: 20, Y: 20}}})
	if gone, _ := s.RemoveOpt(titles[0], true); len(gone) != 1 {
		t.Errorf("only_segment removed %v", gone)
	}
}

func TestViaOnPinRefused(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0)
	// B's connector3 pin sits near (30+12, 12) mm; put a waypoint right on it
	_, err := s.Connect(fakeLib{}, "schematic", Endpoint{"A", "connector1"}, Endpoint{"B", "connector0"}, ConnectOptions{Via: []XY{{X: 30.85, Y: 0.85}}})
	if err == nil || !strings.Contains(err.Error(), "pin-contact") {
		t.Errorf("err = %v", err)
	}
}

func TestOrthogonalAroundPart(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "C", 15, 0) // in the way
	place(t, s, "B", 30, 0)
	if _, err := s.Connect(fakeLib{}, "schematic", Endpoint{"A", "connector1"}, Endpoint{"B", "connector0"}, ConnectOptions{Style: "orthogonal"}); err != nil {
		t.Fatal(err)
	}
	r := s.CheckLayout(fakeLib{}, "schematic")
	if r.Count(LevelError)+r.Count(LevelWarn) != 0 || r.OrthogonalPct != 100 || r.Wires != 1 {
		t.Errorf("not clean: %+v", r)
	}
	if r.Bends < 2 {
		t.Errorf("expected a detour, bends = %d", r.Bends)
	}
}

func TestOrthogonalNoRoute(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 0)
	if _, err := s.Connect(fakeLib{}, "schematic", Endpoint{"A", "connector1"}, Endpoint{"B", "connector0"}, ConnectOptions{Style: "nonsense"}); err == nil {
		t.Error("bad style accepted")
	}
}

func TestDiagonalAndBodyFindings(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 30, 0)
	place(t, s, "B", 0, 0)
	wire(t, s, "A", "connector3", "B", "connector0") // bottom-right of A to top-left of B: across both bodies
	r := s.CheckLayout(fakeLib{}, "schematic")
	k := kinds(r, LevelInfo)
	if !strings.Contains(k, "diagonal") {
		t.Errorf("want diagonal: %+v", r.Issues)
	}
	if !strings.Contains(kinds(r, LevelWarn), "wire-over-part") {
		t.Errorf("want wire-over-part for the wire cutting its own part: %+v", r.Issues)
	}
	if r.Diagonals != 1 || r.OrthogonalPct != 0 || r.Tidiness >= 100 {
		t.Errorf("numbers: %+v", r)
	}
}

func TestTidyKeepsNets(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 20)
	wire(t, s, "A", "connector1", "B", "connector0")
	wire(t, s, "A", "connector3", "B", "connector2")
	before := s.Describe(fakeLib{}).Nets
	res, err := s.TidyView(fakeLib{}, "schematic")
	if err != nil || len(res.Failed) != 0 || len(res.Rerouted) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if res.After.OrthogonalPct != 100 || res.After.Crossings != 0 {
		t.Errorf("after: %+v", res.After)
	}
	if after := s.Describe(fakeLib{}).Nets; !reflect.DeepEqual(before, after) {
		t.Errorf("nets changed:\n%v\n%v", before, after)
	}
}

func TestAlignAndPlaceNear(t *testing.T) {
	s, _ := Parse(project.Template)
	place(t, s, "A", 0, 0)
	place(t, s, "B", 30, 7)
	if err := s.AlignParts(fakeLib{}, "schematic", []string{"A", "B"}, "top"); err != nil {
		t.Fatal(err)
	}
	r := s.CheckLayout(fakeLib{}, "schematic")
	if r.Parts[0].Y != r.Parts[1].Y {
		t.Errorf("not aligned: %+v", r.Parts)
	}
	if err := s.PlaceNear(fakeLib{}, "schematic", "B", "A", "below", 5); err != nil {
		t.Fatal(err)
	}
	r = s.CheckLayout(fakeLib{}, "schematic")
	if gap := r.Parts[1].Y - (r.Parts[0].Y + r.Parts[0].H); gap < 5 || gap > 8 {
		t.Errorf("gap %.2f: %+v", gap, r.Parts)
	}
	if SnapMM(3.5, 2.54) != 2.54 || SnapMM(4, 2.54) != 5.08 {
		t.Error("snap")
	}
}
