package measure

import (
	"math"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// whiteRGB is a w x h white image; paint sets the pixels where in reports true to dark grey.
func whiteRGB(w, h int, in func(x, y int) bool) *imaging.RGB {
	img := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for y := range h {
		for x := range w {
			v := uint8(245)
			if in(x, y) {
				v = 20
			}
			i := (y*w + x) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		}
	}
	return img
}

// TestFrameIsSkipped: a measuring mat's heavy border is a hollow square larger than the part
// inside it. The part must be measured, not the frame, and a note must say so.
func TestFrameIsSkipped(t *testing.T) {
	const ppm = 10.0 // px per mm, i.e. 254 dpi
	// frame: 100 mm square of 1 mm lines (larger than the part) at (10, 10) mm; part: 30 x 8 mm block in the middle
	inFrame := func(x, y int) bool {
		fx, fy := float64(x)/ppm-10, float64(y)/ppm-10
		if fx < 0 || fy < 0 || fx > 100 || fy > 100 {
			return false
		}
		return fx < 1 || fy < 1 || fx > 99 || fy > 99
	}
	inPart := func(x, y int) bool {
		fx, fy := float64(x)/ppm, float64(y)/ppm
		return fx > 45 && fx < 75 && fy > 56 && fy < 64
	}
	img := whiteRGB(1200, 1200, func(x, y int) bool { return inFrame(x, y) || inPart(x, y) })
	res, err := Measure(img, imaging.Meta{}, Options{DPI: 25.4 * ppm, Object: ObjectDark})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.OverallLengthMM-30) > 0.5 || math.Abs(res.OverallWidthMM-8) > 0.5 {
		t.Errorf("measured %.2f x %.2f mm, want the 30 x 8 part, not the frame", res.OverallLengthMM, res.OverallWidthMM)
	}
	if res.ObjectsFound != 1 {
		t.Errorf("objects_found = %d, want 1: the frame is not an object", res.ObjectsFound)
	}
	found := false
	for _, n := range res.Notes {
		found = found || len(n) > 7 && n[:7] == "A frame"
	}
	if !found {
		t.Errorf("no note about the frame: %v", res.Notes)
	}
}

// TestRingPartIsNotAFrame: a ring-shaped part (an O-ring) with nothing inside it is the part.
func TestRingPartIsNotAFrame(t *testing.T) {
	const ppm = 10.0
	img := whiteRGB(600, 600, func(x, y int) bool {
		r := math.Hypot(float64(x)/ppm-30, float64(y)/ppm-30)
		return r > 13 && r < 15
	})
	res, err := Measure(img, imaging.Meta{}, Options{DPI: 25.4 * ppm, Object: ObjectDark})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.OverallLengthMM-30) > 1 {
		t.Errorf("ring measured %.2f mm across, want 30", res.OverallLengthMM)
	}
}
