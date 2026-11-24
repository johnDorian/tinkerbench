package measure

import (
	"math"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/mat"
)

// matPhoto draws a photo of a measuring mat seen from straight above: a darker table round the
// edge, the paper, the 1 mm grid band (lines at the widths the PDF prints) round an inner square
// of side inner mm, filled black for a black mat, and whatever part reports. Pixels are
// supersampled 3 x 3, so thin lines come out grey as they do in a photo. Coordinates in part are
// mm from the photo's top-left.
func matPhoto(ppm, inner float64, black bool, part func(x, y float64) (bool, uint8)) *imaging.RGB {
	const (
		photoMM = 40.0 // margin round the frame
		band    = 10.0
		ink     = 25.0
		paper   = 238.0
		table   = 70.0
	)
	side := inner + 2*band + 2*photoMM
	n := int(side * ppm)
	ox := photoMM // frame's outer corner, mm
	at := func(x, y float64) float64 {
		if ok, v := part(x, y); ok {
			return float64(v)
		}
		if x < 8 || y < 8 || x > side-8 || y > side-8 {
			return table
		}
		fx, fy := x-ox, y-ox
		outer := inner + 2*band
		if fx < -0.3 || fy < -0.3 || fx > outer+0.3 || fy > outer+0.3 {
			return paper
		}
		inSquare := fx > band && fx < band+inner && fy > band && fy < band+inner
		if black && fx > band+mat.BlackGapMM && fx < band+inner-mat.BlackGapMM && fy > band+mat.BlackGapMM && fy < band+inner-mat.BlackGapMM {
			return ink
		}
		width := func(k int) float64 {
			switch {
			case k == 0 || k == int(band) || k == int(band+inner) || k == int(outer):
				return 0.4
			case k%5 == 0:
				return 0.2
			}
			return 0.12
		}
		onLine := func(v float64, cross float64) bool {
			k := int(math.Round(v))
			if math.Abs(v-float64(k)) > width(k)/2 || k < 0 || k > int(outer) {
				return false
			}
			// Lines across the square stop at its edges.
			across := k > int(band) && k < int(band+inner)
			return !across || cross < band || cross > band+inner
		}
		if (onLine(fx, fy) && fy >= -0.2 && fy <= outer+0.2) || (onLine(fy, fx) && fx >= -0.2 && fx <= outer+0.2) {
			if !inSquare {
				return ink
			}
		}
		return paper
	}
	img := &imaging.RGB{W: n, H: n, Pix: make([]uint8, n*n*3)}
	for py := range n {
		for px := range n {
			var s float64
			for sy := range 3 {
				for sx := range 3 {
					s += at((float64(px)+(float64(sx)+0.5)/3)/ppm, (float64(py)+(float64(sy)+0.5)/3)/ppm)
				}
			}
			v := uint8(s/9 + 0.5)
			i := (py*n + px) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		}
	}
	return img
}

// block is a w x h mm rectangle at (x, y) mm of grey v.
func block(x, y, w, h float64, v uint8) func(float64, float64) (bool, uint8) {
	return func(px, py float64) (bool, uint8) {
		return px >= x && px < x+w && py >= y && py < y+h, v
	}
}

func both(a, b func(float64, float64) (bool, uint8)) func(float64, float64) (bool, uint8) {
	return func(x, y float64) (bool, uint8) {
		if ok, v := a(x, y); ok {
			return ok, v
		}
		return b(x, y)
	}
}

const matPPM = 8.0

// The frame's outer corner is at 40 mm, the square's at 50 mm; a 100 mm square ends at 150 mm.

func TestMatMeasuresOnlyInsideTheSquare(t *testing.T) {
	inside := block(80, 95, 30, 8, 30)
	outside := block(165, 60, 18, 70, 30) // larger than the part, on the paper beside the mat
	img := matPhoto(matPPM, 100, false, both(inside, outside))
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mat != "white" {
		t.Errorf("mat = %q, want white", res.Mat)
	}
	if math.Abs(res.OverallLengthMM-30) > 0.6 || math.Abs(res.OverallWidthMM-8) > 0.6 {
		t.Errorf("measured %.2f x %.2f mm, want the 30 x 8 part in the square", res.OverallLengthMM, res.OverallWidthMM)
	}
	if res.ObjectsFound != 1 {
		t.Errorf("objects_found = %d, want 1: the object outside the square does not count", res.ObjectsFound)
	}
}

func TestMatPartOutsideTheSquare(t *testing.T) {
	img := matPhoto(matPPM, 100, false, block(165, 60, 18, 70, 30))
	_, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want the square reported empty", err)
	}
}

func TestBlackMatLightPart(t *testing.T) {
	img := matPhoto(matPPM, 100, true, both(block(70, 90, 40, 12, 235), block(165, 60, 18, 70, 30)))
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mat != "black" || res.Object != "light" {
		t.Errorf("mat = %q, object = %q; want black, light", res.Mat, res.Object)
	}
	if math.Abs(res.OverallLengthMM-40) > 0.6 || math.Abs(res.OverallWidthMM-12) > 0.6 {
		t.Errorf("measured %.2f x %.2f mm, want the 40 x 12 part", res.OverallLengthMM, res.OverallWidthMM)
	}
}

func TestBlackMatEmpty(t *testing.T) {
	img := matPhoto(matPPM, 100, true, block(165, 60, 18, 70, 235))
	_, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want the square reported empty", err)
	}
}

func TestMatPartOverTheEdge(t *testing.T) {
	// Half in the square, half over the band: the half inside is measured, with a warning.
	img := matPhoto(matPPM, 100, false, block(130, 90, 40, 10, 30))
	res, err := Measure(img, imaging.Meta{}, Options{GridMM: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range res.Notes {
		found = found || strings.Contains(n, "reaches the edge of the mat's square")
	}
	if !found {
		t.Errorf("no note about the part crossing the square's edge: %v", res.Notes)
	}
}
