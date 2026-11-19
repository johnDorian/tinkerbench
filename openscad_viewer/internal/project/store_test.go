package project

import (
	"errors"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("Bad Name", "", "", "t"); err == nil {
		t.Fatal("want error for invalid name")
	}
	info, err := s.Create("box", "a box", "cube(10);\ncube(10);\n", "t")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 1 || info.Files[0] != MainFile || info.Description != "a box" {
		t.Fatalf("info = %+v", info)
	}
	if _, err := s.Create("box", "", "", "t"); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}

	if _, err := s.EditFile("box", MainFile, "cube(10);", "cube(20);", false, "t", ""); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("ambiguous edit: %v", err)
	}
	if _, err := s.EditFile("box", MainFile, "sphere", "x", false, "t", ""); err == nil {
		t.Fatal("want not-found error")
	}
	if _, err := s.EditFile("box", MainFile, "cube(10);", "cube(20);", true, "agent", "bigger"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.ReadFile("box", MainFile); c != "cube(20);\ncube(20);\n" {
		t.Fatalf("content = %q", c)
	}

	if _, err := s.WriteFile("box", "lib.scad", "module m() {}", "t", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteFile("box", "../x.scad", "", "t", ""); err == nil {
		t.Fatal("want error for path traversal")
	}
	if _, err := s.DeleteFile("box", MainFile, "t"); err == nil {
		t.Fatal("main.scad must not be deletable")
	}
	if _, err := s.DeleteFile("box", "lib.scad", "t"); err != nil {
		t.Fatal(err)
	}

	vs, _ := s.History("box", "")
	if len(vs) != 4 || vs[1].Source != "agent" || vs[1].Note != "bigger" || !vs[3].Deleted {
		t.Fatalf("history = %+v", vs)
	}
	if _, c, err := s.GetVersion("box", 1); err != nil || c != "cube(10);\ncube(10);\n" {
		t.Fatalf("version 1 = %q, %v", c, err)
	}
	if _, _, err := s.GetVersion("box", 4); err == nil {
		t.Fatal("deleted version has no content")
	}

	params := map[string]string{"size": "30"}
	if info, err = s.UpdateMeta("box", nil, params); err != nil || info.Params["size"] != "30" || info.Description != "a box" {
		t.Fatalf("meta = %+v, %v", info, err)
	}

	if err := s.Delete("box"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("list after delete = %+v", list)
	}
}
