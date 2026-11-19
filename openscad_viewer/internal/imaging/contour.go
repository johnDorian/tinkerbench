package imaging

// Point is a pixel coordinate.
type Point struct{ X, Y int }

// Blob is one connected run of set pixels in a mask.
type Blob struct {
	Area    int     // set pixels
	Outline []Point // the outer border, traced clockwise; holes are not included
}

// Blobs finds the 8-connected set regions of a 0/255 mask and traces the outer border of
// each, which is what OpenCV's findContours(RETR_EXTERNAL, CHAIN_APPROX_NONE) returns.
// They come back largest first.
func Blobs(mask *Gray) []Blob {
	seen := make([]bool, len(mask.Pix))
	stack := make([]int32, 0, 1024)
	var out []Blob
	for y := range mask.H {
		for x := range mask.W {
			i := y*mask.W + x
			if mask.Pix[i] == 0 || seen[i] {
				continue
			}
			// Scanning top to bottom and left to right, the first pixel reached is the
			// topmost-leftmost one, so its left neighbour is outside the blob: the right
			// place to start a border walk.
			area := 0
			stack = append(stack[:0], int32(i))
			seen[i] = true
			for len(stack) > 0 {
				p := int(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
				area++
				px, py := p%mask.W, p/mask.W
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						nx, ny := px+dx, py+dy
						if !mask.In(nx, ny) {
							continue
						}
						n := ny*mask.W + nx
						if mask.Pix[n] != 0 && !seen[n] {
							seen[n] = true
							stack = append(stack, int32(n))
						}
					}
				}
			}
			out = append(out, Blob{Area: area, Outline: trace(mask, Point{x, y})})
		}
	}
	// insertion sort: there are rarely more than a handful of blobs worth keeping
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Area > out[j-1].Area; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// clockwise neighbour offsets, starting east, with y running down the image
var nb = [8]Point{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}

// trace walks the outer border of the blob that start belongs to (Moore neighbourhood
// tracing). start must be the blob's topmost-leftmost pixel, so that the pixel to its west
// is outside it.
func trace(mask *Gray, start Point) []Point {
	set := func(p Point) bool { return mask.In(p.X, p.Y) && mask.Pix[p.Y*mask.W+p.X] != 0 }
	p := start
	back := Point{start.X - 1, start.Y} // outside the blob by construction
	out := []Point{start}
	firstBack := back
	limit := 4*len(mask.Pix) + 8 // a border cannot be longer than this; guards against a cycle
	for range limit {
		k := 0
		for i, d := range nb { // where the backtrack pixel sits relative to p
			if p.X+d.X == back.X && p.Y+d.Y == back.Y {
				k = i
				break
			}
		}
		moved := false
		for j := 1; j <= 8; j++ {
			d := (k + j) % 8
			c := Point{p.X + nb[d].X, p.Y + nb[d].Y}
			if !set(c) {
				continue
			}
			prev := (d + 7) % 8 // the neighbour checked just before c
			back = Point{p.X + nb[prev].X, p.Y + nb[prev].Y}
			p = c
			moved = true
			break
		}
		if !moved {
			return out // a single isolated pixel
		}
		if p == start && back == firstBack {
			return out // back where we began, approaching the same way: the border is closed
		}
		out = append(out, p)
	}
	return out
}

// Area is the polygon area of an outline (the shoelace formula).
func Area(pts []Point) float64 {
	if len(pts) < 3 {
		return 0
	}
	var s float64
	for i, p := range pts {
		q := pts[(i+1)%len(pts)]
		s += float64(p.X)*float64(q.Y) - float64(q.X)*float64(p.Y)
	}
	if s < 0 {
		s = -s
	}
	return s / 2
}
