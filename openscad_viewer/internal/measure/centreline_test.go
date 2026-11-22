package measure

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// bentStrip draws a strip of thickMM along a circular arc of radius rMM and span radians, with a
// round knuckle of diameter knuckleMM centred on each end of the arc, at ppm pixels per mm. The
// arc is centred on the origin and symmetric about the +x axis, so its bow points along +x.
func bentStrip(ppm, rMM, span, thickMM, knuckleMM float64) *imaging.Gray {
	half := span / 2
	ends := [2][2]float64{{rMM * math.Cos(-half), rMM * math.Sin(-half)}, {rMM * math.Cos(half), rMM * math.Sin(half)}}
	const margin = 3.0
	minX, maxX := rMM, rMM
	minY, maxY := ends[0][1], ends[1][1]
	for _, e := range ends {
		minX = math.Min(minX, e[0])
		maxX = math.Max(maxX, e[0])
	}
	minX -= margin
	minY -= margin
	maxX += margin
	maxY += margin
	w := int(math.Ceil((maxX - minX) * ppm))
	h := int(math.Ceil((maxY - minY) * ppm))
	g := imaging.NewGray(w, h)
	for py := range h {
		for px := range w {
			// pixel centre in mm, y up
			x := minX + (float64(px)+0.5)/ppm
			y := maxY - (float64(py)+0.5)/ppm
			inside := false
			for _, e := range ends {
				if math.Hypot(x-e[0], y-e[1]) <= knuckleMM/2 {
					inside = true
				}
			}
			if a := math.Atan2(y, x); math.Abs(a) <= half {
				if math.Abs(math.Hypot(x, y)-rMM) <= thickMM/2 {
					inside = true
				}
			}
			if inside {
				g.Set(px, py, 255)
			}
		}
	}
	return g
}

// rectMask is a filled rectangle of w x h mm at ppm pixels per mm.
func rectMask(ppm, wMM, hMM float64) *imaging.Gray {
	w, h := int(wMM*ppm), int(hMM*ppm)
	g := imaging.NewGray(w+20, h+20)
	for y := 10; y < 10+h; y++ {
		for x := 10; x < 10+w; x++ {
			g.Set(x, y, 255)
		}
	}
	return g
}

// discMask is a filled disc of diameter dMM at ppm pixels per mm.
func discMask(ppm, dMM float64) *imaging.Gray {
	r := dMM / 2 * ppm
	n := int(2*r) + 20
	g := imaging.NewGray(n, n)
	c := float64(n) / 2
	for y := range n {
		for x := range n {
			if math.Hypot(float64(x)+0.5-c, float64(y)+0.5-c) <= r {
				g.Set(x, y, 255)
			}
		}
	}
	return g
}

// TestCentrelineArcStrip is the bent spring strip: a 40 mm radius arc over 60 degrees, 1.1 mm
// thick, with a 3.5 mm knuckle at each end, at 20 px/mm. Its arc length, thickness, sag and
// knuckles are known analytically, and the measured values must match them.
func TestCentrelineArcStrip(t *testing.T) {
	const (
		ppm     = 20.0
		rMM     = 40.0
		thickMM = 1.1
		knuckle = 3.5
		spanRad = math.Pi / 3
	)
	mask := bentStrip(ppm, rMM, spanRad, thickMM, knuckle)
	c, ok := centreline(mask, 1/ppm)
	if !ok {
		t.Fatal("bent strip not reported as a curved centreline")
	}

	wantLen := rMM * spanRad
	wantSag := rMM * (1 - math.Cos(spanRad/2))
	wantChord := 2 * rMM * math.Sin(spanRad/2)
	t.Logf("length %.3f (want %.3f), chord %.3f (want %.3f), sag %.3f (want %.3f), median thickness %.3f",
		c.LengthMM, wantLen, c.ChordMM, wantChord, c.SagMM, wantSag, c.MedianThicknessMM)

	if math.Abs(c.LengthMM-wantLen) > 0.05*wantLen {
		t.Errorf("length = %.3f, want %.3f +- 5%%", c.LengthMM, wantLen)
	}
	if math.Abs(c.ChordMM-wantChord) > 0.05*wantChord {
		t.Errorf("chord = %.3f, want %.3f +- 5%%", c.ChordMM, wantChord)
	}
	if math.Abs(c.MedianThicknessMM-thickMM) > 0.15*thickMM {
		t.Errorf("median thickness = %.3f, want %.2f +- 15%%", c.MedianThicknessMM, thickMM)
	}
	if math.Abs(c.SagMM-wantSag) > 0.10*wantSag {
		t.Errorf("sag = %.3f, want %.3f +- 10%%", c.SagMM, wantSag)
	}

	// The frame: first point at the origin, last point on the +x axis at the chord length.
	if p := c.Points[0]; math.Abs(p[0]) > 1e-9 || math.Abs(p[1]) > 1e-9 {
		t.Errorf("first point = %v, want the origin", p)
	}
	last := c.Points[len(c.Points)-1]
	if math.Abs(last[0]-c.ChordMM) > 0.05 || math.Abs(last[1]) > 0.05 {
		t.Errorf("last point = %v, want (%.3f, 0) on the chord", last, c.ChordMM)
	}
	if n := len(c.Points); n < clMinPts || n > clMaxPts {
		t.Errorf("%d points, want %d to %d", n, clMinPts, clMaxPts)
	}
	if len(c.ThicknessMM) != len(c.Points) {
		t.Errorf("%d thickness values for %d points", len(c.ThicknessMM), len(c.Points))
	}

	if len(c.Ends) != 2 {
		t.Fatalf("found %d knuckles, want 2: %+v", len(c.Ends), c.Ends)
	}
	for _, e := range c.Ends {
		t.Logf("knuckle %s: centre %v, diameter %.3f, rms %.4f", e.At, e.CentreMM, e.DiameterMM, e.RMSMM)
		if math.Abs(e.DiameterMM-knuckle) > 0.10*knuckle {
			t.Errorf("knuckle %s diameter = %.3f, want %.2f +- 10%%", e.At, e.DiameterMM, knuckle)
		}
	}
	if c.Ends[0].At != "start" || c.Ends[1].At != "end" {
		t.Errorf("knuckles are %q and %q, want start and end", c.Ends[0].At, c.Ends[1].At)
	}

	// The OpenSCAD block carries the same numbers.
	for _, want := range []string{"centreline = [[0.000,0.000],", "strip_thickness = ", "knuckle_start = [", "knuckle_end = ["} {
		if !strings.Contains(c.OpenSCAD, want) {
			t.Errorf("OpenSCAD block lacks %q:\n%s", want, c.OpenSCAD)
		}
	}

	// The pixel path is for the overlay: one pixel point per point, and it lies on the part.
	px := c.PixelPath()
	if len(px) != len(c.Points) {
		t.Fatalf("pixel path has %d points, want %d", len(px), len(c.Points))
	}
	for i, p := range px {
		x, y := int(math.Round(p[0])), int(math.Round(p[1]))
		if !mask.In(x, y) || mask.At(x, y) == 0 {
			t.Errorf("pixel path point %d = %v is off the part", i, p)
			break
		}
	}
	cx, cy, r := c.Ends[0].PixelCircle()
	if !mask.In(int(cx), int(cy)) || math.Abs(2*r/ppm-c.Ends[0].DiameterMM) > 0.01*knuckle {
		t.Errorf("knuckle pixel circle (%.1f, %.1f, r %.1f) does not match the mm knuckle", cx, cy, r)
	}
}

// A straight bar is long and thin but does not bow, so it is not a curved centreline.
func TestCentrelineStraightBarIsNotCurved(t *testing.T) {
	const ppm = 20.0
	if c, ok := centreline(rectMask(ppm, 60, 1.1), 1/ppm); ok {
		t.Errorf("straight bar reported as curved: length %.2f sag %.3f", c.LengthMM, c.SagMM)
	}
}

// A fat blob is round, not long: it fails the aspect test, and so does any blob that is not
// long for its thickness.
func TestCentrelineDiscIsNotCurved(t *testing.T) {
	const ppm = 20.0
	if c, ok := centreline(discMask(ppm, 20), 1/ppm); ok {
		t.Errorf("disc reported as curved: length %.2f median %.2f", c.LengthMM, c.MedianThicknessMM)
	}
}

// A strip that bows only a little (sag under a few thicknesses) stays a straight part.
func TestCentrelineSlightBowIsNotCurved(t *testing.T) {
	const ppm = 20.0
	// 400 mm radius over 6 degrees: sag = R(1-cos(3deg)) = 0.12 mm, well under 3 thicknesses.
	mask := bentStrip(ppm, 400, 6*math.Pi/180, 1.1, 0)
	if c, ok := centreline(mask, 1/ppm); ok {
		t.Errorf("nearly straight strip reported as curved: sag %.3f", c.SagMM)
	}
}

// TestCentrelineNoKnuckle checks that a bent strip with plain ends reports no knuckles.
func TestCentrelineNoKnuckle(t *testing.T) {
	const ppm = 20.0
	c, ok := centreline(bentStrip(ppm, 40, math.Pi/3, 1.1, 1.1), 1/ppm)
	if !ok {
		t.Fatal("bent strip not reported as curved")
	}
	if len(c.Ends) != 0 {
		t.Errorf("plain ends reported as %d knuckles: %+v", len(c.Ends), c.Ends)
	}
}

// TestCentrelineRealPhoto measures the bent strip in IMG_0882.jpg. It needs the photo, so it is
// skipped unless MEASURE_TEST_DATA names its folder. The strip is seen edge-on at about 80 px/mm.
// The part was broken in two and the photo shows one piece, so the length is that piece (about
// 22.5 mm along the hand trace in measure-notes/fit_photo_0882.py), not the 47 mm whole part. The
// piece has one knuckle, at the end away from the break. The bounds are loose: the numbers come
// from a phone photo.
func TestCentrelineRealPhoto(t *testing.T) {
	dir := os.Getenv("MEASURE_TEST_DATA")
	if dir == "" {
		t.Skip("set MEASURE_TEST_DATA to the folder that holds IMG_0882.jpg to run this")
	}
	img, meta, err := imaging.Load(filepath.Join(dir, "IMG_0882.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	o := Options{GridMM: 1}
	o.defaults()
	pl, _ := choosePlan(o, meta.Camera)
	const pxPerMM = 80.0
	outline, _, err := silhouette(img, pl, pxPerMM, o)
	if err != nil {
		t.Fatal(err)
	}
	mask := fillOutline(outline, img.W, img.H)
	c, ok := centreline(mask, 1/pxPerMM)
	if !ok {
		t.Fatal("the photographed strip is not reported as a curved centreline")
	}
	t.Logf("length %.2f mm, chord %.2f mm, sag %.2f mm, median thickness %.3f mm, %d points",
		c.LengthMM, c.ChordMM, c.SagMM, c.MedianThicknessMM, len(c.Points))
	for _, e := range c.Ends {
		t.Logf("knuckle %s: centre %v, diameter %.2f mm, rms %.4f mm", e.At, e.CentreMM, e.DiameterMM, e.RMSMM)
	}
	if c.LengthMM < 20 || c.LengthMM > 26 {
		t.Errorf("length = %.2f mm, want 20 to 26 (the visible piece)", c.LengthMM)
	}
	if len(c.Ends) != 1 || c.Ends[0].At != "end" {
		t.Errorf("knuckles = %+v, want one at the end away from the break", c.Ends)
	}
	if c.MedianThicknessMM < 0.8 || c.MedianThicknessMM > 1.5 {
		t.Errorf("median thickness = %.3f mm, want 0.8 to 1.5", c.MedianThicknessMM)
	}
}
