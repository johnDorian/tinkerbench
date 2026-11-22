package measure

import (
	"errors"
	"fmt"
	"math"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// Values of Options.Object.
const (
	ObjectAuto  = "auto"  // split for a scan, dark for a photo on a grid or from a camera
	ObjectDark  = "dark"  // the darkest thing on the paper (a black part on grey-dotted paper)
	ObjectLight = "light" // the lightest thing on the paper
	ObjectSplit = "split" // two-class split of light from dark: the scan path
)

// The part is found in one of two ways. A split is the original two-class Otsu threshold,
// which is right for a scan where the paper is the lightest thing. The dark path is for a
// photo: it removes the dots and lines of the paper first, then keeps the darkest class, so
// a soft shadow (lighter than the part, darker than the paper) is left out.
type plan struct {
	split bool // the two-class split of brightness
	light bool // the part is lighter than the paper (for a split: inverted)
}

// name is how the plan is reported in Result.Object.
func (p plan) name() string {
	switch {
	case p.split && p.light:
		return "split, inverted"
	case p.split:
		return "split"
	case p.light:
		return "light"
	}
	return "dark"
}

// choosePlan turns the options into a plan. It reports whether the choice was automatic,
// because only an automatic choice may be retried the other way round.
func choosePlan(o Options, camera bool) (plan, bool) {
	base := o.Object
	auto := base == ObjectAuto
	if auto {
		if o.GridMM > 0 || camera {
			base = ObjectDark
		} else {
			base = ObjectSplit
		}
	}
	// invert flips the polarity of whatever was chosen: inverted split for the scan path, a
	// light part for the dark path (ObjectLight is the same flip, spelled out).
	light := (base == ObjectLight) != o.Invert
	return plan{split: base == ObjectSplit, light: light}, auto
}

// other is the same polarity with the other method, for an automatic choice that reached
// every edge of the image.
func (p plan) other() plan { return plan{split: !p.split, light: p.light} }

// silhouette thresholds the image with plan p and returns the outline of the largest blob, the
// part, in image pixels. The other blobs that are plausibly parts of their own come back too
// (see plausibleOthers), so that they can be reported rather than silently dropped. ppm is
// millimetres-per-pixel's inverse when the scale is already known, or 0; it sizes the
// morphology in millimetres when it is.
func silhouette(img *imaging.RGB, p plan, ppm float64, o Options) (points, []imaging.Blob, error) {
	ch, err := imaging.Extract(img, o.Channel)
	if err != nil {
		return points{}, nil, err
	}
	var mask *imaging.Gray
	if p.split {
		mask = splitMask(ch, p.light, o.Threshold)
	} else {
		mask = darkMask(ch, p.light, o.Threshold, ppm)
	}

	blobs := imaging.Blobs(mask)
	if len(blobs) == 0 {
		return points{}, nil, errors.New("no part found: try invert, another channel, or crop the image closer")
	}
	b := blobs[0]
	if float64(b.Area) < minPartShare*float64(len(mask.Pix)) {
		return points{}, nil, fmt.Errorf("the largest blob covers only %.3f%% of the image: try invert, another channel, or crop the image closer",
			100*float64(b.Area)/float64(len(mask.Pix)))
	}
	if len(b.Outline) < 12 {
		return points{}, nil, errors.New("the part outline is too small to measure")
	}
	pts := points{X: make([]float64, len(b.Outline)), Y: make([]float64, len(b.Outline))}
	for i, q := range b.Outline {
		pts.X[i], pts.Y[i] = float64(q.X), float64(q.Y)
	}
	others := plausibleOthers(blobs[1:], b.Area, len(mask.Pix))
	return pts, others, nil
}

// splitMask is the scan path: a two-class threshold (Otsu unless one is given), then a
// close and an open with a 7-pixel ellipse to bridge the grid lines and drop speckle.
func splitMask(ch *imaging.Gray, light bool, threshold int) *imaging.Gray {
	blur := imaging.Blur5(ch)
	t := threshold
	if t == 0 {
		t = imaging.Otsu(blur)
	}
	mask := imaging.Threshold(blur, t, light)
	se := imaging.EllipseSE(7)
	mask = imaging.Close(mask, se)
	return imaging.Open(mask, se)
}

// darkMask is the photo path. Grey-level closing with a square a little over twice the
// width of a grid dot removes the dots and lines (they are narrower than that, and the
// part is not), then the darkest of three Otsu classes is the part. Its edges are closed
// and opened by about 0.15 mm, so a dot that survived the closing does not become a spur.
//
// Without a known scale the sizes fall back to pixels.
func darkMask(ch *imaging.Gray, light bool, threshold int, ppm float64) *imaging.Gray {
	g := ch
	if light {
		g = invertGray(ch)
	}
	closed := imaging.GreyClose(imaging.Blur5(g), dotRadius(ppm))
	t := threshold
	switch {
	case t == 0:
		t, _ = imaging.Otsu3(closed)
		t = darkEdge(closed, t)
	case light:
		t = 255 - t // the threshold was given on the lightness of the part, not the inverse
	}
	mask := imaging.Threshold(closed, t, false)
	if threshold == 0 && !light {
		// The automatic level is one level for the whole photo. A shadow on one side of the part is
		// a different level from the paper on the other, so the edge is placed at the half level
		// between the part and what is around it, not between the part and the far paper.
		mask = localMask(closed, t, ppm)
	}
	// A square rather than an ellipse of the same size: separable, so it costs a fraction of
	// the time on a 12 MP photo, and at 0.15 mm the difference is well under a pixel.
	r := openSize(ppm) / 2
	mask = imaging.SquareClose(mask, r)
	return imaging.SquareOpen(mask, r)
}

// dotRadius is the half-width of the closing window: a grid dot is about 0.1 mm across, so
// the window is 0.2 mm wide, twice that.
func dotRadius(ppm float64) int {
	if ppm <= 0 {
		return 4
	}
	return max(2, int(0.1*ppm+0.5))
}

// openSize is the ellipse that closes and opens the mask: about 0.15 mm across, odd.
func openSize(ppm float64) int {
	if ppm <= 0 {
		return 7
	}
	return max(3, 2*int(0.075*ppm+0.5)+1)
}

func invertGray(g *imaging.Gray) *imaging.Gray {
	out := imaging.NewGray(g.W, g.H)
	for i, v := range g.Pix {
		out.Pix[i] = 255 - v
	}
	return out
}

// validObject reports whether s is a value of Options.Object.
func validObject(s string) bool {
	switch s {
	case ObjectAuto, ObjectDark, ObjectLight, ObjectSplit:
		return true
	}
	return false
}

// darkEdge places the threshold between the darkest thing on the paper and the next thing up
// in brightness. t1 is where the three-class Otsu put the darkest class's upper edge. That
// edge is biased low when the two real classes are joined by a ramp of anti-aliased pixels,
// which is the usual case after a blur, so instead the threshold goes midway between the
// histogram's peaks: the darkest peak at or below t1, and the first significant peak above
// it (a shadow, or the paper). An edge between two flat tones is cut at its midpoint that way.
func darkEdge(g *imaging.Gray, t1 int) int {
	var h, s [256]float64
	for _, v := range g.Pix {
		h[v]++
	}
	for v := range 256 {
		for d := -2; d <= 2; d++ {
			if v+d >= 0 && v+d < 256 {
				s[v] += h[v+d]
			}
		}
	}
	// The darkest peak: the first local maximum from black that holds a fifth of the strongest
	// level up to t1. The strongest level there may be the shoulder of a shadow, which is not a
	// peak of its own, so it is not taken.
	var strongest float64
	for v := 0; v <= t1; v++ {
		strongest = math.Max(strongest, s[v])
	}
	m0 := t1
	for v := 0; v <= t1; v++ {
		if s[v] >= 0.2*strongest && s[v] >= s[max(v-1, 0)] && s[v] > s[v+1] {
			m0 = v
			break
		}
	}
	// The next peak must hold a quarter of the strongest level above the darkest one. Less than
	// that and it is a ramp of anti-aliased pixels, or the edge of a shadow, not a tone of its own.
	var above float64
	for v := m0 + 1; v < 256; v++ {
		above = math.Max(above, s[v])
	}
	sig := 0.25 * above
	for v := m0 + 1; v < 255; v++ {
		if s[v] >= sig && s[v] > s[v-1] && s[v] >= s[v+1] {
			return (m0 + v) / 2
		}
	}
	return t1
}

// localMask places the edge of the part at the half level between the part and the grey
// right beside that edge, pixel by pixel. The part is the median of the dark pixels. A core
// is taken at halfway from the part's level up to the global level t: it leaves out a shadow
// that is a long way above the part. The ring is the few pixels just outside the core, and
// the background is the average of the grey on the ring within about 0.1 mm. A pixel is part
// if it is darker than the midpoint of the part and that background; a pixel with no ring
// near it is part only if it is in the core. So a shadow on one side has its own level, and
// the far shadow and the paper on the other side are not pulled in.
func localMask(closed *imaging.Gray, t int, ppm float64) *imaging.Gray {
	w, h := closed.W, closed.H
	n := w * h
	var below [256]int
	nBelow := 0
	for _, v := range closed.Pix {
		if int(v) <= t {
			below[v]++
			nBelow++
		}
	}
	if nBelow == 0 {
		return imaging.Threshold(closed, t, false)
	}
	body := histMedian(below[:], nBelow)
	// The core is kept tight: a shadow whose grey lies well above the part but under the global
	// level would otherwise join the core, and the edge would be placed at the paper beyond it.
	tc := body + max(10, (t-body)/4)
	core := imaging.Threshold(closed, tc, false)

	ring, rBg := 3, 3
	if ppm > 0 {
		rBg = max(3, int(0.1*ppm+0.5))
	}
	grown := imaging.SquareDilate(core, ring)
	val := make([]float64, n)
	ind := make([]float64, n)
	for i := range n {
		if core.Pix[i] == 0 && grown.Pix[i] != 0 {
			val[i] = float64(closed.Pix[i])
			ind[i] = 1
		}
	}
	num := boxMean(val, w, h, rBg)
	den := boxMean(ind, w, h, rBg)
	out := imaging.NewGray(w, h)
	for i := range n {
		thr := float64(tc)
		if den[i] > 1e-6 {
			thr = (float64(body) + num[i]/den[i]) / 2
		}
		if float64(closed.Pix[i]) <= thr {
			out.Pix[i] = 255
		}
	}
	return out
}

// histMedian is the median of a histogram of grey levels holding n samples.
func histMedian(h []int, n int) int {
	acc := 0
	for v, c := range h {
		acc += c
		if 2*acc >= n {
			return v
		}
	}
	return 255
}
