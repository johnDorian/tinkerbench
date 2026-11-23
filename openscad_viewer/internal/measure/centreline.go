package measure

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// A silhouette is a bent strip when it is long for its thickness and its middle bows away
// from the straight line between its ends. Both tests are relative, so they do not depend on
// the scale. A straight bar fails the bow test however long it is, and a disc fails the
// aspect test however round it is: either way the step model is the right description.
const (
	clMinAspect = 10.0 // length over median thickness: long and thin
	clSagThick  = 3.0  // the bow must exceed this many median thicknesses ...
	clSagFrac   = 0.03 // ... and this fraction of the length
	clKnuckle   = 1.5  // an end is a knuckle when its thickness peaks above this times the median
	clSmoothMM  = 1.0  // half-width of the moving average along the spine
	clSpacingMM = 0.8  // spacing the output points aim for, before clamping their count
	clMinPts    = 20
	clMaxPts    = 60
	clEndSpan   = 0.1 // fraction of the length searched for the centre of a knuckle
)

// Centreline is the spine of a long, thin part that bends, with its thickness across the strip
// and the round knuckles at its ends. It is measured from a photo, so the numbers carry the
// photo's accuracy: the spine is on the pixel grid and the thickness is good to a pixel.
//
// The frame is the one OpenSCAD wants. The first point is at the origin and the chord from the
// first point to the last lies along +x. The bow is on +y or -y depending on which way the part
// curves, so a mirrored part gives a mirrored bow. Y is the photo's Y flipped, so the frame is
// the part as seen from above with Y pointing up.
type Centreline struct {
	Points            [][2]float64 `json:"points_mm"`
	ThicknessMM       []float64    `json:"thickness_mm"`        // across the strip, at each point
	MedianThicknessMM float64      `json:"median_thickness_mm"` // of ThicknessMM, which the knuckles raise only at the ends
	LengthMM          float64      `json:"length_mm"`           // arc length of the spine
	ChordMM           float64      `json:"chord_mm"`            // straight distance from the first point to the last
	SagMM             float64      `json:"sag_mm"`              // greatest distance of the spine from the chord
	Ends              []EndFit     `json:"ends"`
	OpenSCAD          string       `json:"openscad"`

	pixels [][2]float64 // Points in the mask's pixel coordinates, for drawing on the photo
}

// EndFit is a round knuckle at one end of a Centreline. Only ends where the strip swells into a
// circle are reported; a plain strip end has none.
type EndFit struct {
	At         string     `json:"at"`          // "start" (the first point) or "end" (the last)
	CentreMM   [2]float64 `json:"centre_mm"`   // in the Centreline frame
	DiameterMM float64    `json:"diameter_mm"` // of the fitted circle
	RMSMM      float64    `json:"rms_mm"`      // rms distance of the outline from the circle

	px  [2]float64 // centre, in mask pixels
	pxR float64    // radius, in mask pixels
}

// PixelPath is the spine in the mask's pixel coordinates (y down), in the same order as Points,
// so an overlay can draw it on the image the mask came from.
func (c *Centreline) PixelPath() [][2]float64 { return c.pixels }

// PixelCircle is the centre and radius of the knuckle in the mask's pixel coordinates.
func (e EndFit) PixelCircle() (cx, cy, r float64) { return e.px[0], e.px[1], e.pxR }

// centreline finds the spine of a long, thin part that bends. mask is the part's silhouette,
// 255 for the part and 0 for everything else, in the pixels of the photo; when it has more than
// one blob the largest is used. mmPerPx is the scale. It returns false when the part is not a
// bent strip, in which case the step model is the right description.
func centreline(mask *imaging.Gray, mmPerPx float64) (*Centreline, bool) {
	if mask == nil || mmPerPx <= 0 {
		return nil, false
	}
	blobs := imaging.Blobs(mask)
	if len(blobs) == 0 || len(blobs[0].Outline) < 12 {
		return nil, false
	}
	blob := blobs[0]

	// Thinning costs one pass over the part's box for every pixel of its half-thickness, so a
	// large compact shape (a disc, or the filled outline of a frame) would take minutes for a
	// shape that is plainly not a strip. A strip of length L and thickness t has an area of
	// about L*t and a perimeter of about 2L, so P*P/(4A) estimates L/t from the outline alone.
	// The gate is set at half the real test, so it only turns away what that would turn away.
	var perim float64
	for i, q := range blob.Outline {
		r := blob.Outline[(i+1)%len(blob.Outline)]
		perim += math.Hypot(float64(r.X-q.X), float64(r.Y-q.Y))
	}
	if perim*perim < 4*float64(blob.Area)*clMinAspect/2 {
		return nil, false
	}

	// The part itself is measured for the thickness, so the thinning works on a copy.
	g := clComponent(mask, blob)
	skel := slices.Clone(g.pix)
	zhangSuen(skel, g.w)
	idx := clSkeletonPath(skel, g.w)
	if len(idx) < 2 {
		return nil, false
	}
	raw := make([][2]float64, len(idx))
	for i, j := range idx {
		raw[i] = [2]float64{float64(j % g.w), float64(j / g.w)} // grid coordinates
	}
	// Start at the upper end of the strip, so the frame does not depend on the search's start.
	if raw[0][1] > raw[len(raw)-1][1] {
		slices.Reverse(raw)
	}

	// Everything below is in pixels until it is turned into the output frame; an isotropic
	// scale commutes with the arc length and the normal, so only the thickness needs mm.
	smooth := clSmooth(raw, clSmoothMM/mmPerPx)
	totalPx := clPathLen(smooth)
	n := min(max(int(math.Round(totalPx*mmPerPx/clSpacingMM))+1, clMinPts), clMaxPts)
	spine := clResample(smooth, n)

	thick := make([]float64, n) // mm
	for i, p := range spine {
		tx, ty := clTangent(spine, i)
		thick[i] = g.width(p, tx, ty, totalPx) * mmPerPx
	}
	medT := median(thick)
	lengthMM := clPathLen(spine) * mmPerPx
	if !(medT > 0) || lengthMM/medT < clMinAspect {
		return nil, false
	}

	full := make([][2]float64, n) // in the photo's pixel coordinates
	for i, p := range spine {
		full[i] = g.full(p)
	}
	to := clToFrame(full[0], full[n-1], mmPerPx)
	pts := make([][2]float64, n)
	var sag float64
	for i, p := range full {
		pts[i] = to(p)
		sag = math.Max(sag, math.Abs(pts[i][1]))
	}
	if sag < math.Max(clSagThick*medT, clSagFrac*lengthMM) {
		return nil, false
	}
	chordMM := math.Hypot(full[n-1][0]-full[0][0], full[n-1][1]-full[0][1]) * mmPerPx
	for i := range pts {
		pts[i] = [2]float64{round3(pts[i][0]), round3(pts[i][1])}
	}
	for i := range thick {
		thick[i] = round3(thick[i])
	}

	c := &Centreline{
		Points:            pts,
		ThicknessMM:       thick,
		MedianThicknessMM: round3(medT),
		LengthMM:          round3(lengthMM),
		ChordMM:           round3(chordMM),
		SagMM:             round3(sag),
		pixels:            full,
	}

	// A knuckle is where the strip swells above its median. The skeleton runs into its centre,
	// so the thickness peaks there; the circle is then fitted to the outline around that peak.
	k := max(1, int(clEndSpan*float64(n)))
	if e, ok := clKnuckleAt(thick, 0, k, full, blob.Outline, medT, mmPerPx, to); ok {
		e.At = "start"
		c.Ends = append(c.Ends, e)
	}
	if e, ok := clKnuckleAt(thick, n-k, n, full, blob.Outline, medT, mmPerPx, to); ok {
		e.At = "end"
		c.Ends = append(c.Ends, e)
	}

	c.OpenSCAD = c.openSCAD()
	return c, true
}

// clKnuckleAt looks for a knuckle among the points [i0, i1) and fits it. The peak thickness in
// that range has to be clKnuckle times the median; the circle is then fitted to the outline
// within a little more than its radius of the peak, iterating so the window follows the fit.
func clKnuckleAt(thick []float64, i0, i1 int, full [][2]float64, outline []imaging.Point, medT, mmPerPx float64, to func([2]float64) [2]float64) (EndFit, bool) {
	best := i0
	for i := i0; i < i1; i++ {
		if thick[i] > thick[best] {
			best = i
		}
	}
	if thick[best] <= clKnuckle*medT {
		return EndFit{}, false
	}
	c := full[best]
	r := thick[best] / mmPerPx / 2
	var fit circle
	for range 3 {
		sel := points{}
		for _, q := range outline {
			x, y := float64(q.X), float64(q.Y)
			if math.Hypot(x-c[0], y-c[1]) <= 1.15*r {
				sel.X = append(sel.X, x)
				sel.Y = append(sel.Y, y)
			}
		}
		if sel.len() < 12 {
			return EndFit{}, false
		}
		fit = fitCircle(sel)
		if math.IsNaN(fit.CX) || math.IsNaN(fit.CY) || !(fit.R > 0) {
			return EndFit{}, false
		}
		c = [2]float64{fit.CX, fit.CY}
		r = fit.R
	}
	// A knuckle is a circle: a fit that is not one (the strip's sides, a corner) is no knuckle.
	if fit.RMS > 0.1*fit.R {
		return EndFit{}, false
	}
	centre := to(c)
	return EndFit{
		CentreMM:   [2]float64{round3(centre[0]), round3(centre[1])},
		DiameterMM: round3(2 * fit.R * mmPerPx),
		RMSMM:      round4(fit.RMS * mmPerPx),
		px:         c,
		pxR:        fit.R,
	}, true
}

// openSCAD writes the centreline as variables to paste at the top of main.scad.
func (c *Centreline) openSCAD() string {
	var b strings.Builder
	b.WriteString("// Centreline of a bent strip, measured from a photo: mm, first point at the origin,\n")
	b.WriteString("// chord along +x. Rounded to what the photo can show.\n")
	b.WriteString("centreline = [")
	for i, p := range c.Points {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "[%.3f,%.3f]", p[0], p[1])
	}
	b.WriteString("];\n")
	fmt.Fprintf(&b, "strip_thickness = %.2f;  // median across the strip\n", c.MedianThicknessMM)
	for _, e := range c.Ends {
		fmt.Fprintf(&b, "knuckle_%s = [%.2f, %.2f, %.2f];  // centre x, y and diameter\n", e.At, e.CentreMM[0], e.CentreMM[1], e.DiameterMM)
	}
	return b.String()
}

// clToFrame returns the map from the photo's pixels to the output frame. The frame has y flipped
// (so it is up when the photo is seen from above), the first point at the origin, and the chord
// turned onto +x. first and last are in pixels; mmPerPx is the scale.
func clToFrame(first, last [2]float64, mmPerPx float64) func([2]float64) [2]float64 {
	ax, ay := first[0]*mmPerPx, -first[1]*mmPerPx
	bx, by := last[0]*mmPerPx, -last[1]*mmPerPx
	ang := math.Atan2(by-ay, bx-ax)
	cos, sin := math.Cos(ang), math.Sin(ang)
	return func(p [2]float64) [2]float64 {
		dx, dy := p[0]*mmPerPx-ax, -p[1]*mmPerPx-ay
		return [2]float64{dx*cos + dy*sin, -dx*sin + dy*cos}
	}
}

// ---------- the mask, thinned ----------

// clGrid is the part's own pixels, cut from the photo with a clear margin so the thinning never
// has to look outside it. Grid coordinates are pixels from its top-left corner, which is the
// photo's pixel (ox, oy).
type clGrid struct {
	w, h   int
	ox, oy int
	pix    []uint8 // 1 = part
}

// full converts a grid point to the photo's pixel coordinates.
func (g *clGrid) full(p [2]float64) [2]float64 {
	return [2]float64{p[0] + float64(g.ox), p[1] + float64(g.oy)}
}

// inside reports whether the grid pixel nearest to (x, y) is part.
func (g *clGrid) inside(x, y float64) bool {
	i, j := int(math.Round(x)), int(math.Round(y))
	return i >= 0 && j >= 0 && i < g.w && j < g.h && g.pix[j*g.w+i] != 0
}

// width is the width of the part across the strip at p, in pixels: the distance from p to the
// edge along the normal in both directions, added. maxPx bounds the search.
func (g *clGrid) width(p [2]float64, tx, ty, maxPx float64) float64 {
	nx, ny := -ty, tx
	const step = 0.25
	reach := func(dir float64) float64 {
		var k float64
		for d := step; d <= maxPx; d += step {
			if !g.inside(p[0]+dir*d*nx, p[1]+dir*d*ny) {
				break
			}
			k = d
		}
		return k
	}
	return reach(1) + reach(-1)
}

// clComponent cuts the blob out of the mask as a grid. It floods from the blob's outline, so
// other blobs in the mask are left out.
func clComponent(mask *imaging.Gray, b imaging.Blob) *clGrid {
	minX, minY, maxX, maxY := math.MaxInt, math.MaxInt, math.MinInt, math.MinInt
	for _, q := range b.Outline {
		minX, minY = min(minX, q.X), min(minY, q.Y)
		maxX, maxY = max(maxX, q.X), max(maxY, q.Y)
	}
	g := &clGrid{w: maxX - minX + 5, h: maxY - minY + 5, ox: minX - 2, oy: minY - 2}
	g.pix = make([]uint8, g.w*g.h)

	start := b.Outline[0]
	stack := []imaging.Point{start}
	g.pix[(start.Y-g.oy)*g.w+start.X-g.ox] = 1
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				x, y := p.X+dx, p.Y+dy
				if x < minX || x > maxX || y < minY || y > maxY || mask.At(x, y) == 0 {
					continue
				}
				j := (y-g.oy)*g.w + x - g.ox
				if g.pix[j] == 0 {
					g.pix[j] = 1
					stack = append(stack, imaging.Point{X: x, Y: y})
				}
			}
		}
	}
	return g
}

// zhangSuen thins the set pixels of a grid to a one-pixel-wide skeleton, in place, by the
// Zhang-Suen method. Each pass peels the outline in one direction, so a strip narrows from both
// sides and stops on its middle line; the grid must have a clear border.
func zhangSuen(pix []uint8, w int) {
	off := [8]int{-w, -w + 1, 1, w + 1, w, w - 1, -1, -w - 1} // P2..P9: N, NE, E, SE, S, SW, W, NW
	live := make([]int, 0, len(pix))
	for i, v := range pix {
		if v != 0 {
			live = append(live, i)
		}
	}
	var del []int
	for changed := true; changed; {
		changed = false
		for pass := range 2 {
			del = del[:0]
			for _, i := range live {
				var p [8]uint8
				nb := 0
				for k, o := range off {
					p[k] = pix[i+o]
					nb += int(p[k])
				}
				if nb < 2 || nb > 6 {
					continue
				}
				trans := 0
				for k := range 8 {
					if p[k] == 0 && p[(k+1)%8] == 1 {
						trans++
					}
				}
				if trans != 1 {
					continue
				}
				p2, p4, p6, p8 := p[0], p[2], p[4], p[6]
				if pass == 0 {
					if p2*p4*p6 != 0 || p4*p6*p8 != 0 {
						continue
					}
				} else if p2*p4*p8 != 0 || p2*p6*p8 != 0 {
					continue
				}
				del = append(del, i)
			}
			for _, i := range del {
				pix[i] = 0
			}
			changed = changed || len(del) > 0
			k := 0
			for _, i := range live {
				if pix[i] != 0 {
					live[k] = i
					k++
				}
			}
			live = live[:k]
		}
	}
}

// clSkeletonPath returns the longest path through the skeleton, as grid indices from one end to
// the other. Two breadth-first searches find the far ends of a tree-like graph; the short spurs
// a thinning leaves at the knuckles are shorter than the strip, so the path runs along it.
func clSkeletonPath(pix []uint8, w int) []int {
	seed := -1
	for i, v := range pix {
		if v != 0 {
			seed = i
			break
		}
	}
	if seed < 0 {
		return nil
	}
	a, _ := clFarthest(pix, w, seed)
	b, parent := clFarthest(pix, w, a)
	var path []int
	for i := b; i != -1; i = int(parent[i]) {
		path = append(path, i)
	}
	return path
}

// clFarthest is a breadth-first search over the skeleton from src. It returns the last pixel
// reached and the parent of each reached pixel (-1 for src, -2 for unreached).
func clFarthest(pix []uint8, w int, src int) (int, []int32) {
	parent := make([]int32, len(pix))
	for i := range parent {
		parent[i] = -2
	}
	parent[src] = -1
	queue := []int32{int32(src)}
	last := src
	for head := 0; head < len(queue); head++ {
		cur := int(queue[head])
		last = cur
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				n := cur + dy*w + dx
				if (dx == 0 && dy == 0) || n < 0 || n >= len(pix) || pix[n] == 0 || parent[n] != -2 {
					continue
				}
				parent[n] = int32(cur)
				queue = append(queue, int32(n))
			}
		}
	}
	return last, parent
}

// ---------- the spine ----------

// clSmooth is a moving average of half-width half (in pixels). Past an end the path is reflected
// through that end rather than cut short, so the average does not pull the ends inward: a
// straight path stays exactly where it is.
//
// The window is clamped to the path: the reflection reaches at most n-1 points past an end,
// so on a path shorter than the window (a short, stubby part) a wider one would index outside it.
func clSmooth(p [][2]float64, half float64) [][2]float64 {
	n := len(p)
	if n < 2 {
		return append([][2]float64(nil), p...)
	}
	h := min(max(int(math.Round(half)), 1), n-1)
	at := func(j int) [2]float64 {
		switch {
		case j < 0:
			return [2]float64{2*p[0][0] - p[-j][0], 2*p[0][1] - p[-j][1]}
		case j >= n:
			q := p[2*(n-1)-j]
			return [2]float64{2*p[n-1][0] - q[0], 2*p[n-1][1] - q[1]}
		}
		return p[j]
	}
	out := make([][2]float64, n)
	for i := range p {
		var sx, sy float64
		for j := i - h; j <= i+h; j++ {
			q := at(j)
			sx += q[0]
			sy += q[1]
		}
		m := float64(2*h + 1)
		out[i] = [2]float64{sx / m, sy / m}
	}
	return out
}

// clPathLen is the length of a polyline.
func clPathLen(p [][2]float64) float64 {
	var s float64
	for i := 1; i < len(p); i++ {
		s += math.Hypot(p[i][0]-p[i-1][0], p[i][1]-p[i-1][1])
	}
	return s
}

// clResample places n points at even arc length along the polyline, the first and last at its
// ends.
func clResample(p [][2]float64, n int) [][2]float64 {
	total := clPathLen(p)
	out := make([][2]float64, n)
	out[0] = p[0]
	out[n-1] = p[len(p)-1]
	seg, acc := 0, 0.0 // the segment p[seg]..p[seg+1] holds arc length acc at its start
	for k := 1; k < n-1; k++ {
		target := total * float64(k) / float64(n-1)
		for seg < len(p)-2 {
			l := math.Hypot(p[seg+1][0]-p[seg][0], p[seg+1][1]-p[seg][1])
			if acc+l >= target {
				break
			}
			acc += l
			seg++
		}
		l := math.Hypot(p[seg+1][0]-p[seg][0], p[seg+1][1]-p[seg][1])
		f := 0.0
		if l > 0 {
			f = min(max((target-acc)/l, 0), 1)
		}
		out[k] = [2]float64{p[seg][0] + f*(p[seg+1][0]-p[seg][0]), p[seg][1] + f*(p[seg+1][1]-p[seg][1])}
	}
	return out
}

// clTangent is the unit direction of the spine at point i, from its neighbours.
func clTangent(p [][2]float64, i int) (float64, float64) {
	a, b := max(i-1, 0), min(i+1, len(p)-1)
	dx, dy := p[b][0]-p[a][0], p[b][1]-p[a][1]
	l := math.Hypot(dx, dy)
	if l == 0 {
		return 1, 0
	}
	return dx / l, dy / l
}

// fillOutline rasterises an outline (pixel centres, as silhouette returns them) into a w x h
// mask, 255 inside. The outline is taken as a closed polygon with the even-odd rule, so the
// border pixels are part of the mask. It is how an outline becomes the mask centreline needs.
func fillOutline(p points, w, h int) *imaging.Gray {
	g := imaging.NewGray(w, h)
	n := p.len()
	var xs []float64
	for y := range h {
		yc := float64(y)
		xs = xs[:0]
		for i := range n {
			j := (i + 1) % n
			ya, yb := p.Y[i], p.Y[j]
			if (ya <= yc && yb > yc) || (yb <= yc && ya > yc) {
				xs = append(xs, p.X[i]+(yc-ya)/(yb-ya)*(p.X[j]-p.X[i]))
			}
		}
		slices.Sort(xs)
		for k := 0; k+1 < len(xs); k += 2 {
			for x := max(int(math.Ceil(xs[k])), 0); x <= min(int(math.Floor(xs[k+1])), w-1); x++ {
				g.Set(x, y, 255)
			}
		}
	}
	return g
}
