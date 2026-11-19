package models

import (
	"errors"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func TestCreateListReplaceDelete(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Create(Input{Name: "Xmas Light Case", Source: "upload", Data: testkit.ASCIICube([3]float64{1, 2, 3}, 20)})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || m.Name != "Xmas Light Case" || m.Size != [3]float64{20, 20, 20} || m.Min != [3]float64{1, 2, 3} || m.Triangles != 12 {
		t.Fatalf("model = %+v", m)
	}
	if _, err := s.Create(Input{Name: "bad", Data: []byte("nope")}); !errors.Is(err, ErrBadModel) {
		t.Fatalf("bad data err = %v, want ErrBadModel", err)
	}

	os := &Input{Name: "proj", Source: "openscad", OpenSCADProject: "proj", OpenSCADVersion: "3", Data: testkit.Cube([3]float64{}, 10)}
	o, err := s.Create(*os)
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.FindOpenSCAD("proj")
	if err != nil || found.ID != o.ID {
		t.Fatalf("FindOpenSCAD = %+v, %v", found, err)
	}
	// re-import: same id, new geometry, mesh cache follows the checksum
	tris1, _, _ := s.Mesh(o.ID)
	r, err := s.Replace(o.ID, Input{Name: "proj", Source: "openscad", OpenSCADProject: "proj", Data: testkit.Cube([3]float64{}, 30)})
	if err != nil || r.ID != o.ID || r.Size != [3]float64{30, 30, 30} {
		t.Fatalf("replace = %+v, %v", r, err)
	}
	tris2, _, _ := s.Mesh(o.ID)
	if len(tris1) != 12 || len(tris2) != 12 || tris2[0][1][0] != 30 {
		t.Fatalf("mesh cache returned stale geometry")
	}

	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if _, err := s.Get("../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid id should be not found, got %v", err)
	}
}
