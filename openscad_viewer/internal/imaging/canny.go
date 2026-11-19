package imaging

// Canny finds edges the way OpenCV's Canny(src, low, high, apertureSize=3) does: a 3x3
// Sobel, an L1 gradient magnitude, non-maximum suppression across the gradient, then
// hysteresis between the two thresholds. The result is a 0/255 edge map.
func Canny(g *Gray, low, high int) *Gray {
	if low > high {
		low, high = high, low
	}
	w, h := g.W, g.H
	dx := make([]int32, w*h)
	dy := make([]int32, w*h)
	at := func(x, y int) int32 { // Sobel replicates the border, as OpenCV's Canny does
		return int32(g.Pix[clampi(y, 0, h-1)*w+clampi(x, 0, w-1)])
	}
	mag := make([]int32, w*h)
	for y := range h {
		for x := range w {
			a, b, c := at(x-1, y-1), at(x, y-1), at(x+1, y-1)
			d, f := at(x-1, y), at(x+1, y)
			i, j, k := at(x-1, y+1), at(x, y+1), at(x+1, y+1)
			gx := (c + 2*f + k) - (a + 2*d + i)
			gy := (i + 2*j + k) - (a + 2*b + c)
			dx[y*w+x], dy[y*w+x] = gx, gy
			mag[y*w+x] = abs32(gx) + abs32(gy)
		}
	}

	// Non-maximum suppression. The gradient direction is quantised into horizontal,
	// vertical and the two diagonals by comparing |dy| against tan(22.5) and tan(67.5)
	// times |dx|, which is how OpenCV picks the pair of neighbours to compare against.
	const tg22, tg67 = 0.41421356237309503, 2.414213562373095
	strong := make([]bool, w*h)
	weak := make([]bool, w*h)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			i := y*w + x
			m := mag[i]
			if int(m) <= low {
				continue
			}
			ax, ay := float64(abs32(dx[i])), float64(abs32(dy[i]))
			var a, b int32
			switch {
			case ay < tg22*ax: // gradient runs across the image: compare left and right
				a, b = mag[i-1], mag[i+1]
			case ay > tg67*ax: // compare up and down
				a, b = mag[i-w], mag[i+w]
			default: // diagonal; which one depends on the signs
				s := 1
				if (dx[i] < 0) != (dy[i] < 0) {
					s = -1
				}
				a, b = mag[i-w-s], mag[i+w+s]
			}
			if m > a && m >= b {
				if int(m) > high {
					strong[i] = true
				} else {
					weak[i] = true
				}
			}
		}
	}

	// Hysteresis: keep the weak maxima that touch a strong edge, directly or through
	// other weak ones.
	out := NewGray(w, h)
	stack := make([]int32, 0, 1024)
	for i, s := range strong {
		if s {
			out.Pix[i] = 255
			stack = append(stack, int32(i))
		}
	}
	for len(stack) > 0 {
		i := int(stack[len(stack)-1])
		stack = stack[:len(stack)-1]
		x, y := i%w, i/w
		for ddy := -1; ddy <= 1; ddy++ {
			for ddx := -1; ddx <= 1; ddx++ {
				nx, ny := x+ddx, y+ddy
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				n := ny*w + nx
				if weak[n] && out.Pix[n] == 0 {
					out.Pix[n] = 255
					stack = append(stack, int32(n))
				}
			}
		}
	}
	return out
}

func clampi(v, lo, hi int) int { return min(max(v, lo), hi) }

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
