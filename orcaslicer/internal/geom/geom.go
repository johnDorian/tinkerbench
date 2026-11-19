// Package geom is the 3D maths behind putting a model on the bed: rotations in the order the
// API documents, the bounds of a mesh and the transform that places it. Placed meshes are
// written to the 3MF in bed coordinates, so this is the only place that decides positions.
package geom

import "math"

// Vec3 is a point or a direction in millimetres.
type Vec3 [3]float64

// Triangle is three vertices.
type Triangle [3]Vec3

// Mat3 is a row-major 3x3 matrix.
type Mat3 [3][3]float64

// Box is an axis-aligned box.
type Box struct{ Min, Max Vec3 }

// Size returns the box's extent along each axis.
func (b Box) Size() Vec3 {
	return Vec3{b.Max[0] - b.Min[0], b.Max[1] - b.Min[1], b.Max[2] - b.Min[2]}
}

// Center returns the middle of the box.
func (b Box) Center() Vec3 {
	return Vec3{(b.Min[0] + b.Max[0]) / 2, (b.Min[1] + b.Max[1]) / 2, (b.Min[2] + b.Max[2]) / 2}
}

// Apply returns m·v.
func (m Mat3) Apply(v Vec3) Vec3 {
	var out Vec3
	for i := 0; i < 3; i++ {
		out[i] = m[i][0]*v[0] + m[i][1]*v[1] + m[i][2]*v[2]
	}
	return out
}

// Mul returns m·n.
func (m Mat3) Mul(n Mat3) Mat3 {
	var out Mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				out[i][j] += m[i][k] * n[k][j]
			}
		}
	}
	return out
}

func rotX(a float64) Mat3 {
	c, s := math.Cos(a), math.Sin(a)
	return Mat3{{1, 0, 0}, {0, c, -s}, {0, s, c}}
}

func rotY(a float64) Mat3 {
	c, s := math.Cos(a), math.Sin(a)
	return Mat3{{c, 0, s}, {0, 1, 0}, {-s, 0, c}}
}

func rotZ(a float64) Mat3 {
	c, s := math.Cos(a), math.Sin(a)
	return Mat3{{c, -s, 0}, {s, c, 0}, {0, 0, 1}}
}

// Rotation returns Rz·Ry·Rx for angles in degrees about X, Y and Z: the X rotation is applied
// first, then Y, then Z.
func Rotation(deg [3]float64) Mat3 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	return rotZ(rad(deg[2])).Mul(rotY(rad(deg[1]))).Mul(rotX(rad(deg[0])))
}

// Pose is how a model is turned and scaled before it goes on the bed.
type Pose struct {
	Rotation [3]float64 // degrees about X, Y, Z, applied in that order
	Scale    float64    // uniform factor
}

// Transform maps model coordinates to bed coordinates: world = A·p + B.
type Transform struct {
	A Mat3
	B Vec3
}

// Apply maps one model point to the bed.
func (t Transform) Apply(p Vec3) Vec3 {
	q := t.A.Apply(p)
	return Vec3{q[0] + t.B[0], q[1] + t.B[1], q[2] + t.B[2]}
}

// Bounds returns the box of the vertices of tris.
func Bounds(tris []Triangle) Box {
	if len(tris) == 0 {
		return Box{}
	}
	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, t := range tris {
		for _, v := range t {
			for i := 0; i < 3; i++ {
				lo[i] = math.Min(lo[i], v[i])
				hi[i] = math.Max(hi[i], v[i])
			}
		}
	}
	return Box{Min: lo, Max: hi}
}

// Place puts a mesh on the bed. It is turned and scaled about the centre of its own bounding
// box, then moved so that the centre of its footprint is (x, y) and its lowest point is at z=0.
// It returns the transform from model to bed coordinates and the placed box.
func Place(tris []Triangle, pose Pose, x, y float64) (Transform, Box) {
	scale := pose.Scale
	if scale == 0 {
		scale = 1
	}
	A := Rotation(pose.Rotation).Mul(Mat3{{scale, 0, 0}, {0, scale, 0}, {0, 0, scale}})
	c := Bounds(tris).Center()

	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, t := range tris {
		for _, v := range t {
			q := A.Apply(Vec3{v[0] - c[0], v[1] - c[1], v[2] - c[2]})
			for i := 0; i < 3; i++ {
				lo[i] = math.Min(lo[i], q[i])
				hi[i] = math.Max(hi[i], q[i])
			}
		}
	}
	// shift: after A·(p-c), move the footprint centre to (x, y) and the bottom to z=0
	shift := Vec3{x - (lo[0]+hi[0])/2, y - (lo[1]+hi[1])/2, -lo[2]}
	ac := A.Apply(c)
	tr := Transform{A: A, B: Vec3{shift[0] - ac[0], shift[1] - ac[1], shift[2] - ac[2]}}
	box := Box{
		Min: Vec3{lo[0] + shift[0], lo[1] + shift[1], lo[2] + shift[2]},
		Max: Vec3{hi[0] + shift[0], hi[1] + shift[1], hi[2] + shift[2]},
	}
	return tr, box
}
