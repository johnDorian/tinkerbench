package measure

import (
	"math"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// barOnPaper is a black bar on dotted paper with a soft shadow falling down and to the right,
// the way a part looks on graph paper in a photo. The grid is 60 px per mm; the bar and the
// shadow are placed in pixels.
type rect struct{ x0, y0, x1, y1 int }

func barOnPaper(w, h int, bars ...rect) *imaging.RGB {
	return barOnPaperShade(w, h, 0.4, bars...)
}

// barOnPaperShade is barOnPaper with the depth of the shadow given: shade is the paper's
// brightness under the darkest part of it (0.4 means the paper drops to 60%).
func barOnPaperShade(w, h int, shade float64, bars ...rect) *imaging.RGB {
	img := dottedPaper(w, h, 60, 15, 3)
	const shx, shy, reach = 30, 30, 40.0
	for y := range h {
		for x := range w {
			i := (y*w + x) * 3
			// the shadow: the bar moved by (shx, shy), darkening the paper within reach of it
			level := 1.0
			for _, b := range bars {
				d := distToRect(float64(x), float64(y), float64(b.x0+shx), float64(b.y0+shy), float64(b.x1+shx), float64(b.y1+shy))
				if s := 1 - d/reach; s > 0 {
					level = math.Min(level, 1-shade*s)
				}
			}
			for k := range 3 {
				img.Pix[i+k] = uint8(float64(img.Pix[i+k]) * level)
			}
			for _, b := range bars {
				if x >= b.x0 && x < b.x1 && y >= b.y0 && y < b.y1 {
					img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 25, 25, 25
				}
			}
		}
	}
	return img
}

// distToRect is the distance from (x, y) to the rectangle [x0, x1) x [y0, y1), 0 inside.
func distToRect(x, y float64, x0, y0, x1, y1 float64) float64 {
	dx := math.Max(math.Max(x0-x, 0), x-x1)
	dy := math.Max(math.Max(y0-y, 0), y-y1)
	return math.Hypot(dx, dy)
}

// TestDarkSegmentation finds a black bar on dotted paper with a shadow beside it: the dots
// are removed, the shadow is left out, and the bar's width and length come back. A split (the
// scan path) takes the shadow, which is why auto picks dark for a grid.
func TestDarkSegmentation(t *testing.T) {
	img := barOnPaper(1600, 1000, rect{600, 120, 930, 840})
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1, Object: "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Object != "dark" {
		t.Errorf("object = %q, want dark", res.Object)
	}
	close(t, "scale", 1/res.ScaleMMPerPx, 60, 0.6)
	close(t, "bar width", res.OverallWidthMM, 330.0/60, 0.1)
	close(t, "bar length", res.OverallLengthMM, 720.0/60, 0.15)
}

// TestCropMeasuresInside measures only the part inside the crop: the second bar is outside it
// and must not change the answer, while the grid is still read from the whole photo.
func TestCropMeasuresInside(t *testing.T) {
	img := barOnPaper(1600, 1000, rect{300, 120, 630, 840}, rect{1100, 120, 1430, 840})
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1, Object: "dark", Crop: []int{200, 0, 800, 1000}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Crop) != 4 {
		t.Errorf("crop not reported: %v", res.Crop)
	}
	close(t, "bar width inside the crop", res.OverallWidthMM, 330.0/60, 0.1)
	close(t, "bar length inside the crop", res.OverallLengthMM, 720.0/60, 0.15)
	if res.OverallWidthMM > 6 {
		t.Errorf("the second bar was measured: width %.2f", res.OverallWidthMM)
	}
}

// TestCameraDPIRefused: a phone's recorded dpi is a default, so without a grid, a scale_ref or
// an explicit dpi the measurement says why it cannot scale. An explicit dpi wins over it.
func TestCameraDPIRefused(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	camera := imaging.Meta{DPI: 72, Make: "Apple", Model: "iPhone 16e", Camera: true}
	_, err := Measure(img, camera, Options{})
	if err == nil || !strings.Contains(err.Error(), "camera photo") || !strings.Contains(err.Error(), "pass grid_mm, scale_ref_mm or dpi") {
		t.Fatalf("error = %v, want the camera refusal", err)
	}
	res, err := Measure(img, camera, Options{DPI: 600})
	if err != nil {
		t.Fatalf("explicit dpi: %v", err)
	}
	close(t, "scale with explicit dpi", res.ScaleMMPerPx, 25.4/600, 1e-6)
}

// TestParallaxNote asks for the camera distance when the scale came from the paper.
func TestParallaxNote(t *testing.T) {
	img := renderPart(508, 0, 1.0, false)
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "thickness_mm") && strings.Contains(n, "camera_mm") {
			found = true
		}
	}
	if !found {
		t.Errorf("no parallax note for a grid scale without camera_mm: %q", res.Notes)
	}
	withCamera, err := Measure(img, imaging.Meta{}, Options{GridMM: 1, CameraMM: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range withCamera.Notes {
		if strings.Contains(n, "thickness_mm") {
			t.Errorf("the parallax note should go once camera_mm is given: %q", n)
		}
	}
}

// TestOptionValidation refuses options that cannot work.
func TestOptionValidation(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	for name, o := range map[string]Options{
		"object":      {DPI: 600, Object: "blue"},
		"threshold":   {DPI: 600, Threshold: 300},
		"crop length": {DPI: 600, Crop: []int{1, 2, 3}},
		"crop size":   {DPI: 600, Crop: []int{0, 0, 8, 8}},
		"crop bounds": {DPI: 600, Crop: []int{0, 0, 100000, 100000}},
	} {
		if _, err := Measure(img, imaging.Meta{}, o); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// TestObjectSplitIsTheScanPath: object split on a scan is the original two-class threshold,
// which measures the part exactly as the scan tests do; the invert flag flips it.
func TestObjectSplitIsTheScanPath(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Object: "split"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Object != "split" {
		t.Errorf("object = %q, want split", res.Object)
	}
	checkPart(t, res, 0.1)
}

// TestDarkShadowOneSide: a deep shadow falls on one side of the bar, so its grey sits just
// inside a single global threshold and would widen the bar. The edge is taken at the half level
// between the bar and the grey around it, so the bar comes back at its own width.
func TestDarkShadowOneSide(t *testing.T) {
	img := barOnPaperShade(1600, 1000, 0.6, rect{600, 120, 930, 840})
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1, Object: "dark"})
	if err != nil {
		t.Fatal(err)
	}
	close(t, "bar width with a deep shadow", res.OverallWidthMM, 330.0/60, 0.15)
	close(t, "bar length with a deep shadow", res.OverallLengthMM, 720.0/60, 0.2)
}
