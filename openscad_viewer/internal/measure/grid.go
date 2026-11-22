package measure

import (
	"errors"
	"fmt"
	"math"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// gridScale is the scale a piece of graph paper gives.
type gridScale struct {
	SpreadPct              float64                    // how much the pitch varies across the photo, relative to its median, in per cent
	PxPerMMMin, PxPerMMMax float64                    // the least and the most pixels per millimetre across the photo
	MMPerPx                float64                    // at the photo's typical place
	MMPerPxAt              func(x, y float64) float64 // at a point, in full-resolution pixels
	Method                 string                     // "Hough" or "dotted (projection)"
	Note                   string
}

// scaleFromGrid recovers millimetres per pixel from graph paper. It first tries the solid
// lines of the paper (a Hough transform for the angle, the spectrum of the edge profile for
// the pitch); when there are no solid lines, which is how a lot of graph paper is printed
// now, it falls back to the dots or dashes between the grid points (dottedPitch). Either way
// the grid is the scale, so a photo needs nothing else.
func scaleFromGrid(gray *imaging.Gray, gridMM float64) (gridScale, error) {
	gs, err := houghScale(gray, gridMM)
	if err == nil {
		return gs, nil
	}
	return dottedScale(gray, gridMM, err)
}

// houghScale is the solid-line method. Any reason it gives up (too few lines, no pitch, the
// two axes disagreeing) is an error, and the caller falls back to the dots.
//
// The full-resolution pass is only made when a half-resolution pass already finds lines whose
// two axes agree: the solid lines are as long at half resolution, and a photo of dots makes
// Hough lines along its dotted rows that do not agree, which is what the cheap pass rejects.
func houghScale(gray *imaging.Gray, gridMM float64) (gridScale, error) {
	// Only disagreeing axes stop it here: a small grid may simply have too few lines at half
	// resolution, and then the full pass decides. The gate is looser (15 per cent) than the
	// refusal (5 per cent): dots disagree by a factor of several.
	// It is only made on a large photo, where it saves time: at half resolution a small grid
	// has periods of a few pixels, too short to read its axes, so there the full pass decides.
	if gray.W*gray.H > 4<<20 {
		if _, _, err := houghPitch(imaging.Canny(halve(gray), 40, 120), 0.15); errors.Is(err, errAxes) {
			return gridScale{}, err
		}
	}
	ang, pitch, err := houghPitch(imaging.Canny(gray, 40, 120), 0.05)
	if err != nil {
		return gridScale{}, err
	}
	mm := gridMM / pitch
	return gridScale{
		MMPerPx:    mm,
		MMPerPxAt:  func(x, y float64) float64 { return mm },
		Method:     "Hough",
		Note:       fmt.Sprintf("grid: %+.2f deg, %.2f px per %g mm cell", ang, pitch, gridMM),
		PxPerMMMin: pitch / gridMM,
		PxPerMMMax: pitch / gridMM,
	}, nil
}

// errAxes is the Hough refusal: the two axes of the lines do not agree.
var errAxes = errors.New("the lines' two axes disagree")

// houghPitch is the angle of the lines in edges and their pitch in pixels, from the Hough
// transform and the spectrum of the rotated edges. The two axes have to agree.
func houghPitch(edges *imaging.Gray, tol float64) (ang, pitch float64, err error) {
	ang, err = gridAngle(edges)
	if err != nil {
		return 0, 0, err
	}
	rot := imaging.RotateNearest(edges, ang)

	var pitches []float64
	for _, p := range []float64{pitchByFFT(imaging.ColSums(rot), 4), pitchByFFT(imaging.RowSums(rot), 4)} {
		if p > 0 {
			pitches = append(pitches, p)
		}
	}
	if len(pitches) == 0 {
		return 0, 0, errors.New("the lines gave no repeating pitch")
	}
	// Square paper, so the two axes have to agree. If they do not, the scale is a guess
	// and every dimension would be wrong by that one factor, so refuse rather than return
	// a plausible-looking number.
	if len(pitches) == 2 && math.Abs(pitches[0]-pitches[1]) > tol*math.Max(pitches[0], pitches[1]) {
		return 0, 0, fmt.Errorf("%w (%.1f px vs %.1f px)", errAxes, pitches[0], pitches[1])
	}
	return ang, median(pitches), nil
}

// dottedScale is the fallback for paper whose grid is dots or dashes, not lines. hough is
// why the line method gave up, and is only reported if the dots fail as well.
func dottedScale(gray *imaging.Gray, gridMM float64, hough error) (gridScale, error) {
	dg, err := dottedPitch(gray)
	if err != nil {
		return gridScale{}, fmt.Errorf("grid: no solid lines (%v) and no dotted pattern either (%v); pass dpi or scale_ref_mm instead", hough, err)
	}
	note := fmt.Sprintf("grid: dots, not solid lines: %+.2f deg, %.2f px per %g mm cell (median of %d blocks)",
		dg.AngleDeg, dg.PitchPx, gridMM, dg.Blocks)
	typical := gridMM / dg.PitchPx
	at := func(x, y float64) float64 { return gridMM / dg.PitchAt(x, y) }
	return gridScale{
		MMPerPx:    typical,
		MMPerPxAt:  at,
		Method:     "dotted (projection)",
		Note:       note,
		SpreadPct:  100 * dg.SpreadFrac,
		PxPerMMMin: dg.PitchMinPx / gridMM,
		PxPerMMMax: dg.PitchMaxPx / gridMM,
	}, nil
}

// maxHoughEdges bounds the edge pixels that vote in the Hough transform.
const maxHoughEdges = 60000

// gridAngle is the rotation of the graph paper in degrees, from a Hough transform.
//
// A square grid has two line families 90 degrees apart, so the angle is only defined
// modulo 90. Multiplying by 4 maps that onto a full circle, where the peak can be found
// without a wraparound seam: folding onto [0, 90) instead would split an axis-aligned grid
// between 0 and 89 and let a spurious direction win.
//
// Only the angle comes from here. The pitch does not, because Canny finds two edges per
// printed line and the line-to-line spacing then collapses to the width of a single line.
func gridAngle(edges *imaging.Gray) (float64, error) {
	// A grid line runs the width of the image, so it earns votes in proportion to the
	// image size. A low fixed threshold instead returns tens of thousands of lines, and
	// the diagonals through the lattice intersections drown out the real grid.
	// Every edge votes at every angle, so a photo of dots (every dot has an edge) would cost
	// millions of votes. A line is many votes, so an evenly thinned set of edges finds the same
	// lines at a bounded cost.
	count := 0
	for _, v := range edges.Pix {
		if v != 0 {
			count++
		}
	}
	thin := max(1, (count+maxHoughEdges-1)/maxHoughEdges)
	sparse := edges
	if thin > 1 {
		sparse = imaging.NewGray(edges.W, edges.H)
		k := 0
		for i, v := range edges.Pix {
			if v != 0 {
				if k%thin == 0 {
					sparse.Pix[i] = 255
				}
				k++
			}
		}
	}
	threshold := int(0.25 * float64(max(edges.W, edges.H)) / float64(thin))
	lines := imaging.HoughLines(sparse, 1, math.Pi/720, threshold, 300)
	if len(lines) < 8 {
		return 0, fmt.Errorf("grid: only %d strong lines found, need 8", len(lines))
	}

	quad := make([]float64, len(lines))
	for i, l := range lines {
		quad[i] = math.Mod(4*l.Theta, 2*math.Pi) // 90 degrees -> a full circle
	}
	const bins = 360
	var hist [bins]int
	for _, q := range quad {
		b := min(int(q/(2*math.Pi)*bins), bins-1)
		hist[b]++
	}
	peak := 0
	for i, c := range hist {
		if c > hist[peak] {
			peak = i
		}
	}
	centre := (float64(peak) + 0.5) * 2 * math.Pi / bins

	// circular mean of the lines within 8 degrees of the peak
	var sre, sim float64
	n := 0
	for _, q := range quad {
		if math.Abs(wrapPi(q-centre)) < 8*math.Pi/180 {
			sre += math.Cos(q)
			sim += math.Sin(q)
			n++
		}
	}
	if n == 0 {
		for _, q := range quad {
			sre += math.Cos(q)
			sim += math.Sin(q)
		}
	}
	return math.Atan2(sim, sre) / 4 * 180 / math.Pi, nil
}

// wrapPi folds an angle onto (-pi, pi].
func wrapPi(a float64) float64 { return math.Atan2(math.Sin(a), math.Cos(a)) }
