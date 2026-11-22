package measure

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// TestRealPhotos measures the photos of a real part on graph paper. It needs the photos, which
// are not in the repository: MEASURE_TEST_DATA names the folder that holds IMG_0882.jpg,
// IMG_0880.jpeg and IMG_0878.jpeg, with the crops in its measure-notes subfolder. Without it the
// test is skipped.
//
// The expected values are the caliper and grid readings of the field report, with the tolerances
// they were asked for. They are scale checks on a phone photo, where the paper is not flat, so
// the scale is read where the part is.
func TestRealPhotos(t *testing.T) {
	dir := os.Getenv("MEASURE_TEST_DATA")
	if dir == "" {
		t.Skip("set MEASURE_TEST_DATA to the folder of the real photos to run this")
	}
	load := func(name string) (*imaging.RGB, imaging.Meta) {
		t.Helper()
		img, meta, err := imaging.Load(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return img, meta
	}

	t.Run("IMG_0882 grid", func(t *testing.T) {
		img, meta := load("IMG_0882.jpg")
		res, err := Measure(img, meta, Options{GridMM: 1})
		if err != nil {
			t.Fatal(err)
		}
		pxPerMM := 1 / res.ScaleMMPerPx
		t.Logf("px/mm %.2f (%s), spread %.1f%% (%.2f to %.2f), object %s, width %.2f mm", pxPerMM, res.ScaleFrom, res.GridSpreadPct, res.GridPxPerMMMin, res.GridPxPerMMMax, res.Object, res.OverallWidthMM)
		if math.Abs(pxPerMM-81.5) > 0.02*81.5 {
			t.Errorf("px/mm = %.2f, want 81.5 +- 2%%", pxPerMM)
		}
	})

	t.Run("IMG_0880 grid", func(t *testing.T) {
		img, meta := load("IMG_0880.jpeg")
		res, err := Measure(img, meta, Options{GridMM: 1, Crop: []int{1750, 1180, 2300, 2420}, Object: "dark"})
		if err != nil {
			t.Fatal(err)
		}
		pxPerMM := 1 / res.ScaleMMPerPx
		t.Logf("px/mm %.2f (%s), spread %.1f%% (%.2f to %.2f)", pxPerMM, res.ScaleFrom, res.GridSpreadPct, res.GridPxPerMMMin, res.GridPxPerMMMax)
		if math.Abs(pxPerMM-43.5) > 0.02*43.5 {
			t.Errorf("px/mm = %.2f, want 43.5 +- 2%%", pxPerMM)
		}
	})

	t.Run("IMG_0878 grid", func(t *testing.T) {
		img, meta := load("IMG_0878.jpeg")
		res, err := Measure(img, meta, Options{GridMM: 1})
		if err != nil {
			t.Fatal(err)
		}
		pxPerMM := 1 / res.ScaleMMPerPx
		t.Logf("px/mm %.2f (%s), spread %.1f%% (%.2f to %.2f)", pxPerMM, res.ScaleFrom, res.GridSpreadPct, res.GridPxPerMMMin, res.GridPxPerMMMax)
		if math.Abs(pxPerMM-58.6) > 0.02*58.6 {
			t.Errorf("px/mm = %.2f, want 58.6 +- 2%%", pxPerMM)
		}
	})

	t.Run("top piece body width", func(t *testing.T) {
		img, meta := load(filepath.Join("measure-notes", "IMG_0880_top_piece.jpg"))
		res, err := Measure(img, meta, Options{DPI: 1105, Object: "dark"})
		if err != nil {
			t.Fatal(err)
		}
		// The body is the run of steps between the flare at each end: the widths that are
		// narrower than the flare and longer than a millimetre.
		var widths []float64
		for _, s := range res.Steps {
			if s.LengthMM > 1 && s.WidthMM < 7 {
				widths = append(widths, s.WidthMM)
			}
		}
		if len(widths) == 0 {
			t.Fatalf("no body steps found: %+v", res.Steps)
		}
		sort.Float64s(widths)
		body := widths[len(widths)/2]
		t.Logf("body width %.2f mm from %v", body, widths)
		if math.Abs(body-5.5) > 0.15 {
			t.Errorf("body width = %.2f, want 5.5 +- 0.15", body)
		}
	})

	t.Run("knuckle collar fit", func(t *testing.T) {
		img, meta := load(filepath.Join("measure-notes", "IMG_0882_knuckle.jpg"))
		res, err := Measure(img, meta, Options{DPI: 2070})
		if err != nil {
			t.Fatal(err)
		}
		if res.RoundEnd == nil {
			t.Fatalf("no circle fitted (tip fit rms %.3f mm)", res.TipFitRMSMM)
		}
		t.Logf("circle diameter %.3f mm, rms %.4f mm", res.RoundEnd.DiameterMM, res.RoundEnd.FitRMSMM)
		if res.RoundEnd.FitRMSMM > 0.05 {
			t.Errorf("fit rms = %.4f, want under 0.05", res.RoundEnd.FitRMSMM)
		}
	})
}
