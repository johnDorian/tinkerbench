package measure

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// A photo on a measuring mat is measured inside the mat's square only. Anything outside it (the
// grid band, the text, the table, another part lying beside the mat) is not the part, so the
// square is found first and everything outside it is painted over with the square's own colour.
// The square's colour also says which way round the part is: a white square holds a dark part,
// a black square a light one.

const (
	matMinShare  = 0.02 // a mat's frame encloses at least this share of the photo
	matFrameFill = 0.5  // a frame's own pixels cover under this share of what it encloses
	matNested    = 0.5  // an inner frame encloses at least this share of the one around it
	matInsetMM   = 0.5  // the square is shrunk by this much, to leave its edge line out
	matEmptyGrey = 40   // the part differs from the square by at least this many grey levels
)

// matSquare is the mat's square found in a photo.
type matSquare struct {
	box    []int         // [left, top, right, bottom) in full-image pixels
	inside *imaging.Gray // box-sized, 255 inside the square (shrunk by matInsetMM)
	black  bool          // a black mat: the part is lighter than the square
	fill   [3]uint8      // the square's median colour, painted outside it
}

// findMatSquare looks for a measuring mat in the photo. It returns nil when there is none, and
// the photo is then measured whole, as before. ppm is pixels per mm (the grid's scale).
func findMatSquare(img *imaging.RGB, ppm float64) *matSquare {
	gray, err := imaging.Extract(img, imaging.ChannelGray)
	if err != nil {
		return nil
	}
	w, h := gray.W, gray.H
	blur := imaging.Blur5(gray)
	dark := imaging.Threshold(blur, imaging.Otsu(blur), false)
	r := 1
	if ppm > 0 {
		r = max(1, int(0.15*ppm+0.5))
	}
	dark = imaging.SquareClose(dark, r)

	// Frames: hollow dark shapes, big enough, not touching the photo's edge (a dark table does).
	type frameC struct {
		b        imaging.Blob
		box      []int
		enclosed float64
	}
	var frames []frameC
	for _, b := range imaging.Blobs(dark) {
		if len(b.Outline) < 12 {
			continue
		}
		enc := imaging.Area(b.Outline)
		if enc < matMinShare*float64(w*h) {
			continue
		}
		box := blobBox(b)
		if box[0] <= 0 || box[1] <= 0 || box[2] >= w || box[3] >= h {
			continue
		}
		if float64(b.Area) >= matFrameFill*enc {
			continue
		}
		frames = append(frames, frameC{b, box, enc})
	}
	if len(frames) == 0 {
		return nil
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].enclosed > frames[j].enclosed })
	// The mat's band may come out as one frame or as its two edge lines: go inwards while a frame
	// nests inside the current one and is nearly as large (the inner edge, 100 mm in 120 mm).
	cur := frames[0]
	for _, f := range frames[1:] {
		if f.enclosed >= matNested*cur.enclosed && boxInside(f.box, cur.box) {
			cur = f
		}
	}

	// The square is the largest light region inside that frame (the white square, or the white
	// gap round a black mat's fill). Its convex hull includes a part that touches its edge.
	light := regionInside(cur.b.Outline, cur.box, dark, false)
	sq, ok := largestEnclosed(light)
	if !ok || imaging.Area(sq.Outline) < 0.4*cur.enclosed {
		return nil
	}
	region := hullMask(sq.Outline, w, h)
	regionArea := count(region)
	if regionArea == 0 {
		return nil
	}

	// A black mat: most of the square is dark. The black fill is then the square to measure in.
	black := countAnd(region, dark) > regionArea/2
	if black {
		inner := regionInside(nil, nil, dark, true)
		and(inner, region)
		bs, ok := largestEnclosed(inner)
		if !ok || imaging.Area(bs.Outline) < 0.5*float64(regionArea) {
			return nil
		}
		region = hullMask(bs.Outline, w, h)
	}

	inset := 2
	if ppm > 0 {
		inset = max(2, int(matInsetMM*ppm+0.5))
	}
	region = imaging.SquareErode(region, inset)
	l, t, rr, bb := maskBox(region)
	if rr-l < 16 || bb-t < 16 {
		return nil
	}

	ms := &matSquare{box: []int{l, t, rr, bb}, black: black}
	ms.inside = imaging.NewGray(rr-l, bb-t)
	var hist [3][256]int
	n := 0
	for y := t; y < bb; y++ {
		for x := l; x < rr; x++ {
			if region.Pix[y*w+x] == 0 {
				continue
			}
			ms.inside.Pix[(y-t)*(rr-l)+x-l] = 255
			i := (y*w + x) * 3
			for c := range 3 {
				hist[c][img.Pix[i+c]]++
			}
			n++
		}
	}
	for c := range 3 {
		ms.fill[c] = uint8(histMedian(hist[c][:], n))
	}
	return ms
}

// crop cuts the square's box out of img and paints everything outside the square with its
// median colour, so that the segmentation sees only what lies in the square.
func (m *matSquare) crop(img *imaging.RGB) *imaging.RGB {
	out := crop(img, m.box)
	for i, v := range m.inside.Pix {
		if v == 0 {
			out.Pix[3*i], out.Pix[3*i+1], out.Pix[3*i+2] = m.fill[0], m.fill[1], m.fill[2]
		}
	}
	return out
}

// empty reports whether nothing in the square stands out from it: the part is darker (white
// mat) or lighter (black mat) than the square by matEmptyGrey levels over at least the share of
// the square a part has to cover. Without that, Otsu would happily split paper texture.
func (m *matSquare) empty(work *imaging.RGB) bool {
	g, err := imaging.Extract(work, imaging.ChannelGray)
	if err != nil {
		return false
	}
	g = imaging.Blur5(g)
	var hist [256]int
	n := 0
	for i, v := range m.inside.Pix {
		if v != 0 {
			hist[g.Pix[i]]++
			n++
		}
	}
	if n == 0 {
		return true
	}
	med := histMedian(hist[:], n)
	need := int(math.Ceil(minPartShare * float64(n)))
	acc := 0
	if m.black {
		for v := 255; v >= 0; v-- {
			acc += hist[v]
			if acc >= need {
				return v-med < matEmptyGrey
			}
		}
	} else {
		for v := range 256 {
			acc += hist[v]
			if acc >= need {
				return med-v < matEmptyGrey
			}
		}
	}
	return true
}

// touchesEdge reports whether the part's outline (in crop pixels) runs along the square's edge:
// then some of it may lie outside the square and was cut off.
func (m *matSquare) touchesEdge(p points) bool {
	const d = 2
	w, h := m.inside.W, m.inside.H
	in := func(x, y int) bool { return x >= 0 && y >= 0 && x < w && y < h && m.inside.Pix[y*w+x] != 0 }
	near := 0
	for i := range p.X {
		x, y := int(p.X[i]), int(p.Y[i])
		if !in(x-d, y) || !in(x+d, y) || !in(x, y-d) || !in(x, y+d) {
			near++
		}
	}
	return near > max(3, p.len()/100)
}

// note says what was measured, for Result.Notes.
func (m *matSquare) note() string {
	kind := "white square, so the darker part"
	if m.black {
		kind = "black square, so the lighter part"
	}
	return fmt.Sprintf("Measuring mat found: only what lies inside its %s, was measured (box [%d, %d, %d, %d] of the photo); anything outside the square is ignored.",
		kind, m.box[0], m.box[1], m.box[2], m.box[3])
}

// regionInside is the mask of pixels inside the outline (within box), set where dark is set
// (want true) or clear (want false). With no outline it is the whole of dark (or its inverse).
func regionInside(outline []imaging.Point, box []int, dark *imaging.Gray, want bool) *imaging.Gray {
	w, h := dark.W, dark.H
	var fill *imaging.Gray
	if outline != nil {
		fill = polyMask(outline, box, w, h)
	}
	out := imaging.NewGray(w, h)
	for i, v := range dark.Pix {
		if (v != 0) == want && (fill == nil || fill.Pix[i] != 0) {
			out.Pix[i] = 255
		}
	}
	return out
}

// polyMask fills an outline into a w x h mask; box, when given, bounds the work to the
// outline's own box.
func polyMask(outline []imaging.Point, box []int, w, h int) *imaging.Gray {
	if box == nil {
		box = []int{0, 0, w, h}
	}
	bw, bh := box[2]-box[0], box[3]-box[1]
	p := points{X: make([]float64, len(outline)), Y: make([]float64, len(outline))}
	for i, q := range outline {
		p.X[i], p.Y[i] = float64(q.X-box[0]), float64(q.Y-box[1])
	}
	small := fillOutline(p, bw, bh)
	out := imaging.NewGray(w, h)
	for y := range bh {
		copy(out.Pix[(y+box[1])*w+box[0]:(y+box[1])*w+box[0]+bw], small.Pix[y*bw:(y+1)*bw])
	}
	return out
}

// hullMask fills the convex hull of an outline into a w x h mask.
func hullMask(outline []imaging.Point, w, h int) *imaging.Gray {
	hull := convexHull(outline)
	l, t, r, b := math.MaxInt, math.MaxInt, 0, 0
	for _, q := range hull {
		l, t, r, b = min(l, q.X), min(t, q.Y), max(r, q.X+1), max(b, q.Y+1)
	}
	return polyMask(hull, []int{l, t, min(r, w), min(b, h)}, w, h)
}

// largestEnclosed is the blob of mask whose outline encloses the most area.
func largestEnclosed(mask *imaging.Gray) (imaging.Blob, bool) {
	var best imaging.Blob
	bestA := 0.0
	for _, b := range imaging.Blobs(mask) {
		if len(b.Outline) < 12 {
			continue
		}
		if a := imaging.Area(b.Outline); a > bestA {
			best, bestA = b, a
		}
	}
	return best, bestA > 0
}

// convexHull is the convex hull of the points (Andrew's monotone chain), counter-clockwise.
func convexHull(pts []imaging.Point) []imaging.Point {
	p := slices.Clone(pts)
	slices.SortFunc(p, func(a, b imaging.Point) int {
		if a.X != b.X {
			return a.X - b.X
		}
		return a.Y - b.Y
	})
	p = slices.CompactFunc(p, func(a, b imaging.Point) bool { return a == b })
	if len(p) < 3 {
		return p
	}
	cross := func(o, a, b imaging.Point) int { return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X) }
	hull := make([]imaging.Point, 0, 2*len(p))
	for _, q := range p {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], q) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, q)
	}
	lower := len(hull) + 1
	for i := len(p) - 2; i >= 0; i-- {
		q := p[i]
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], q) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, q)
	}
	return hull[:len(hull)-1]
}

// boxInside reports whether box a lies strictly inside box b.
func boxInside(a, b []int) bool { return a[0] > b[0] && a[1] > b[1] && a[2] < b[2] && a[3] < b[3] }

func count(g *imaging.Gray) int {
	n := 0
	for _, v := range g.Pix {
		if v != 0 {
			n++
		}
	}
	return n
}

func countAnd(a, b *imaging.Gray) int {
	n := 0
	for i, v := range a.Pix {
		if v != 0 && b.Pix[i] != 0 {
			n++
		}
	}
	return n
}

// and clears a where b is clear.
func and(a, b *imaging.Gray) {
	for i, v := range b.Pix {
		if v == 0 {
			a.Pix[i] = 0
		}
	}
}

// maskBox is the bounding box [l, t, r, b) of the set pixels, or all zero when none are set.
func maskBox(g *imaging.Gray) (l, t, r, b int) {
	l, t = g.W, g.H
	for y := range g.H {
		for x := range g.W {
			if g.Pix[y*g.W+x] != 0 {
				l, t, r, b = min(l, x), min(t, y), max(r, x+1), max(b, y+1)
			}
		}
	}
	if r == 0 {
		return 0, 0, 0, 0
	}
	return l, t, r, b
}
