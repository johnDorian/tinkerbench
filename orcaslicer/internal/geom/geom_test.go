package geom

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// square tris: a flat-ish box from (x0,y0,z0) with size s
func box(x0, y0, z0, s float64) []Triangle {
	v := func(x, y, z float64) Vec3 { return Vec3{x, y, z} }
	return []Triangle{{v(x0, y0, z0), v(x0+s, y0, z0), v(x0, y0+s, z0+s)}, {v(x0+s, y0, z0), v(x0+s, y0+s, z0+s), v(x0, y0+s, z0+s)}}
}

func TestPlaceCentresFootprintAndDropsToBed(t *testing.T) {
	tris := box(200, 10, 5, 20)
	tr, b := Place(tris, Pose{Scale: 1}, 120, 80)
	if !near((b.Min[0]+b.Max[0])/2, 120) || !near((b.Min[1]+b.Max[1])/2, 80) {
		t.Fatalf("footprint centre = %v, want (120, 80)", b.Center())
	}
	if !near(b.Min[2], 0) {
		t.Fatalf("lowest point z = %v, want 0", b.Min[2])
	}
	// the transform really maps the points there
	for _, tri := range tris {
		for _, v := range tri {
			p := tr.Apply(v)
			if p[0] < b.Min[0]-1e-9 || p[0] > b.Max[0]+1e-9 || p[2] < -1e-9 {
				t.Fatalf("vertex %v maps outside the placed box: %v", v, p)
			}
		}
	}
}

func TestRotationOrderAndScale(t *testing.T) {
	// a 10 mm bar along X, rotated 90° about Z: it should lie along Y
	bar := []Triangle{{{0, 0, 0}, {10, 0, 0}, {0, 0, 1}}}
	_, b := Place(bar, Pose{Rotation: [3]float64{0, 0, 90}, Scale: 1}, 0, 0)
	size := b.Size()
	if !near(size[0], 0) || !near(size[1], 10) {
		t.Fatalf("rotated size = %v, want (0, 10, _): the bar is zero-thick in X", size)
	}
	// scale 2 doubles it
	_, b = Place(bar, Pose{Scale: 2}, 0, 0)
	if !near(b.Size()[0], 20) {
		t.Fatalf("scaled width = %v, want 20", b.Size()[0])
	}
	// X is applied before Z: a point (0,1,0) rotated 90 about X becomes (0,0,1); then 90 about Z leaves it
	R := Rotation([3]float64{90, 0, 90})
	p := R.Apply(Vec3{0, 1, 0})
	if !near(p[0], 0) || !near(p[1], 0) || !near(p[2], 1) {
		t.Fatalf("Rotation(90,0,90)·(0,1,0) = %v, want (0,0,1)", p)
	}
}

func TestBoundsEmpty(t *testing.T) {
	if (Bounds(nil) != Box{}) {
		t.Fatal("empty bounds should be zero")
	}
}
