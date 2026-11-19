package render

import (
	"encoding/binary"
	"math"
	"testing"
)

// cube returns the 12 triangles of an axis-aligned cube from 0 to a, outward facing.
func cube(a float32) [][3][3]float32 {
	v := func(x, y, z float32) [3]float32 { return [3]float32{x * a, y * a, z * a} }
	q := func(p0, p1, p2, p3 [3]float32) [][3][3]float32 { return [][3][3]float32{{p0, p1, p2}, {p0, p2, p3}} }
	var t [][3][3]float32
	t = append(t, q(v(0, 0, 0), v(0, 1, 0), v(1, 1, 0), v(1, 0, 0))...) // bottom
	t = append(t, q(v(0, 0, 1), v(1, 0, 1), v(1, 1, 1), v(0, 1, 1))...) // top
	t = append(t, q(v(0, 0, 0), v(1, 0, 0), v(1, 0, 1), v(0, 0, 1))...) // front
	t = append(t, q(v(0, 1, 0), v(0, 1, 1), v(1, 1, 1), v(1, 1, 0))...) // back
	t = append(t, q(v(0, 0, 0), v(0, 0, 1), v(0, 1, 1), v(0, 1, 0))...) // left
	t = append(t, q(v(1, 0, 0), v(1, 1, 0), v(1, 1, 1), v(1, 0, 1))...) // right
	return t
}

func TestReadSTLBinary(t *testing.T) {
	tris := cube(10)
	b := make([]byte, 84+50*len(tris))
	binary.LittleEndian.PutUint32(b[80:], uint32(len(tris)))
	for i, tri := range tris {
		off := 84 + 50*i + 12
		for v := range 3 {
			for c := range 3 {
				binary.LittleEndian.PutUint32(b[off+12*v+4*c:], math.Float32bits(tri[v][c]))
			}
		}
	}
	s, err := ReadSTL(b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Triangles != 12 || s.Size != [3]float64{10, 10, 10} || s.VolumeMM3 != 1000 {
		t.Fatalf("stats = %+v", s)
	}
}

func TestReadSTLASCII(t *testing.T) {
	src := `solid x
facet normal 0 0 1
 outer loop
  vertex 0 0 0
  vertex 4 0 0
  vertex 0 3 2
 endloop
endfacet
endsolid x`
	s, err := ReadSTL([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Triangles != 1 || s.Size != [3]float64{4, 3, 2} {
		t.Fatalf("stats = %+v", s)
	}
}

func TestParseLog(t *testing.T) {
	log := "ECHO: 5\nWARNING: Ignoring unknown variable 'x' in file /d/p/main.scad, line 3\nERROR: Parser error in file \"/d/p/main.scad\", line 5: syntax error\n"
	errs, warns, echo := parseLog(log, "/d/p")
	if len(errs) != 1 || errs[0] != `ERROR: Parser error in file "main.scad", line 5: syntax error` {
		t.Fatalf("errs = %q", errs)
	}
	if len(warns) != 1 || len(echo) != 1 {
		t.Fatalf("warns = %q echo = %q", warns, echo)
	}
}
