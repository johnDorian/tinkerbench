package measure

import (
	"math"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// The test part, in millimetres: a half-round tab, a body, a middle step and a tongue.
// These are the true dimensions every assertion below is checked against.
var (
	segWidth  = []float64{7.98, 11.08, 7.83, 4.61}
	segLength = []float64{8.01, 14.24, 3.10, 1.60}
	totalLen  = 26.95
	maxWidth  = 11.08
	tipDia    = 7.98
)

// inside reports whether a point of the part's own frame is part of it. x runs from the
// round tab at 0, y is across the width and centred on the axis.
func inside(x, y float64) bool {
	if x < 0 || x > totalLen {
		return false
	}
	half := segWidth[0] / 2
	at := 0.0
	for i, l := range segLength {
		if x < at+l || (i == len(segLength)-1 && x <= at+l) {
			if i == 0 && x < half { // the tab's half-round tip
				dx := x - half
				return dx*dx+y*y <= half*half
			}
			return math.Abs(y) <= segWidth[i]/2
		}
		at += l
	}
	return false
}

// renderPart draws the part at a resolution and an angle, as a dark shape on a light
// background, optionally on graph paper. Coverage is sampled rather than rasterised and
// resized, so the silhouette is exact to a fraction of a pixel.
func renderPart(dpi, angleDeg, gridMM float64, light bool) *imaging.RGB {
	const margin = 6.0
	const ss = 4
	pxPerMM := dpi / 25.4
	wmm, hmm := totalLen+2*margin, maxWidth+2*margin
	w, h := int(wmm*pxPerMM), int(hmm*pxPerMM)
	rad := angleDeg * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)

	fg, bg := uint8(25), uint8(235)
	if light {
		fg, bg = 240, 30
	}
	img := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for py := range h {
		for px := range w {
			var cover, grid float64
			for sy := range ss {
				for sx := range ss {
					// pixel -> mm about the image centre -> the part's frame
					mx := (float64(px)+(float64(sx)+0.5)/ss)/pxPerMM - wmm/2
					my := (float64(py)+(float64(sy)+0.5)/ss)/pxPerMM - hmm/2
					rx := mx*cos + my*sin + totalLen/2
					ry := -mx*sin + my*cos
					if inside(rx, ry) {
						cover++
					}
					if gridMM > 0 && onGrid(mx+wmm/2, my+hmm/2, gridMM, 1/pxPerMM) {
						grid++
					}
				}
			}
			cover /= ss * ss
			grid /= ss * ss
			v := float64(bg)*(1-cover) + float64(fg)*cover
			if cover == 0 { // graph paper shows only where the part does not cover it
				v = v*(1-grid*0.45) + 150*grid*0.45
			}
			i := (py*w + px) * 3
			c := uint8(math.Round(math.Max(math.Min(v, 255), 0)))
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = c, c, c
		}
	}
	return img
}

// onGrid reports whether a point in mm falls on a printed grid line about one pixel wide.
func onGrid(xmm, ymm, pitch, pxmm float64) bool {
	line := pxmm * 0.8
	fx := math.Abs(xmm/pitch - math.Round(xmm/pitch))
	fy := math.Abs(ymm/pitch - math.Round(ymm/pitch))
	return fx*pitch < line || fy*pitch < line
}

func close(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.3f, want %.3f +- %.2f (off by %.3f)", what, got, want, tol, got-want)
	}
}

// checkPart asserts against the true dimensions. A step's width is a median over its whole
// run and is accurate to a fraction of a pixel; its length depends on where the boundary
// between two steps is placed within the few columns the width takes to change, so it is
// allowed half again as much room.
func checkPart(t *testing.T, r *Result, tol float64) {
	t.Helper()
	lenTol := tol * 1.5
	close(t, "overall length", r.OverallLengthMM, totalLen, tol)
	close(t, "overall width", r.OverallWidthMM, maxWidth, tol)
	if len(r.Steps) != len(segWidth) {
		t.Fatalf("found %d steps, want %d: %+v", len(r.Steps), len(segWidth), r.Steps)
	}
	var sum float64
	for i, s := range r.Steps {
		close(t, "step width", s.WidthMM, segWidth[i], tol)
		close(t, "step length", s.LengthMM, segLength[i], lenTol)
		if !s.Centred {
			t.Errorf("step %d should be centred on the axis", i+1)
		}
		sum += s.LengthMM
	}
	// The steps tile the part: their lengths add up to the whole, rather than each
	// stopping short of the transition into the next.
	close(t, "steps summed", sum, r.OverallLengthMM, 0.01)
	if r.RoundEnd == nil {
		t.Fatalf("the round tab was not recognised (tip fit rms %.3f mm)", r.TipFitRMSMM)
	}
	close(t, "tip diameter", r.RoundEnd.DiameterMM, tipDia, tol)
	close(t, "tip arc centre", r.RoundEnd.CentreXMM, tipDia/2, tol)
}

// TestScan is the intended workflow: a flatbed scan, whose dpi is the scale.
func TestScan(t *testing.T) {
	for _, angle := range []float64{0, 7, -11} {
		img := renderPart(1200, angle, 0, false)
		res, err := Measure(img, imaging.Meta{DPI: 1200}, Options{Overlay: true})
		if err != nil {
			t.Fatalf("%+g deg: %v", angle, err)
		}
		close(t, "scale", res.ScaleMMPerPx, 25.4/1200, 1e-6)
		checkPart(t, res, 0.1)
		if len(res.Overlay) == 0 {
			t.Error("no overlay produced")
		}
		if res.OpenSCAD == "" {
			t.Error("no OpenSCAD block produced")
		}
	}
}

// TestGridPhoto recovers the scale from graph paper instead, which is the fallback for a
// phone photo. The angle of the paper is found with a Hough transform and its pitch from
// the spectrum of the edge profile.
func TestGridPhoto(t *testing.T) {
	for _, angle := range []float64{0, -11} {
		img := renderPart(508, angle, 1.0, false) // 508 dpi = 20 px per mm
		res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1.0})
		if err != nil {
			t.Fatalf("%+g deg: %v", angle, err)
		}
		// The grid has to give back the scale it was drawn at, to within a per cent.
		close(t, "scale", res.ScaleMMPerPx, 0.05, 0.0005)
		checkPart(t, res, 0.15)
	}
}

// TestScaleRef anchors the scale to one known dimension, for an image with neither a dpi
// nor graph paper.
func TestScaleRef(t *testing.T) {
	img := renderPart(600, 3, 0, false)
	res, err := Measure(img, imaging.Meta{}, Options{ScaleRefMM: totalLen})
	if err != nil {
		t.Fatal(err)
	}
	close(t, "overall length", res.OverallLengthMM, totalLen, 1e-3) // it was told this
	checkPart(t, res, 0.1)
}

// TestInvert handles a part lighter than its background.
func TestInvert(t *testing.T) {
	img := renderPart(600, 0, 0, true)
	// Without invert the dark background is taken for the part. That cannot be detected
	// for certain, but it reaches every edge of the image, which is worth saying.
	wrong, err := Measure(img, imaging.Meta{DPI: 600}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if wrong.OverallLengthMM < totalLen+5 {
		t.Errorf("without invert the background should have been measured, got %.2f mm", wrong.OverallLengthMM)
	}
	if len(wrong.Notes) == 0 || !strings.Contains(wrong.Notes[0], "invert") {
		t.Errorf("want a note suggesting invert, got %q", wrong.Notes)
	}

	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Invert: true})
	if err != nil {
		t.Fatal(err)
	}
	checkPart(t, res, 0.1)
}

// TestFlip forces the blunt end to lead instead of the round one.
func TestFlip(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Flip: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.RoundEnd != nil {
		t.Errorf("the tongue end is not round, but a circle was fitted to it: %+v", res.RoundEnd)
	}
	if got := res.Steps[0].WidthMM; math.Abs(got-segWidth[len(segWidth)-1]) > 0.1 {
		t.Errorf("first step width = %.2f, want the tongue's %.2f", got, segWidth[len(segWidth)-1])
	}
}

// TestNoScale refuses to report dimensions it cannot justify.
func TestNoScale(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	_, err := Measure(img, imaging.Meta{}, Options{})
	if err == nil {
		t.Fatal("want an error when nothing gives a scale")
	}
}

// TestParallax shrinks the scale of a photo, where the top face of a thick part images
// larger than its footprint.
func TestParallax(t *testing.T) {
	img := renderPart(600, 0, 0, false)
	plain, err := Measure(img, imaging.Meta{DPI: 600}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := Measure(img, imaging.Meta{DPI: 600}, Options{ThicknessMM: 3, CameraMM: 300})
	if err != nil {
		t.Fatal(err)
	}
	want := plain.OverallLengthMM * (300 - 3) / 300
	close(t, "corrected length", corrected.OverallLengthMM, want, 0.01)
	if corrected.OverallLengthMM >= plain.OverallLengthMM {
		t.Error("parallax correction should shrink the part")
	}
}

// TestBlankImage fails rather than measuring noise.
func TestBlankImage(t *testing.T) {
	img := &imaging.RGB{W: 200, H: 200, Pix: make([]uint8, 200*200*3)}
	for i := range img.Pix {
		img.Pix[i] = 235
	}
	if _, err := Measure(img, imaging.Meta{DPI: 600}, Options{}); err == nil {
		t.Fatal("want an error for an image with no part in it")
	}
}
