package measure

import (
	"math"
	"sort"
)

// points is an outline as two parallel coordinate slices.
type points struct{ X, Y []float64 }

func (p points) len() int { return len(p.X) }

func (p points) clone() points {
	q := points{X: make([]float64, len(p.X)), Y: make([]float64, len(p.Y))}
	copy(q.X, p.X)
	copy(q.Y, p.Y)
	return q
}

// frame is the mapping between image pixels and the part's own axes, kept so the overlay
// can draw part-frame measurements back onto the photo.
type frame struct {
	mx, my   float64 // centroid of the outline, in image pixels
	cos, sin float64 // rotation that puts the long axis on +X
	minX     float64 // shift that puts the leading end at x = 0
	midY     float64 // shift that centres the width on y = 0
	spanPx   float64 // length along the axis
	flipped  bool    // x was mirrored, so the far end became x = 0
}

// toImage maps a point in the part's frame back to image pixel coordinates.
func (f frame) toImage(x, y float64) (float64, float64) {
	if f.flipped {
		x = f.spanPx - x
	}
	u, v := x+f.minX, y+f.midY
	return f.mx + u*f.cos - v*f.sin, f.my + u*f.sin + v*f.cos
}

// toAxes rotates the outline so its long axis is +X with the width centred on y = 0, and
// returns the rotation that was undone in degrees. Rotating the points rather than the
// pixels costs no resolution.
//
// The long axis is the principal component of the outline, which for a 2x2 scatter matrix
// is a closed form. Its direction is reported in (-90, 90]; which end of it is x = 0 is
// decided later, by which tip is the rounder one.
func toAxes(p points) (points, float64, frame) {
	n := float64(p.len())
	var mx, my float64
	for i := range p.X {
		mx += p.X[i]
		my += p.Y[i]
	}
	mx, my = mx/n, my/n

	var sxx, sxy, syy float64
	for i := range p.X {
		dx, dy := p.X[i]-mx, p.Y[i]-my
		sxx += dx * dx
		sxy += dx * dy
		syy += dy * dy
	}
	ang := 0.5 * math.Atan2(2*sxy, sxx-syy)

	cos, sin := math.Cos(ang), math.Sin(ang)
	out := points{X: make([]float64, p.len()), Y: make([]float64, p.len())}
	minX, minY, maxY := math.Inf(1), math.Inf(1), math.Inf(-1)
	for i := range p.X {
		dx, dy := p.X[i]-mx, p.Y[i]-my
		x := dx*cos + dy*sin
		y := -dx*sin + dy*cos
		out.X[i], out.Y[i] = x, y
		minX, minY, maxY = math.Min(minX, x), math.Min(minY, y), math.Max(maxY, y)
	}
	midY := (maxY + minY) / 2
	for i := range out.X {
		out.X[i] -= minX
		out.Y[i] -= midY
	}
	f := frame{mx: mx, my: my, cos: cos, sin: sin, minX: minX, midY: midY, spanPx: maxOf(out.X)}
	return out, ang * 180 / math.Pi, f
}

// circle is a least-squares circle through a set of points.
type circle struct {
	CX, CY, R float64
	RMS       float64 // root mean square distance of the points from it
}

// fitCircle is an algebraic circle fit with one outlier-rejection pass.
func fitCircle(p points) circle {
	cx, cy, r := solveCircle(p)
	resid := make([]float64, p.len())
	var mean float64
	for i := range p.X {
		resid[i] = math.Abs(math.Hypot(p.X[i]-cx, p.Y[i]-cy) - r)
		mean += resid[i]
	}
	mean /= float64(p.len())

	cut := math.Max(2*mean, 1e-6)
	keep := points{}
	for i := range p.X {
		if resid[i] < cut {
			keep.X = append(keep.X, p.X[i])
			keep.Y = append(keep.Y, p.Y[i])
		}
	}
	if keep.len() > 10 {
		cx, cy, r = solveCircle(keep)
	}
	var sq float64
	for i := range keep.X {
		d := math.Hypot(keep.X[i]-cx, keep.Y[i]-cy) - r
		sq += d * d
	}
	rms := math.Inf(1)
	if keep.len() > 0 {
		rms = math.Sqrt(sq / float64(keep.len()))
	}
	return circle{CX: cx, CY: cy, R: r, RMS: rms}
}

// solveCircle fits x^2 + y^2 = 2*cx*x + 2*cy*y + c by least squares, which is linear in
// (cx, cy, c), and recovers the radius from c.
func solveCircle(p points) (cx, cy, r float64) {
	// normal equations of the 3-column design matrix [2x, 2y, 1]
	var m [3][4]float64
	for i := range p.X {
		row := [3]float64{2 * p.X[i], 2 * p.Y[i], 1}
		b := p.X[i]*p.X[i] + p.Y[i]*p.Y[i]
		for a := range 3 {
			for c := range 3 {
				m[a][c] += row[a] * row[c]
			}
			m[a][3] += row[a] * b
		}
	}
	s := solve3(m)
	cx, cy = s[0], s[1]
	return cx, cy, math.Sqrt(math.Max(s[2]+cx*cx+cy*cy, 1e-9))
}

// solve3 solves a 3x3 system given as an augmented matrix, by Gaussian elimination with
// partial pivoting. A singular system gives zeros, which fitCircle then reports as a bad
// fit rather than a wrong number.
func solve3(m [3][4]float64) [3]float64 {
	for c := range 3 {
		p := c
		for r := c + 1; r < 3; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[p][c]) {
				p = r
			}
		}
		m[c], m[p] = m[p], m[c]
		if math.Abs(m[c][c]) < 1e-12 {
			return [3]float64{}
		}
		for r := range 3 {
			if r == c {
				continue
			}
			f := m[r][c] / m[c][c]
			for k := c; k < 4; k++ {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	return [3]float64{m[0][3] / m[0][0], m[1][3] / m[1][1], m[2][3] / m[2][2]}
}

// median matches numpy: for an even count it averages the two middle values.
func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := make([]float64, len(v))
	copy(s, v)
	sort.Float64s(s)
	h := len(s) / 2
	if len(s)%2 == 1 {
		return s[h]
	}
	return (s[h-1] + s[h]) / 2
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
