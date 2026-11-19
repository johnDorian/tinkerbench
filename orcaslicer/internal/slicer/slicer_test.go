package slicer

import (
	"bufio"
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

// harness is a slicer over a temp data dir with one cube model.
type harness struct {
	s      *Slicer
	plates *plates.Store
	models *models.Store
	cube   models.Model
	dir    string
}

func newHarness(t *testing.T, bin, profilesDir string) *harness {
	t.Helper()
	dir := t.TempDir()
	ms, err := models.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := plates.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cube, err := ms.Create(models.Input{Name: "cube", Source: "upload", Data: testkit.Cube([3]float64{200, 10, 0}, 20)})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Binary: bin, DataDir: dir, Timeout: 5 * time.Minute, Profiles: profiles.Open(profilesDir),
		Models: ms, Plates: ps, Version: "test"})
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Run(ctx)
	return &harness{s: s, plates: ps, models: ms, cube: cube, dir: dir}
}

func (h *harness) plate(t *testing.T, id string, overrides map[string]string, objs ...plates.Object) plates.Plate {
	t.Helper()
	p := plates.Plate{ID: id, Name: id, Machine: testkit.Machine, Process: testkit.Process, Filament: testkit.Filament,
		Bed: "Cool Plate", Overrides: overrides, Objects: objs}
	if err := h.plates.Save(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// wait polls until the slice is done or failed.
func (h *harness) wait(t *testing.T, id string) Result {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		r, _, err := h.s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == StatusDone || r.Status == StatusError {
			return r
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("slice did not finish")
	return Result{}
}

func TestSliceRefusesObjectOffBedWithoutOrca(t *testing.T) {
	h := newHarness(t, "/nonexistent/orca", testkit.WriteProfiles(t))
	h.plate(t, "off", nil, plates.Object{ID: "o1", Model: h.cube.ID, X: 250, Y: 100, Scale: 1})
	if _, err := h.s.Enqueue("off"); err != nil {
		t.Fatal(err)
	}
	r := h.wait(t, "off")
	if r.Status != StatusError || !strings.Contains(r.Error, "not on the print bed") {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Warnings) == 0 || r.Warnings[0] != "object o1 outside bed" {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

func TestEnqueueUnknownPlate(t *testing.T) {
	h := newHarness(t, "/nonexistent/orca", testkit.WriteProfiles(t))
	if _, err := h.s.Enqueue("missing"); err != plates.ErrNotFound {
		t.Fatalf("err = %v, want plates.ErrNotFound", err)
	}
}

// TestSliceHonoursPositions slices real models with the real OrcaSlicer CLI and checks that the
// G-code's extrusion sits where the plate asked. It runs when ORCA_TEST_BIN names the AppRun
// launcher (or bin/orca-slicer) of OrcaSlicer 2.4.2.
func TestSliceHonoursPositions(t *testing.T) {
	bin, profilesDir := testkit.OrcaProfiles()
	if bin == "" {
		t.Skip("set ORCA_TEST_BIN to the OrcaSlicer AppRun to run this")
	}
	if profilesDir == "" {
		t.Fatal("ORCA_TEST_BIN set, but no resources/profiles next to it")
	}
	h := newHarness(t, bin, profilesDir)

	// 20 mm cube, off-centre: centre (120, 80)
	noBrim := map[string]string{"brim_type": "no_brim"} // the default auto brim widens the outline on both sides
	h.plate(t, "off-centre", noBrim, plates.Object{ID: "o1", Model: h.cube.ID, X: 120, Y: 80, Scale: 1})
	if _, err := h.s.Enqueue("off-centre"); err != nil {
		t.Fatal(err)
	}
	r := h.wait(t, "off-centre")
	if r.Status != StatusDone {
		t.Fatalf("slice failed: %s\n%s", r.Error, r.Log)
	}
	x0, x1, y0, y1 := extrusionExtents(t, filepath.Join(h.plates.Dir("off-centre"), "plate.gcode"))
	if math.Abs((x0+x1)/2-120) > 0.1 || math.Abs((y0+y1)/2-80) > 0.1 {
		t.Fatalf("extrusion centre = (%.2f, %.2f), want (120, 80); X %.2f..%.2f Y %.2f..%.2f", (x0+x1)/2, (y0+y1)/2, x0, x1, y0, y1)
	}
	if w := x1 - x0; w < 19 || w > 20 {
		t.Fatalf("width = %.2f, want about 19.6 (20 mm cube less the wall inset)", w)
	}
	if r.GcodeName == "" || r.EstimatedTime == 0 || r.Layers == 0 || r.FilamentG == 0 {
		t.Fatalf("result missing statistics: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.plates.Dir("off-centre"), "plate.3mf")); err != nil {
		t.Fatalf("plate.3mf missing: %v", err)
	}

	// rotated 45° about Z and scaled 1.5: footprint 42.4 mm square, centre (60, 200)
	h.plate(t, "turned", noBrim, plates.Object{ID: "o1", Model: h.cube.ID, X: 60, Y: 200, Rotation: [3]float64{0, 0, 45}, Scale: 1.5})
	if _, err := h.s.Enqueue("turned"); err != nil {
		t.Fatal(err)
	}
	r = h.wait(t, "turned")
	if r.Status != StatusDone {
		t.Fatalf("slice failed: %s\n%s", r.Error, r.Log)
	}
	x0, x1, y0, y1 = extrusionExtents(t, filepath.Join(h.plates.Dir("turned"), "plate.gcode"))
	if math.Abs((x0+x1)/2-60) > 0.1 || math.Abs((y0+y1)/2-200) > 0.1 {
		t.Fatalf("turned extrusion centre = (%.2f, %.2f), want (60, 200)", (x0+x1)/2, (y0+y1)/2)
	}
	if w := x1 - x0; w < 40 || w > 43 {
		t.Fatalf("turned width = %.2f, want about 41.5", w)
	}
}

// extrusionExtents returns the min and max X and Y of the extruding moves of a G-code file.
func extrusionExtents(t *testing.T, path string) (x0, x1, y0, y1 float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	x0, y0 = math.Inf(1), math.Inf(1)
	x1, y1 = math.Inf(-1), math.Inf(-1)
	var x, y, lastE float64
	relE := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), ";")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "M83":
			relE = true
			continue
		case "M82":
			relE = false
			continue
		case "G1", "G0":
		default:
			continue
		}
		nx, ny, ne := x, y, math.NaN()
		for _, w := range fields[1:] {
			if len(w) < 2 {
				continue
			}
			v, err := strconv.ParseFloat(w[1:], 64)
			if err != nil {
				continue
			}
			switch w[0] {
			case 'X':
				nx = v
			case 'Y':
				ny = v
			case 'E':
				ne = v
			}
		}
		extruding := false
		if !math.IsNaN(ne) {
			if relE {
				extruding = ne > 0
			} else {
				extruding = ne > lastE
				lastE = ne
			}
		}
		if extruding {
			x0, x1 = math.Min(x0, nx), math.Max(x1, nx)
			y0, y1 = math.Min(y0, ny), math.Max(y1, ny)
		}
		x, y = nx, ny
	}
	return
}

// A printer profile without multi bed types (the test machine, like the Kobra 3) slices as High
// Temp Plate whatever the plate says, like the desktop OrcaSlicer.
func TestPresetsBedFollowsPrinter(t *testing.T) {
	h := newHarness(t, "/nonexistent/orca", testkit.WriteProfiles(t))
	p := h.plate(t, "bed", nil) // Bed: "Cool Plate"
	_, process, _, err := h.s.presets(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := process["curr_bed_type"]; got != plates.SingleBed {
		t.Fatalf("curr_bed_type = %v, want %s", got, plates.SingleBed)
	}
}
