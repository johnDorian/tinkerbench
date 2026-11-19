package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/app"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testapp"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func TestPlateWorkflow(t *testing.T) {
	a := testapp.NewApp(t)
	m, err := a.UploadModel("Bracket", testkit.Cube([3]float64{}, 20))
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.CreatePlate("Xmas case", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Machine != testkit.Machine || p.Process != testkit.Process {
		t.Fatalf("defaults not applied: %+v", p)
	}

	// two auto-placed objects do not overlap and sit on the bed
	v, err := a.AddObject(p.ID, app.ObjectSpec{Model: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	v, err = a.AddObject(p.ID, app.ObjectSpec{Model: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Objects) != 2 || v.Objects[0].ID != "o1" || v.Objects[1].ID != "o2" {
		t.Fatalf("objects = %+v", v.Objects)
	}
	b1, b2 := v.Objects[0].Bounds, v.Objects[1].Bounds
	if b1[0][2] != 0 || b2[0][2] != 0 {
		t.Fatalf("objects should sit on the bed: %v %v", b1, b2)
	}
	if b1[1][0] > b2[0][0] && b2[1][0] > b1[0][0] && b1[1][1] > b2[0][1] && b2[1][1] > b1[0][1] {
		t.Fatalf("auto-placed objects overlap: %v %v", b1, b2)
	}
	// transform: the column-major matrix's translation maps the model's origin to the object's placement
	if v.Objects[0].Transform[15] != 1 {
		t.Fatalf("transform = %v", v.Objects[0].Transform)
	}

	// moving, scaling and rotating keep the object on the bed
	x := 128.0
	v, err = a.PatchObject(p.ID, "o1", app.ObjectPatch{X: &x, Rotation: &[3]float64{0, 0, 90}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Objects[0].X != 128 || v.Objects[0].Rotation[2] != 90 {
		t.Fatalf("patched object = %+v", v.Objects[0])
	}

	v, err = a.Arrange(p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range v.Objects {
		if o.X <= 0 || o.X >= 255 || o.Y <= 0 || o.Y >= 255 {
			t.Fatalf("arranged object off the bed: %+v", o)
		}
	}

	v, err = a.DuplicateObject(p.ID, "o1")
	if err != nil || len(v.Objects) != 3 {
		t.Fatalf("duplicate: %d objects, %v", len(v.Objects), err)
	}
	v, err = a.RemoveObject(p.ID, "o3")
	if err != nil || len(v.Objects) != 2 {
		t.Fatalf("remove: %d objects, %v", len(v.Objects), err)
	}

	// the model is on a plate, so it can't be deleted
	if err := a.DeleteModel(m.ID); StatusIs(err) != 409 {
		t.Fatalf("delete used model: %v", err)
	}
	// overrides: known keys are kept, unknown keys refused
	one := "25%"
	if _, err := a.UpdatePlate(p.ID, app.PlatePatch{Overrides: map[string]*string{"sparse_infill_density": &one}}); err != nil {
		t.Fatal(err)
	}
	bad := "1"
	if _, err := a.UpdatePlate(p.ID, app.PlatePatch{Overrides: map[string]*string{"not_a_setting": &bad}}); StatusIs(err) != 400 {
		t.Fatalf("unknown override: %v", err)
	}
	got, _ := a.GetPlate(p.ID)
	if got.Overrides["sparse_infill_density"] != "25%" {
		t.Fatalf("overrides = %v", got.Overrides)
	}
}

func TestSliceStaleAndPrintGuards(t *testing.T) {
	a := testapp.NewApp(t)
	m, _ := a.UploadModel("cube", testkit.Cube([3]float64{}, 20))
	p, _ := a.CreatePlate("p", "", "", "")
	if _, err := a.AddObject(p.ID, app.ObjectSpec{Model: m.ID}); err != nil {
		t.Fatal(err)
	}

	// not sliced yet
	if _, _, err := a.PrintFile(p.ID); StatusIs(err) != 409 {
		t.Fatalf("print before slice: %v", err)
	}
	// a finished slice made from an older plate hash is stale
	dir := a.Plates.Dir(p.ID)
	rec := map[string]any{"status": "done", "gcode_name": "p_PETG_0.20_1m00s.gcode", "hash": "not-the-current-hash"}
	b, _ := json.Marshal(rec)
	os.WriteFile(filepath.Join(dir, "slice.json"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "plate.gcode"), []byte("G28\n"), 0o644)
	res, err := a.SliceStatus(p.ID)
	if err != nil || !res.Stale {
		t.Fatalf("slice = %+v, %v; want stale", res, err)
	}
	if _, _, err := a.PrintFile(p.ID); StatusIs(err) != 409 {
		t.Fatalf("print of stale slice: %v", err)
	}
	// printing is disabled here (no PRINTER_URL): refused with 409 too
	if _, err := a.PrintPlate(context.Background(), p.ID); StatusIs(err) != 409 {
		t.Fatalf("print with printer disabled: %v", err)
	}
	// a plate being sliced can't be deleted
	rec["status"] = "queued"
	b, _ = json.Marshal(rec)
	os.WriteFile(filepath.Join(dir, "slice.json"), b, 0o644)
	if err := a.DeletePlate(p.ID); StatusIs(err) != 409 {
		t.Fatalf("delete queued plate: %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	a := testapp.NewApp(t)
	if _, err := a.GetPlate("missing-id"); StatusIs(err) != 404 {
		t.Fatalf("missing plate: %v", err)
	}
	if _, err := a.UploadModel("bad", []byte("not an stl at all")); StatusIs(err) != 400 {
		t.Fatalf("bad upload: %v", err)
	}
	if _, err := a.CreatePlate("x", "No Such Machine", "", ""); StatusIs(err) != 400 {
		t.Fatalf("unknown machine: %v", err)
	}
	cfg, err := a.Config()
	if err != nil || cfg.Bed.Width != 255 || len(cfg.CuratedSettings) == 0 {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	var layer bool
	for _, s := range cfg.CuratedSettings {
		if s.Key == "layer_height" && s.Default == "0.2" && s.Source == "process" {
			layer = true
		}
	}
	if !layer {
		t.Fatalf("curated defaults = %+v", cfg.CuratedSettings)
	}
}

func StatusIs(err error) int {
	if err == nil {
		return 0
	}
	return app.StatusOf(err)
}

// A bed override is refused by the app, since the slicer ignores it.
func TestBedOverrideRefused(t *testing.T) {
	a := testapp.NewApp(t)
	p, err := a.CreatePlate("Bed", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bed_type", "curr_bed_type"} {
		v := "Textured PEI Plate"
		if _, err := a.UpdatePlate(p.ID, app.PlatePatch{Overrides: map[string]*string{k: &v}}); err == nil {
			t.Fatalf("override %s accepted", k)
		}
	}
}
