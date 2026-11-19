package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/parts"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// waitParts blocks until the part renders of project name finish and returns their ok flag.
func waitParts(t *testing.T, sub chan events.Event, name string) bool {
	t.Helper()
	timeout := time.After(3 * time.Minute)
	for {
		select {
		case e := <-sub:
			if e.Kind == events.PartsDone && e.Project == name {
				return e.OK != nil && *e.OK
			}
		case <-timeout:
			t.Fatalf("parts of %s did not finish", name)
		}
	}
}

// TestParts renders a two-part model with the real openscad: the listing, the per-part STLs,
// the unlabelled remainder, features, source lines and anchors. Skipped without openscad.
func TestParts(t *testing.T) {
	bin, err := exec.LookPath("openscad")
	if err != nil {
		t.Skip("openscad not installed")
	}
	r := render.New(bin, 3*time.Minute, 2)
	r.LibDir = t.TempDir()
	if err := render.InstallLibrary(r.LibDir); err != nil {
		t.Fatal(err)
	}
	store, _ := project.Open(t.TempDir())
	a := New(store, r, events.NewHub())
	sub := a.Hub.Subscribe()
	ctx := context.Background()

	src := "use <workbench.scad>\n" +
		"part(\"lid\") translate([0, 0, 10]) cube([20, 20, 2]);\n" +
		"part(\"body\") cube([20, 20, 10]);\n" +
		"feature(\"M3 hole\", [5, 5, 5]);\n"
	_, res, err := a.Create(ctx, "boxy", "", src, "test", RenderSync)
	if err != nil || res == nil || !res.OK {
		t.Fatalf("create: %v %+v", err, res)
	}
	if !waitParts(t, sub, "boxy") {
		t.Fatal("parts failed")
	}

	l, err := a.Parts("boxy")
	if err != nil {
		t.Fatal(err)
	}
	if l.Pending || len(l.Parts) != 2 || len(l.Features) != 1 {
		t.Fatalf("listing = %+v", l)
	}
	if l.ModelTime == nil || !l.ModelTime.Equal(res.STLTime) {
		t.Fatalf("model_time %v, stl_time %v", l.ModelTime, res.STLTime)
	}
	lid, body := l.Parts[0], l.Parts[1]
	if lid.Name != "lid" || lid.File != "main.scad" || lid.Line != 2 || !lid.OK || lid.Triangles == 0 {
		t.Fatalf("lid = %+v", lid)
	}
	if lid.BBox == nil || lid.BBox.Min[2] != 10 || lid.BBox.Max[2] != 12 {
		t.Fatalf("lid bbox = %+v", lid.BBox)
	}
	if body.Name != "body" || body.Line != 3 || body.BBox.Max[2] != 10 || body.BBox.Min[2] != 0 {
		t.Fatalf("body = %+v bbox %+v", body, body.BBox)
	}
	if f := l.Features[0]; f.Name != "M3 hole" || f.At != [3]float64{5, 5, 5} || f.Line != 4 {
		t.Fatalf("feature = %+v", f)
	}
	// Every piece is covered: no unlabelled entry.
	for _, p := range l.Parts {
		if p.Name == parts.Unlabelled {
			t.Fatal("fully labelled model has an unlabelled entry")
		}
	}
	path, err := a.PartFile("boxy", "lid")
	if err != nil {
		t.Fatal(err)
	}
	if tris, err := render.ReadTrianglesFile(path); err != nil || len(tris) != lid.Triangles {
		t.Fatalf("lid STL: %d triangles, %v", len(tris), err)
	}
	if _, err := a.PartFile("boxy", "nope"); err == nil {
		t.Fatal("unknown part must not resolve")
	}

	// Anchors: a comment on the lid's top face goes on the lid's line; a point off the model
	// is model-only and stale at once; the body's top face is fine.
	up := [3]float64{0, 0, 1}
	c1, err := a.AddComment("boxy", "", 0, 0, "lid top", nil, &project.Anchor{Part: "lid", Kind: "face", Point: [3]float64{10, 10, 12}, Normal: &up}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if c1.File != "main.scad" || c1.Line != 2 || c1.Anchor.Stale {
		t.Fatalf("lid comment = %+v", c1)
	}
	c2, err := a.AddComment("boxy", "", 0, 0, "nowhere", nil, &project.Anchor{Kind: "point", Point: [3]float64{100, 100, 100}}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if c2.File != "" || c2.Line != 0 || !c2.Anchor.Stale {
		t.Fatalf("model-only comment = %+v", c2)
	}

	// Move the lid away from the anchored face: the next render flags it stale.
	moved := strings.Replace(src, "translate([0, 0, 10])", "translate([0, 0, 30])", 1)
	if _, err := a.WriteFile(ctx, "boxy", project.MainFile, moved, "test", "", RenderSync); err != nil {
		t.Fatal(err)
	}
	if !waitParts(t, sub, "boxy") {
		t.Fatal("parts failed after the move")
	}
	cs, _ := a.Store.Comments("boxy", "", true)
	for _, c := range cs {
		if c.ID == c1.ID && !c.Anchor.Stale {
			t.Fatal("lid anchor should be stale after the lid moved")
		}
	}
}

// TestPartsUnlabelled: geometry outside every part is listed as (unlabelled), and the parts
// that are rendered carry it too (documented limitation).
func TestPartsUnlabelled(t *testing.T) {
	bin, err := exec.LookPath("openscad")
	if err != nil {
		t.Skip("openscad not installed")
	}
	r := render.New(bin, 3*time.Minute, 2)
	r.LibDir = t.TempDir()
	render.InstallLibrary(r.LibDir)
	store, _ := project.Open(t.TempDir())
	a := New(store, r, events.NewHub())
	sub := a.Hub.Subscribe()

	src := "use <workbench.scad>\npart(\"a\") cube(5);\ntranslate([30, 0, 0]) cube(2);\n"
	if _, _, err := a.Create(context.Background(), "mixed", "", src, "test", RenderSync); err != nil {
		t.Fatal(err)
	}
	if !waitParts(t, sub, "mixed") {
		t.Fatal("parts failed")
	}
	l, _ := a.Parts("mixed")
	if len(l.Parts) != 2 || l.Parts[1].Name != parts.Unlabelled || l.Parts[1].File != "" || l.Parts[1].Line != 0 {
		t.Fatalf("parts = %+v", l.Parts)
	}
	if u := l.Parts[1]; u.BBox.Min[0] != 30 || u.BBox.Max[0] != 32 {
		t.Fatalf("unlabelled bbox = %+v", u.BBox)
	}
	if _, err := a.PartFile("mixed", parts.Unlabelled); err != nil {
		t.Fatal(err)
	}
	// Known limitation: plain geometry is in every part's mesh too.
	if a := l.Parts[0].BBox; a.Max[0] != 32 {
		t.Fatalf("expected the unlabelled cube inside part a's mesh, bbox %+v", a)
	}
}
