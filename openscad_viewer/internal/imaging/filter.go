package imaging

import "math"

// Blur5 is a 5x5 Gaussian blur with the [1 4 6 4 1]/16 kernel OpenCV uses for
// GaussianBlur(src, (5,5), 0), reflecting at the border (BORDER_REFLECT_101).
func Blur5(g *Gray) *Gray {
	k := [5]int32{1, 4, 6, 4, 1}
	tmp := make([]int32, g.W*g.H)
	for y := range g.H {
		row := y * g.W
		for x := range g.W {
			var s int32
			for i := range 5 {
				s += k[i] * int32(g.Pix[row+reflect(x+i-2, g.W)])
			}
			tmp[row+x] = s
		}
	}
	out := NewGray(g.W, g.H)
	for y := range g.H {
		for x := range g.W {
			var s int32
			for i := range 5 {
				s += k[i] * tmp[reflect(y+i-2, g.H)*g.W+x]
			}
			out.Pix[y*g.W+x] = uint8((s + 128) >> 8)
		}
	}
	return out
}

// reflect mirrors an out-of-range index without repeating the edge pixel.
func reflect(i, n int) int {
	if n == 1 {
		return 0
	}
	for i < 0 || i >= n {
		if i < 0 {
			i = -i
		}
		if i >= n {
			i = 2*n - 2 - i
		}
	}
	return i
}

// Otsu returns the threshold that best splits the histogram into two classes.
func Otsu(g *Gray) int {
	var hist [256]int
	for _, v := range g.Pix {
		hist[v]++
	}
	n := float64(len(g.Pix))
	mu := 0.0
	for i, c := range hist {
		mu += float64(i) * float64(c)
	}
	mu /= n
	var q1, m1sum, best float64
	bestT := 0
	for t := range 256 {
		p := float64(hist[t]) / n
		q1 += p
		m1sum += float64(t) * p
		q2 := 1 - q1
		if q1 <= 0 {
			continue
		}
		if q2 <= 0 {
			break
		}
		d := m1sum/q1 - (mu-m1sum)/q2
		if s := q1 * q2 * d * d; s > best {
			best, bestT = s, t
		}
	}
	return bestT
}

// Threshold turns g into a 0/255 mask. With above set, pixels brighter than t become 255
// (OpenCV's THRESH_BINARY); otherwise the darker ones do (THRESH_BINARY_INV).
func Threshold(g *Gray, t int, above bool) *Gray {
	out := NewGray(g.W, g.H)
	for i, v := range g.Pix {
		if (int(v) > t) == above {
			out.Pix[i] = 255
		}
	}
	return out
}

// Run is one horizontal, centre-symmetric row of a structuring element: DY rows from the
// anchor, HalfW pixels either side of it.
type Run struct{ DY, HalfW int }

// SE is a structuring element, one run per row.
type SE []Run

// EllipseSE builds the elliptical structuring element OpenCV's
// getStructuringElement(MORPH_ELLIPSE, (size, size)) produces.
func EllipseSE(size int) SE {
	r := size / 2
	se := make(SE, 0, size)
	for i := range size {
		dy := i - r
		if abs(dy) > r {
			continue
		}
		dx := 0
		if r > 0 {
			dx = int(math.Round(float64(r) * math.Sqrt(float64(r*r-dy*dy)/float64(r*r))))
		}
		se = append(se, Run{DY: dy, HalfW: dx})
	}
	return se
}

// Dilate and Erode apply a structuring element. Outside the image counts as empty for
// dilation and as set for erosion, so the border is left alone (OpenCV's default).
func Dilate(g *Gray, se SE) *Gray { return morph(g, se, true) }
func Erode(g *Gray, se SE) *Gray  { return morph(g, se, false) }

// Close fills gaps (dilate, then erode): it bridges the graph-paper lines crossing a part.
func Close(g *Gray, se SE) *Gray { return Erode(Dilate(g, se), se) }

// Open drops speckle (erode, then dilate).
func Open(g *Gray, se SE) *Gray { return Dilate(Erode(g, se), se) }

// morph runs a min/max filter over se. Each row of se is a centred horizontal window, so
// the horizontal pass is shared by every row of the same width and only has to be done
// once per distinct width.
func morph(g *Gray, se SE, dilate bool) *Gray {
	cache := map[int][]uint8{}
	type layer struct {
		dy  int
		pix []uint8
	}
	layers := make([]layer, 0, len(se))
	for _, r := range se {
		if _, ok := cache[r.HalfW]; !ok {
			cache[r.HalfW] = hMorph(g, r.HalfW, dilate)
		}
		layers = append(layers, layer{dy: r.DY, pix: cache[r.HalfW]})
	}
	// Row by row: each output row is the max (or min) of the matching rows of every layer,
	// which runs over contiguous memory. Rows outside the image contribute nothing.
	w := g.W
	out := NewGray(g.W, g.H)
	for y := range g.H {
		dst := out.Pix[y*w : (y+1)*w]
		if dilate {
			clear(dst) // 0 is the identity for a maximum
		} else {
			for x := range dst {
				dst[x] = 255 // and 255 for a minimum
			}
		}
		for _, l := range layers {
			yy := y + l.dy
			if yy < 0 || yy >= g.H {
				continue
			}
			src := l.pix[yy*w : (yy+1)*w]
			if dilate {
				for x, p := range src {
					if p > dst[x] {
						dst[x] = p
					}
				}
			} else {
				for x, p := range src {
					if p < dst[x] {
						dst[x] = p
					}
				}
			}
		}
	}
	return out
}

// hMorph is a horizontal min/max over a centred window of half-width h, with the
// out-of-image convention of morph.
//
// It uses the van Herk / Gil-Werman trick: cut the row into blocks as wide as the window,
// take a running min/max forward through each block and another backward, and every window
// is then the combination of two of those, because it straddles exactly one block boundary.
// That is a constant three operations per pixel whatever the window size, instead of one
// per pixel of the window, which is what makes a 7x7 element affordable on a big scan.
// combine is the maximum (dilate) or the minimum of two grey levels; small enough to inline.
func combine(a, b uint8, dilate bool) uint8 {
	if dilate {
		return max(a, b)
	}
	return min(a, b)
}

func hMorph(g *Gray, h int, dilate bool) []uint8 {
	out := make([]uint8, g.W*g.H)
	if h <= 0 {
		copy(out, g.Pix)
		return out
	}
	// Padding with the identity element (empty for dilation, set for erosion) gives the
	// border the same treatment as morph's: outside the image changes nothing.
	ident := uint8(255)
	if dilate {
		ident = 0
	}
	k := 2*h + 1
	n := g.W
	blocks := (n + k - 1 + k - 1) / k // enough for n + k - 1 padded samples
	m := blocks * k
	buf := make([]uint8, m)
	pre := make([]uint8, m)
	suf := make([]uint8, m)

	for y := range g.H {
		row := g.Pix[y*n : (y+1)*n]
		for i := range buf {
			buf[i] = ident
		}
		copy(buf[h:], row) // row index j sits at buf index j+h
		for b := range blocks {
			s := b * k
			pre[s] = buf[s]
			for i := 1; i < k; i++ {
				pre[s+i] = combine(pre[s+i-1], buf[s+i], dilate)
			}
			suf[s+k-1] = buf[s+k-1]
			for i := k - 2; i >= 0; i-- {
				suf[s+i] = combine(suf[s+i+1], buf[s+i], dilate)
			}
		}
		// The window for output x covers row [x-h, x+h], which is buf [x, x+k-1].
		dst := out[y*n : (y+1)*n]
		for x := range dst {
			dst[x] = combine(suf[x], pre[x+k-1], dilate)
		}
	}
	return out
}

// RotateNearest rotates an image by deg degrees counter-clockwise about its centre,
// keeping the same size, with nearest-neighbour sampling and black outside.
func RotateNearest(g *Gray, deg float64) *Gray {
	rad := deg * math.Pi / 180
	// The inverse rotation, applied to destination pixels.
	cos, sin := math.Cos(rad), math.Sin(rad)
	cx, cy := float64(g.W)/2, float64(g.H)/2
	out := NewGray(g.W, g.H)
	for y := range g.H {
		dy := float64(y) - cy
		for x := range g.W {
			dx := float64(x) - cx
			sx := int(math.Round(cx + dx*cos - dy*sin))
			sy := int(math.Round(cy + dx*sin + dy*cos))
			if sx >= 0 && sy >= 0 && sx < g.W && sy < g.H {
				out.Pix[y*g.W+x] = g.Pix[sy*g.W+sx]
			}
		}
	}
	return out
}

// ColSums and RowSums collapse an image onto one axis.
func ColSums(g *Gray) []float64 {
	s := make([]float64, g.W)
	for y := range g.H {
		row := g.Pix[y*g.W : (y+1)*g.W]
		for x, v := range row {
			s[x] += float64(v)
		}
	}
	return s
}

func RowSums(g *Gray) []float64 {
	s := make([]float64, g.H)
	for y := range g.H {
		var t float64
		for _, v := range g.Pix[y*g.W : (y+1)*g.W] {
			t += float64(v)
		}
		s[y] = t
	}
	return s
}

// Scale shrinks a colour image by an integer-free box filter to fit within max pixels on
// its long edge. It returns the image unchanged when it already fits, plus the factor
// applied (destination pixels per source pixel).
func Scale(c *RGB, maxEdge int) (*RGB, float64) {
	long := max(c.W, c.H)
	if maxEdge <= 0 || long <= maxEdge {
		return c, 1
	}
	f := float64(maxEdge) / float64(long)
	w, h := max(int(float64(c.W)*f), 1), max(int(float64(c.H)*f), 1)
	out := &RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for y := range h {
		y0, y1 := y*c.H/h, max((y+1)*c.H/h, y*c.H/h+1)
		for x := range w {
			x0, x1 := x*c.W/w, max((x+1)*c.W/w, x*c.W/w+1)
			var sr, sg, sb, n int
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					i := (sy*c.W + sx) * 3
					sr, sg, sb, n = sr+int(c.Pix[i]), sg+int(c.Pix[i+1]), sb+int(c.Pix[i+2]), n+1
				}
			}
			i := (y*w + x) * 3
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = uint8(sr/n), uint8(sg/n), uint8(sb/n)
		}
	}
	return out, float64(w) / float64(c.W)
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}
