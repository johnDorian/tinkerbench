package measure

import (
	"errors"
	"fmt"
	"math"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// The dotted search works at half resolution: a dot is a few pixels across at 80 px/mm, and
// the pitch is tens of pixels, so nothing it needs is lost, and it runs in seconds on a 12 MP
// photo.
const (
	dotHalf      = 2    // downsampling factor
	dotMinPeriod = 6.0  // shortest cell searched for, in half-resolution pixels
	dotMaxPeriod = 60.0 // longest cell searched for: 120 px at full resolution, a phone photo at 20-120 px/mm
	dotBlockMax  = 512  // largest square block analysed, in half-resolution pixels
	dotMinRatio  = 6.0  // a lattice peak must stand this far above the band's mean power
	dotPad       = 2    // the block is zero-padded to this many times its size
	dotLocalPct  = 0.08 // how far a block's period may stray from the photo's, relatively
	dotLocalDeg  = 4.0  // ...and its angle from the photo's, in degrees
)

// dottedGrid is what the dotted search finds.
type dottedGrid struct {
	PitchPx    float64 // median pitch over the photo, full-resolution pixels per cell
	AngleDeg   float64 // the grid's rotation, in (-45, 45]
	SpreadFrac float64 // the spread of the pitch between blocks, relative to its median
	PitchMinPx float64 // the shortest and the longest pitch of the blocks that agreed (full resolution)
	PitchMaxPx float64
	Blocks     int // blocks that agreed
	// PitchAt is the pitch at a point of the photo (full-resolution pixels). A phone photo is
	// not flat, so the pitch changes across it by a few per cent; a plane fitted to the blocks
	// gives the pitch where the part is.
	PitchAt func(x, y float64) float64
}

// dottedBlock is one block's answer: the period (in half-resolution pixels), the wave-vector
// angle, and where the block is.
type dottedBlock struct {
	period, angle float64
	perp          float64 // the period across the first family (0 when the block has none)
	cx, cy        float64 // centre, full-resolution pixels
}

// dottedPitch finds the pitch and the rotation of a grid drawn as dots or dashes, which has no
// lines for a Hough transform.
//
// A grid of dots is a two-dimensional lattice, so its spectrum has peaks at the lattice's
// reciprocal points: a peak at distance 1/P from the origin is a grid of period P, and its
// direction is the normal to the grid lines. The photo is cut into square blocks, each with its
// own spectrum. The spectra are added up first, so the grid, which is in every block, is the
// strongest peak of the sum, and the moire and the part are not. Then each block finds the peak
// near that one, which gives the pitch where the block is: a phone photo is not flat, and the
// pitch differs by a few per cent across the frame. The scale is the median over the blocks,
// and a plane through them gives it where the part is. The part is masked out first, so its
// edges make no lattice of their own, and the paper's shading is removed by a high-pass.
func dottedPitch(gray *imaging.Gray) (dottedGrid, error) {
	g := halve(gray)
	w, h := g.W, g.H
	n := dotBlockMax
	for n > min(w, h) {
		n /= 2
	}
	if n < 128 {
		return dottedGrid{}, errors.New("the image is too small to find a dotted grid in")
	}

	// Darkness above the paper, high-passed so the shadow's gradient is gone and the dots
	// are not. Negative values (paper brighter than its surroundings) are noise, not dots.
	d := make([]float64, w*h)
	for i, v := range g.Pix {
		d[i] = float64(255 - int(v))
	}
	local := boxMean(d, w, h, 4)

	// The part: a large dark blob. Opening drops the dots and any lines; the dilation adds a
	// margin for the shadow edge.
	dark := imaging.Threshold(g, int(0.45*percentile(g.Pix, 0.9)), false)
	part := imaging.Dilate(imaging.Open(dark, imaging.EllipseSE(7)), imaging.EllipseSE(15))

	hp := make([]float64, w*h)
	dots := 0
	for i := range hp {
		if part.Pix[i] == 0 {
			if v := d[i] - local[i]; v > 0 {
				hp[i] = v
				dots++
			}
		}
	}
	if dots < 1000 {
		return dottedGrid{}, errors.New("no dots found outside the part")
	}
	// The dots along a line repeat at a few pixels, and that is stronger than the grid, which
	// is what the spectrum would find. Two passes of a 9-pixel box (about a Gaussian of sigma 4
	// pixels) remove it and keep the lines.
	hp = boxMean(boxMean(hp, w, h, 4), w, h, 4)

	// The spectrum of every block, kept inside the band where a lattice can be.
	type blockSpec struct {
		cx, cy float64
		sp     *spectrum
	}
	var specs []blockSpec
	for y0 := 0; y0+n <= h; y0 += n {
		for x0 := 0; x0+n <= w; x0 += n {
			if sp := blockSpectrum(hp, w, x0, y0, n); sp != nil {
				specs = append(specs, blockSpec{
					cx: dotHalf * float64(x0+n/2),
					cy: dotHalf * float64(y0+n/2),
					sp: sp,
				})
			}
		}
	}
	if len(specs) == 0 {
		return dottedGrid{}, errors.New("the dots repeat at no pitch that can be measured")
	}

	// The grid: the strongest peak of the summed, normalised spectra.
	sum := specs[0].sp.clone()
	for _, b := range specs[1:] {
		sum.add(b.sp)
	}
	gkx, gky, ok := sum.strongest()
	if !ok {
		return dottedGrid{}, errors.New("the dots repeat at no pitch that can be measured")
	}

	// Each block's own peak, near the grid's.
	var got []dottedBlock
	for _, b := range specs {
		relP, ang := b.sp.tolerance(math.Hypot(float64(gkx), float64(gky)))
		kx, ky, ok := b.sp.nearest(gkx, gky, relP, ang)
		if !ok {
			continue
		}
		// The other family of lines is the same peak turned a quarter turn: on square paper it has
		// the same period, so the two must agree.
		perp := 0.0
		if px, py, ok := b.sp.nearest(-ky, kx, relP, ang); ok {
			perp = b.sp.period(px, py)
		}
		got = append(got, dottedBlock{period: b.sp.period(kx, ky), angle: math.Atan2(float64(ky), float64(kx)), perp: perp, cx: b.cx, cy: b.cy})
	}
	if len(got) == 0 {
		return dottedGrid{}, errors.New("the dots repeat at no pitch that can be measured")
	}
	// Square paper, so the two axes have to agree, as the solid lines' do. If they do not the
	// scale is a guess, so refuse.
	var along, across []float64
	for _, b := range got {
		if b.perp > 0 {
			along = append(along, b.period)
			across = append(across, b.perp)
		}
	}
	// No block that sees both axes means the pitch is from one family of peaks alone, which
	// is what an edge or a shadow also makes: unverified, so refuse rather than guess.
	if len(along) == 0 {
		return dottedGrid{}, errors.New("no part of the paper shows both axes of the grid at one pitch, so the scale cannot be verified")
	}
	ma, mc := median(along), median(across)
	if math.Abs(ma-mc) > 0.05*math.Max(ma, mc) {
		return dottedGrid{}, fmt.Errorf("the dots' two axes disagree (%.1f px vs %.1f px), so the scale would be unreliable", ma*dotHalf, mc*dotHalf)
	}

	// The blocks that agree with the photo's plane give the pitch. Their angles, averaged at
	// four times the value (the grid repeats every 90 degrees), give the rotation.
	at, kept := pitchPlane(got)
	var sin4, cos4 float64
	var periods []float64
	for i, b := range got {
		if !kept[i] {
			continue
		}
		periods = append(periods, b.period)
		sin4 += math.Sin(4 * b.angle)
		cos4 += math.Cos(4 * b.angle)
	}
	angle := math.Atan2(sin4, cos4) / 4 * 180 / math.Pi

	med := median(periods)
	spread := (maxOf(periods) - minOf(periods)) / med
	return dottedGrid{
		PitchPx:    med * dotHalf,
		AngleDeg:   angle,
		SpreadFrac: spread,
		PitchMinPx: minOf(periods) * dotHalf,
		PitchMaxPx: maxOf(periods) * dotHalf,
		Blocks:     len(periods),
		PitchAt:    at,
	}, nil
}

// spectrum is the power spectrum of a block, kept for the frequencies a lattice can have: the
// square of side 2R+1 about zero frequency, of which only the band is non-zero.
type spectrum struct {
	m, R int       // the padded size of the transform, and the half-width kept
	p    []float64 // power, indexed by (ky+R)*(2R+1)+(kx+R)
	// the band (cycles per padded block) that a lattice of periods dotMinPeriod..dotMaxPeriod
	// occupies
	rLo, rHi float64
}

func (s *spectrum) idx(kx, ky int) int { return (ky+s.R)*(2*s.R+1) + kx + s.R }

func (s *spectrum) inBand(kx, ky int) bool {
	r := math.Hypot(float64(kx), float64(ky))
	return r >= s.rLo && r <= s.rHi
}

func (s *spectrum) clone() *spectrum {
	c := *s
	c.p = append([]float64(nil), s.p...)
	return &c
}

// add adds o's power, after scaling each spectrum to its own strongest peak so that one
// block with a loud pattern does not decide the sum.
func (s *spectrum) add(o *spectrum) {
	for i := range s.p {
		s.p[i] += o.p[i]
	}
}

// normalise scales the band of s to a peak of 1.
func (s *spectrum) normalise() {
	var pmax float64
	for _, v := range s.p {
		pmax = math.Max(pmax, v)
	}
	if pmax == 0 {
		return
	}
	for i := range s.p {
		s.p[i] /= pmax
	}
}

// strongest is the fundamental of the square lattice in the (normalised) spectrum.
//
// Square paper has two families of lines a quarter turn apart with the same pitch, so a peak
// is only a candidate when the peak turned by 90 degrees is there too, at the same period: a
// lone peak is the part's edge, a shadow or the leakage of the mask, and on a small crop with
// one block to look at such a peak can be the strongest of all. A candidate's strength is the
// weaker of its pair. Of the candidates within half of the best, the one with the longest
// period wins: a lattice of dots at the crossings has equal peaks at its fundamental and at its
// diagonals (both pairs are square), and the fundamental is the longer one; a lattice of lines
// has the fundamental strongest anyway.
//
// ok is false when no peak has a partner: the dots form no square lattice that can be read.
func (s *spectrum) strongest() (kx, ky int, ok bool) {
	s.normalise()
	var pmax float64
	for y := -s.R; y <= s.R; y++ {
		for x := -s.R; x <= s.R; x++ {
			if s.inBand(x, y) {
				pmax = math.Max(pmax, s.p[s.idx(x, y)])
			}
		}
	}
	if pmax == 0 {
		return 0, 0, false
	}
	type cand struct {
		x, y  int
		r, sc float64
	}
	var cands []cand
	best := 0.0
	for y := -s.R; y <= s.R; y++ {
		for x := -s.R; x <= s.R; x++ {
			if !s.inBand(x, y) {
				continue
			}
			p := s.p[s.idx(x, y)]
			if p < 0.25*pmax || !s.localMax(x, y) {
				continue
			}
			r := math.Hypot(float64(x), float64(y))
			relP, ang := s.tolerance(r)
			px, py, found := s.nearest(-y, x, relP, ang)
			if !found || !s.localMax(px, py) {
				continue
			}
			sc := math.Min(p, s.p[s.idx(px, py)])
			cands = append(cands, cand{x, y, r, sc})
			best = math.Max(best, sc)
		}
	}
	bestR := math.Inf(1)
	for _, c := range cands {
		if c.sc >= 0.5*best && c.r < bestR {
			bestR, kx, ky = c.r, c.x, c.y
		}
	}
	return kx, ky, !math.IsInf(bestR, 1)
}

// tolerance is how far a peak at radius r (bins) may stray in period (relative) and in angle
// (radians) and still be the same family: dotLocalPct and dotLocalDeg, or one and a half bins
// where the spectrum is too coarse for those (a small block has few bins out to the grid).
func (s *spectrum) tolerance(r float64) (relPeriod, angle float64) {
	return math.Max(dotLocalPct, 1.5/r), math.Max(dotLocalDeg*math.Pi/180, math.Atan(1.5/r))
}

// localMax reports whether (kx, ky) is at least as strong as its eight neighbours.
func (s *spectrum) localMax(kx, ky int) bool {
	p := s.p[s.idx(kx, ky)]
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			x, y := kx+dx, ky+dy
			if (dx != 0 || dy != 0) && x >= -s.R && x <= s.R && y >= -s.R && y <= s.R && s.p[s.idx(x, y)] > p {
				return false
			}
		}
	}
	return true
}

// period is the period in pixels of the lattice at frequency (kx, ky), refined to a sub-bin
// position by parabolic interpolation along each axis.
func (s *spectrum) period(kx, ky int) float64 {
	fx, fy := s.refine(kx, ky)
	return float64(s.m) / math.Hypot(fx, fy)
}

// refine interpolates the peak at (kx, ky) to a fraction of a bin.
func (s *spectrum) refine(kx, ky int) (float64, float64) {
	fx, fy := float64(kx), float64(ky)
	p := s.p[s.idx(kx, ky)]
	if kx-1 >= -s.R && kx+1 <= s.R {
		a, c := s.p[s.idx(kx-1, ky)], s.p[s.idx(kx+1, ky)]
		if d := a - 2*p + c; d < 0 {
			fx += math.Max(-0.5, math.Min(0.5, 0.5*(a-c)/d))
		}
	}
	if ky-1 >= -s.R && ky+1 <= s.R {
		a, c := s.p[s.idx(kx, ky-1)], s.p[s.idx(kx, ky+1)]
		if d := a - 2*p + c; d < 0 {
			fy += math.Max(-0.5, math.Min(0.5, 0.5*(a-c)/d))
		}
	}
	return fx, fy
}

// nearest is the strongest local maximum of the block within relPeriod (relative) of the
// period of (gx, gy) and within angle (radians) of its direction, or false.
func (s *spectrum) nearest(gx, gy int, relPeriod, angle float64) (int, int, bool) {
	gr := math.Hypot(float64(gx), float64(gy))
	ga := math.Atan2(float64(gy), float64(gx))
	bestP := 0.0
	bx, by := 0, 0
	for ky := -s.R; ky <= s.R; ky++ {
		for kx := -s.R; kx <= s.R; kx++ {
			if !s.inBand(kx, ky) {
				continue
			}
			r := math.Hypot(float64(kx), float64(ky))
			if math.Abs(r-gr) > relPeriod*gr {
				continue
			}
			// directions are the same modulo 180 degrees (a wave and its negative)
			if math.Abs(wrapHalf(math.Atan2(float64(ky), float64(kx))-ga)) > angle {
				continue
			}
			if p := s.p[s.idx(kx, ky)]; p > bestP {
				bestP, bx, by = p, kx, ky
			}
		}
	}
	return bx, by, bestP > 0
}

// wrapHalf folds an angle onto [-pi/2, pi/2): a direction and its reverse are the same.
func wrapHalf(a float64) float64 {
	return a - math.Pi*math.Round(a/math.Pi)
}

// blockSpectrum is the power spectrum of the n x n block of hp at (x0, y0), zero-padded to
// dotPad times its size, which interpolates the spectrum (at n the bins are three pixels apart
// in period, too coarse to place a peak to a fraction of a pixel). It is nil for a block with
// nothing in it.
func blockSpectrum(hp []float64, w, x0, y0, n int) *spectrum {
	m := dotPad * n
	win := make([]float64, n)
	for i := range win {
		win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	buf := make([]complex128, m*m)
	var energy float64
	for y := range n {
		for x := range n {
			v := hp[(y0+y)*w+x0+x] * win[y] * win[x]
			buf[y*m+x] = complex(v, 0)
			energy += v * v
		}
	}
	if energy == 0 {
		return nil
	}
	for y := range m {
		fft(buf[y*m : (y+1)*m])
	}
	col := make([]complex128, m)
	for x := range m {
		for y := range m {
			col[y] = buf[y*m+x]
		}
		fft(col)
		for y := range m {
			buf[y*m+x] = col[y]
		}
	}

	R := m / 6 // the band's outer edge is m/dotMinPeriod, but the search never needs more than m/6
	s := &spectrum{m: m, R: R, p: make([]float64, (2*R+1)*(2*R+1)),
		rLo: float64(m) / dotMaxPeriod, rHi: float64(m) / dotMinPeriod}
	for ky := -R; ky <= R; ky++ {
		for kx := -R; kx <= R; kx++ {
			if !s.inBand(kx, ky) {
				continue
			}
			c := buf[((ky+m)%m)*m+(kx+m)%m]
			s.p[s.idx(kx, ky)] = real(c)*real(c) + imag(c)*imag(c)
		}
	}
	// A block with no lattice has a flat spectrum: its peak is not much above its mean.
	var sum, pmax float64
	cnt := 0
	for ky := -R; ky <= R; ky++ {
		for kx := -R; kx <= R; kx++ {
			if s.inBand(kx, ky) {
				v := s.p[s.idx(kx, ky)]
				sum += v
				cnt++
				pmax = math.Max(pmax, v)
			}
		}
	}
	if cnt == 0 || pmax < dotMinRatio*sum/float64(cnt) {
		return nil
	}
	s.normalise()
	return s
}

// pitchPlane fits the pitch over the photo as a plane in the image (pitch = a + b x + c y). A
// block that strays more than 3 per cent from the plane (the strip, a shadow, the moire) is
// left out, the worst one first, and the plane is fitted again. It returns the plane and which
// blocks it kept. With too few blocks for a plane, the plane is the median of all of them.
func pitchPlane(blocks []dottedBlock) (func(x, y float64) float64, []bool) {
	used := make([]bool, len(blocks))
	for i := range used {
		used[i] = true
	}
	var coef [3]float64
	fit := func() bool {
		var m [3][4]float64 // normal equations, augmented
		n := 0
		for i, b := range blocks {
			if !used[i] {
				continue
			}
			n++
			row := [3]float64{1, b.cx, b.cy}
			for r := range 3 {
				for c := range 3 {
					m[r][c] += row[r] * row[c]
				}
				m[r][3] += row[r] * b.period * dotHalf
			}
		}
		if n < 3 {
			return false
		}
		sol, ok := solve3x(m)
		coef = sol
		return ok
	}
	at := func(x, y float64) float64 { return coef[0] + coef[1]*x + coef[2]*y }

	count := len(blocks)
	for count >= 3 && fit() {
		worst, wi := 0.0, -1
		for i, b := range blocks {
			if !used[i] {
				continue
			}
			p := b.period * dotHalf
			if r := math.Abs(p-at(b.cx, b.cy)) / at(b.cx, b.cy); r > worst {
				worst, wi = r, i
			}
		}
		if worst <= 0.03 || count <= 3 {
			return at, used
		}
		used[wi] = false
		count--
	}
	// Too few blocks for a plane: the median everywhere, from all of them.
	ps := make([]float64, len(blocks))
	for i, b := range blocks {
		ps[i] = b.period * dotHalf
	}
	med := median(ps)
	return func(x, y float64) float64 { return med }, used
}

// solve3x solves a 3x3 linear system given as an augmented matrix, by Gaussian elimination.
func solve3x(m [3][4]float64) ([3]float64, bool) {
	for c := range 3 {
		piv := c
		for r := c + 1; r < 3; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[piv][c]) {
				piv = r
			}
		}
		if math.Abs(m[piv][c]) < 1e-12 {
			return [3]float64{}, false
		}
		m[c], m[piv] = m[piv], m[c]
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
	return [3]float64{m[0][3] / m[0][0], m[1][3] / m[1][1], m[2][3] / m[2][2]}, true
}

// halve shrinks a grey image by a factor of two, by averaging each 2x2 block.
func halve(g *imaging.Gray) *imaging.Gray {
	w, h := g.W/dotHalf, g.H/dotHalf
	out := imaging.NewGray(w, h)
	for y := range h {
		for x := range w {
			var s int
			for dy := range dotHalf {
				for dx := range dotHalf {
					s += int(g.Pix[(y*dotHalf+dy)*g.W+x*dotHalf+dx])
				}
			}
			out.Pix[y*w+x] = uint8(s / (dotHalf * dotHalf))
		}
	}
	return out
}

// boxMean is the mean over a (2r+1)-square window at each pixel, edges clamped, from an
// integral image.
func boxMean(src []float64, w, h, r int) []float64 {
	sum := make([]float64, (w+1)*(h+1))
	for y := range h {
		var row float64
		for x := range w {
			row += src[y*w+x]
			sum[(y+1)*(w+1)+x+1] = sum[y*(w+1)+x+1] + row
		}
	}
	out := make([]float64, w*h)
	for y := range h {
		y0, y1 := max(y-r, 0), min(y+r, h-1)
		for x := range w {
			x0, x1 := max(x-r, 0), min(x+r, w-1)
			s := sum[(y1+1)*(w+1)+x1+1] - sum[y0*(w+1)+x1+1] - sum[(y1+1)*(w+1)+x0] + sum[y0*(w+1)+x0]
			out[y*w+x] = s / float64((x1-x0+1)*(y1-y0+1))
		}
	}
	return out
}

// percentile returns the q-quantile of the grey levels, from a histogram.
func percentile(pix []uint8, q float64) float64 {
	var hist [256]int
	for _, v := range pix {
		hist[v]++
	}
	want := q * float64(len(pix))
	acc := 0
	for v, c := range hist {
		acc += c
		if float64(acc) >= want {
			return float64(v)
		}
	}
	return 255
}

// fft is an in-place radix-2 transform; len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wl := complex(math.Cos(ang), math.Sin(ang))
		half := size / 2
		for i := 0; i < n; i += size {
			w := complex(1, 0)
			for j := range half {
				u, v := a[i+j], a[i+j+half]*w
				a[i+j], a[i+j+half] = u+v, u-v
				w *= wl
			}
		}
	}
}
