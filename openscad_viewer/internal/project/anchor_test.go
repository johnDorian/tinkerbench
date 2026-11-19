package project

import (
	"strings"
	"testing"
)

func TestModelOnlyComments(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.Create("p", "", "a = 1;\nb = 2;\n", "t"); err != nil {
		t.Fatal(err)
	}
	anchor := &Anchor{Part: "lid", Point: [3]float64{1, 2, 3}}
	sug := "x"
	if _, err := s.AddComment("p", "", 0, 0, "on the model", &sug, anchor, "web"); err == nil {
		t.Fatal("a suggestion on a model-only comment must be refused")
	}
	if _, err := s.AddComment("p", "", 0, 0, "no anchor", nil, nil, "web"); err == nil {
		t.Fatal("a comment with neither a line nor an anchor must be refused")
	}
	c, err := s.AddComment("p", "", 0, 0, "on the model", nil, anchor, "web")
	if err != nil {
		t.Fatal(err)
	}
	if c.File != "" || c.Line != 0 || c.Quote != "" || c.Anchor == nil || c.Anchor.Kind != KindPoint {
		t.Fatalf("model-only comment = %+v", c)
	}
	// Re-anchoring on save and apply-suggestion leave it alone.
	if _, err := s.WriteFile("p", MainFile, "// new\na = 1;\nb = 2;\n", "agent", ""); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.Comments("p", "", false)
	if len(cs) != 1 || cs[0].File != "" || cs[0].Outdated {
		t.Fatalf("after save: %+v", cs)
	}
	if _, _, err := s.ApplySuggestion("p", c.ID, "web"); err == nil || !strings.Contains(err.Error(), "nothing to apply") {
		t.Fatalf("apply on model-only: %v", err)
	}
	// Reply and resolve work.
	if _, err := s.ReplyComment("p", c.ID, "agent", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveComment("p", c.ID, true, "agent"); err != nil {
		t.Fatal(err)
	}
}

func TestAnchorKindDropsUnusedFields(t *testing.T) {
	n := [3]float64{0, 0, 1}
	a := &Anchor{Kind: KindPoint, Point: [3]float64{1, 1, 1}, Normal: &n, Edge: &[2][3]float64{{0, 0, 0}, {1, 1, 1}}}
	if err := NormalizeAnchor(a); err != nil {
		t.Fatal(err)
	}
	if a.Normal != nil || a.Edge != nil {
		t.Fatalf("point anchor kept fields: %+v", a)
	}
	if err := NormalizeAnchor(&Anchor{Kind: "corner"}); err == nil {
		t.Fatal("unknown kind must be refused")
	}
}

func TestUpdateAnchorsFlagsAndClears(t *testing.T) {
	s, _ := Open(t.TempDir())
	if _, err := s.Create("p", "", "a = 1;\n", "t"); err != nil {
		t.Fatal(err)
	}
	near := &Anchor{Kind: KindPoint, Point: [3]float64{1, 1, 1}}
	far := &Anchor{Kind: KindPoint, Point: [3]float64{9, 9, 9}}
	if _, err := s.AddComment("p", "", 0, 0, "near", nil, near, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment("p", "", 0, 0, "far", nil, far, "web"); err != nil {
		t.Fatal(err)
	}
	fitsOnlyNear := func(a Anchor) bool { return a.Point[0] < 5 }
	n, err := s.UpdateAnchors("p", fitsOnlyNear)
	if err != nil || n != 1 {
		t.Fatalf("changed = %d, %v", n, err)
	}
	cs, _ := s.Comments("p", "", true)
	if cs[0].Anchor.Stale || !cs[1].Anchor.Stale {
		t.Fatalf("stale flags: %+v / %+v", cs[0].Anchor, cs[1].Anchor)
	}
	// Nothing changes: nothing written, nothing counted.
	if n, _ := s.UpdateAnchors("p", fitsOnlyNear); n != 0 {
		t.Fatalf("second pass changed %d", n)
	}
	// Geometry comes back: the flag clears.
	if n, _ := s.UpdateAnchors("p", func(Anchor) bool { return true }); n != 1 {
		t.Fatalf("clear changed %d", n)
	}
	cs, _ = s.Comments("p", "", true)
	if cs[1].Anchor.Stale {
		t.Fatal("anchor should be valid again")
	}
}
