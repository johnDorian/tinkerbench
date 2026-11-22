package measure

import (
	"math"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// dottedPaper draws graph paper with 1 mm squares drawn as dotted lines, at pitchPx pixels
// per cell, rotated by angleDeg, with a dot every dotPx pixels along each line. The dots are
// soft (a Gaussian of sigma 0.8 px) and grey on white, like a printed dotted grid. The result
// is a w x h RGB image.
func dottedPaper(w, h int, pitchPx, dotPx, angleDeg float64) *imaging.RGB {
	dark := make([]float64, w*h)
	rad := angleDeg * math.Pi / 180
	c, s := math.Cos(rad), math.Sin(rad)
	// A dot is drawn at each point of the lattice of dot positions along both families of
	// lines. Work in the paper's frame (u along the rows, v across them).
	for v := -pitchPx; v < float64(w+h)+pitchPx; v += dotPx {
		for u := -pitchPx; u < float64(w+h)+pitchPx; u += dotPx {
			// the dot at (u, v) in the paper's frame is on a line when either coordinate sits
			// on a multiple of the pitch
			onLine := math.Abs(math.Remainder(u, pitchPx)) < 0.5 || math.Abs(math.Remainder(v, pitchPx)) < 0.5
			if !onLine {
				continue
			}
			// back to image coordinates
			x := u*c - v*s
			y := u*s + v*c
			stamp(dark, w, h, x, y, 110)
		}
	}
	img := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for i, d := range dark {
		g := uint8(math.Max(0, math.Min(255, 235-d)))
		img.Pix[i*3], img.Pix[i*3+1], img.Pix[i*3+2] = g, g, g
	}
	return img
}

// stamp adds a soft dot of the given depth at (x, y) to dark.
func stamp(dark []float64, w, h int, x, y, depth float64) {
	const r = 2
	cx, cy := int(math.Round(x)), int(math.Round(y))
	for py := cy - r; py <= cy+r; py++ {
		for px := cx - r; px <= cx+r; px++ {
			if px < 0 || py < 0 || px >= w || py >= h {
				continue
			}
			dx, dy := float64(px)-x, float64(py)-y
			dark[py*w+px] += depth * math.Exp(-(dx*dx+dy*dy)/(2*0.8*0.8))
		}
	}
}

// TestDottedPitchSynthetic finds the grid of a dotted photo. The cells are 40 to 80 pixels, as in a
// phone photo at 40-80 px/mm, with a dot every quarter cell (0.25 mm).
func TestDottedPitchSynthetic(t *testing.T) {
	for _, tc := range []struct {
		pitch, angle float64
	}{{80, 0}, {80, 3}, {60, -7}} {
		img := dottedPaper(1600, 1200, tc.pitch, tc.pitch/4, tc.angle)
		gray, _ := imaging.Extract(img, imaging.ChannelGray)
		dg, err := dottedPitch(gray)
		if err != nil {
			t.Fatalf("pitch %v angle %v: %v", tc.pitch, tc.angle, err)
		}
		t.Logf("pitch %v angle %v: got %.3f angle %.2f spread %.4f blocks %d", tc.pitch, tc.angle, dg.PitchPx, dg.AngleDeg, dg.SpreadFrac, dg.Blocks)
		close(t, "dotted pitch", dg.PitchPx, tc.pitch, 0.25)
		close(t, "dotted angle", dg.AngleDeg, tc.angle, 1)
	}
}
