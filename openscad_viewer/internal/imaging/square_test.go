package imaging

import (
	"math/rand"
	"testing"
)

// bruteSquare is the reference: the min or max over every (2r+1)-square window, with the
// morph conventions for the image edge.
func bruteSquare(g *Gray, r int, dilate bool) *Gray {
	out := NewGray(g.W, g.H)
	for y := range g.H {
		for x := range g.W {
			v := uint8(255)
			if dilate {
				v = 0
			}
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					xx, yy := x+dx, y+dy
					var p uint8
					switch {
					case xx < 0 || yy < 0 || xx >= g.W || yy >= g.H:
						p = 255
						if dilate {
							p = 0
						}
					default:
						p = g.At(xx, yy)
					}
					if dilate {
						v = max(v, p)
					} else {
						v = min(v, p)
					}
				}
			}
			out.Set(x, y, v)
		}
	}
	return out
}

func TestSquareMorphMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, dims := range [][2]int{{9, 7}, {23, 14}, {5, 31}} {
		g := NewGray(dims[0], dims[1])
		for i := range g.Pix {
			g.Pix[i] = uint8(rng.Intn(256))
		}
		for _, r := range []int{1, 2, 4} {
			for _, dilate := range []bool{true, false} {
				want := bruteSquare(g, r, dilate)
				got := SquareMorph(g, r, dilate)
				for i := range want.Pix {
					if want.Pix[i] != got.Pix[i] {
						t.Fatalf("grey %dx%d r=%d dilate=%v: pixel %d = %d, want %d", dims[0], dims[1], r, dilate, i, got.Pix[i], want.Pix[i])
					}
				}
				// binary: the same window on a 0/255 mask
				b := NewGray(g.W, g.H)
				for i, v := range g.Pix {
					if v > 128 {
						b.Pix[i] = 255
					}
				}
				wantB := bruteSquare(b, r, dilate)
				gotB := binaryMorph(b, r, dilate)
				for i := range wantB.Pix {
					if wantB.Pix[i] != gotB.Pix[i] {
						t.Fatalf("binary %dx%d r=%d dilate=%v: pixel %d differs", dims[0], dims[1], r, dilate, i)
					}
				}
			}
		}
	}
}
