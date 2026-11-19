package stl

import (
	"math"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func TestParseBinaryAndASCIIAgree(t *testing.T) {
	bin, err := Parse(testkit.Cube([3]float64{10, 20, 30}, 5))
	if err != nil {
		t.Fatal(err)
	}
	asc, err := Parse(testkit.ASCIICube([3]float64{10, 20, 30}, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(bin) != 12 || len(asc) != 12 {
		t.Fatalf("triangles: binary %d, ascii %d, want 12", len(bin), len(asc))
	}
	b1, b2 := geom.Bounds(bin), geom.Bounds(asc)
	if b1 != b2 {
		t.Fatalf("bounds differ: %v vs %v", b1, b2)
	}
	if b1.Min != (geom.Vec3{10, 20, 30}) || b1.Size() != (geom.Vec3{5, 5, 5}) {
		t.Fatalf("bounds = %v", b1)
	}
}

func TestBinaryWithSolidHeader(t *testing.T) {
	data := testkit.Cube([3]float64{0, 0, 0}, 1)
	copy(data, []byte("solid exported by some tool"))
	tris, err := Parse(data)
	if err != nil || len(tris) != 12 {
		t.Fatalf("got %d triangles, err %v", len(tris), err)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("hello, not a model")); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Parse(testkit.Cube([3]float64{}, 1)[:200]); err == nil {
		t.Fatal("truncated binary should fail")
	}
	bad := []byte("solid x\nfacet normal 0 0 0\nouter loop\nvertex 1 2\nendloop\nendfacet\nendsolid\n")
	if _, err := Parse(bad); err == nil {
		t.Fatal("short vertex line should fail")
	}
	nan := []byte("solid x\nfacet normal 0 0 0\nouter loop\nvertex NaN 0 0\nvertex 1 0 0\nvertex 0 1 0\nendloop\nendfacet\nendsolid\n")
	if _, err := Parse(nan); err == nil || math.IsNaN(0) {
		t.Fatal("NaN coordinates should fail")
	}
}
