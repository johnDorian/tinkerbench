package imaging

// SquareMorph is a grey-level minimum (dilate=false) or maximum (dilate=true) over a
// (2r+1)-square window. A square is separable: a horizontal pass over the rows and then a
// vertical pass, each a van Herk sweep, so the cost does not grow with r.
func SquareMorph(g *Gray, r int, dilate bool) *Gray {
	if r <= 0 {
		return g.Clone()
	}
	rows := &Gray{W: g.W, H: g.H, Pix: hMorph(g, r, dilate)}
	return vMorph(rows, r, dilate)
}

// GreyClose is the grey-level closing over a square of radius r: a maximum then a minimum.
// Dark details narrower than the window are filled in while the edges of large dark areas
// stay where they were, which is how dots and grid lines are removed from a dark part.
func GreyClose(g *Gray, r int) *Gray {
	return SquareMorph(SquareMorph(g, r, true), r, false)
}

// vMorph is hMorph down the columns. It works on whole rows at a time, so memory is read
// along its length: the van Herk blocks run over rows, each step is a pass over a row, and the
// window for each output row is the suffix of one block and the prefix of the next.
// Outside the image the identity is used, as in hMorph.
func vMorph(g *Gray, r int, dilate bool) *Gray {
	W, H := g.W, g.H
	out := NewGray(W, H)
	ident := uint8(255)
	if dilate {
		ident = 0
	}
	k := 2*r + 1
	// Padded rows: r identity rows, the image, r identity rows, rounded up to whole blocks.
	blocks := (H + 2*r + k - 1) / k
	Q := blocks * k
	src := make([]uint8, Q*W)
	for i := range src {
		src[i] = ident
	}
	copy(src[r*W:], g.Pix)
	pre := make([]uint8, Q*W)
	suf := make([]uint8, Q*W)
	for b := range blocks {
		s := b * k
		copy(pre[s*W:(s+1)*W], src[s*W:(s+1)*W])
		for i := 1; i < k; i++ {
			p := (s + i) * W
			q := p - W
			for x := range W {
				pre[p+x] = combine(pre[q+x], src[p+x], dilate)
			}
		}
		e := (s + k - 1) * W
		copy(suf[e:e+W], src[e:e+W])
		for i := k - 2; i >= 0; i-- {
			p := (s + i) * W
			q := p + W
			for x := range W {
				suf[p+x] = combine(suf[q+x], src[p+x], dilate)
			}
		}
	}
	for y := range H {
		a := y * W         // padded row y: the top of the window
		b := (y + 2*r) * W // padded row y+2r: the bottom of the window
		dst := out.Pix[y*W : (y+1)*W]
		sa := suf[a : a+W]
		pb := pre[b : b+W]
		for x := range W {
			dst[x] = combine(sa[x], pb[x], dilate)
		}
	}
	return out
}

// binaryMorph dilates (dilate) or erodes a 0/255 mask by a (2r+1) square, counting instead of
// comparing: a dilation keeps a pixel if any set pixel lies in its window, an erosion if none
// of the window is unset. Outside the image counts as empty for a dilation and as set for an
// erosion, as in SquareMorph. The count runs along each row and then down the columns, so the
// cost is a few passes whatever r is.
func binaryMorph(g *Gray, r int, dilate bool) *Gray {
	W, H := g.W, g.H
	out := NewGray(W, H)
	if r <= 0 {
		copy(out.Pix, g.Pix)
		return out
	}
	// counted reports whether a pixel is one the window counts.
	counted := func(v uint8) bool { return (v != 0) == dilate }
	hs := make([]uint16, W*H)
	for y := range H {
		row := g.Pix[y*W : (y+1)*W]
		h := hs[y*W : (y+1)*W]
		cnt := 0
		for x := 0; x < min(r+1, W); x++ {
			if counted(row[x]) {
				cnt++
			}
		}
		for x := range W {
			h[x] = uint16(cnt)
			if x+r+1 < W && counted(row[x+r+1]) {
				cnt++
			}
			if x-r >= 0 && counted(row[x-r]) {
				cnt--
			}
		}
	}
	col := make([]int32, W)
	add := func(y int, sign int32) {
		h := hs[y*W : (y+1)*W]
		for x, v := range h {
			col[x] += sign * int32(v)
		}
	}
	for y := 0; y < min(r, H); y++ {
		add(y, 1)
	}
	for y := range H {
		if y+r < H {
			add(y+r, 1)
		}
		if y-r-1 >= 0 {
			add(y-r-1, -1)
		}
		dst := out.Pix[y*W : (y+1)*W]
		for x, c := range col {
			if (dilate && c > 0) || (!dilate && c == 0) {
				dst[x] = 255
			}
		}
	}
	return out
}

// SquareOpen drops binary specks narrower than a (2r+1) square: an erosion then a dilation.
// The mask is 0 or 255.
func SquareOpen(g *Gray, r int) *Gray {
	return binaryMorph(binaryMorph(g, r, false), r, true)
}

// SquareClose fills binary gaps narrower than a (2r+1) square: a dilation then an erosion.
// The mask is 0 or 255.
func SquareClose(g *Gray, r int) *Gray {
	return binaryMorph(binaryMorph(g, r, true), r, false)
}

// SquareDilate and SquareErode are the binary dilation and erosion by a (2r+1) square.
func SquareDilate(g *Gray, r int) *Gray { return binaryMorph(g, r, true) }

// SquareErode is the binary erosion by a (2r+1) square.
func SquareErode(g *Gray, r int) *Gray { return binaryMorph(g, r, false) }

// Otsu3 returns the two thresholds that best split the histogram into three classes:
// v <= t1 (the darkest), t1 < v <= t2 (the middle) and v > t2 (the lightest).
func Otsu3(g *Gray) (t1, t2 int) {
	var hist [256]float64
	for _, v := range g.Pix {
		hist[v]++
	}
	n := float64(len(g.Pix))
	// cumulative probability P and weighted sum S over levels below i
	var P, S [257]float64
	for i := range 256 {
		p := hist[i] / n
		P[i+1] = P[i] + p
		S[i+1] = S[i] + float64(i)*p
	}
	mu := S[256]
	best := -1.0
	for a := range 255 {
		for b := a + 1; b < 255; b++ {
			w0, w1, w2 := P[a+1], P[b+1]-P[a+1], 1-P[b+1]
			if w0 <= 0 || w1 <= 0 || w2 <= 0 {
				continue
			}
			m0, m1, m2 := S[a+1]/w0, (S[b+1]-S[a+1])/w1, (mu-S[b+1])/w2
			s := w0*(m0-mu)*(m0-mu) + w1*(m1-mu)*(m1-mu) + w2*(m2-mu)*(m2-mu)
			if s > best {
				best, t1, t2 = s, a, b
			}
		}
	}
	return t1, t2
}
