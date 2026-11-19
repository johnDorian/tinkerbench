package parts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLocate(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.scad": "use <parts.scad>\n// part(\"ghost\") is only a comment\n  part (  'lid' )  cube(1);\npart(\"body\") cube(2);\nfeature(\"M3 hole\", [1,2,3]);\nspart(\"apart\") x();\n",
		"parts.scad": "module helper() { part(\"lid\") sphere(1); }\n",
	})
	cases := []struct {
		kind, name, file string
		line         int
	}{
		{"part", "lid", "main.scad", 3},    // main.scad is searched first; single quotes and spacing
		{"part", "body", "main.scad", 4},   //
		{"part", "ghost", "", 0},           // only in a comment
		{"part", "apart", "", 0},           // "spart(" is not part(
		{"feature", "M3 hole", "main.scad", 5},
		{"part", "missing", "", 0},
		{"part", "", "", 0},
	}
	for _, c := range cases {
		file, line := Locate(dir, c.kind, c.name)
		if file != c.file || line != c.line {
			t.Errorf("Locate(%s, %q) = %q:%d, want %q:%d", c.kind, c.name, file, line, c.file, c.line)
		}
	}
	// a part declared only in another file is found there
	dir2 := writeFiles(t, map[string]string{"main.scad": "use <x.scad>;\n", "x.scad": "a();\npart(\"arm\") b();\n"})
	if file, line := Locate(dir2, "part", "arm"); file != "x.scad" || line != 2 {
		t.Fatalf("arm = %q:%d", file, line)
	}
}

func TestWrapperEscapesName(t *testing.T) {
	dir := t.TempDir()
	if err := writeWrapper(dir, "wb_0.scad", `it's "q" \x`); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "wb_0.scad"))
	want := "$only_part = \"it's \\\"q\\\" \\\\x\";\ninclude <main.scad>\n"
	if string(b) != want {
		t.Fatalf("wrapper = %q", b)
	}
}

func TestPrepareWorkCopiesSourcesAndWritesWrappers(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.scad": "part(\"a\") cube(1);\n", "lib.scad": "x=1;\n", "notes.txt": "no"})
	work := filepath.Join(t.TempDir(), "w")
	if err := prepareWork(dir, work, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"main.scad", "lib.scad", "wb_0.scad", "wb_1.scad", "wb_rest.scad"} {
		if _, err := os.Stat(filepath.Join(work, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "notes.txt")); err == nil {
		t.Error("non-scad files must not be copied")
	}
	b, _ := os.ReadFile(filepath.Join(work, "wb_rest.scad"))
	if !strings.Contains(string(b), `$only_part = "__rest__";`) {
		t.Fatalf("rest wrapper = %q", b)
	}
}

func tri(a, b, c [3]float64) render.Triangle { return render.Triangle{a, b, c} }

// A large square in the plane z = 10 with its normal pointing up, and a vertical wall at x = 0.
var testMesh = []render.Triangle{
	tri([3]float64{0, 0, 10}, [3]float64{20, 0, 10}, [3]float64{20, 20, 10}),
	tri([3]float64{0, 0, 10}, [3]float64{20, 20, 10}, [3]float64{0, 20, 10}),
	tri([3]float64{0, 0, 0}, [3]float64{0, 20, 0}, [3]float64{0, 20, 10}),
}

func TestFits(t *testing.T) {
	up := [3]float64{0, 0, 1}
	down := [3]float64{0, 0, -1}
	cases := []struct {
		name string
		a    project.Anchor
		want bool
	}{
		{"face on top", project.Anchor{Kind: "face", Point: [3]float64{10, 10, 10.5}, Normal: &up}, true},
		{"face wrong normal", project.Anchor{Kind: "face", Point: [3]float64{10, 10, 10.5}, Normal: &down}, false},
		{"face 2 mm above", project.Anchor{Kind: "face", Point: [3]float64{10, 10, 12}, Normal: &up}, false},
		{"point 0.9 mm off the top", project.Anchor{Kind: "point", Point: [3]float64{10, 10, 10.9}}, true},
		{"point far away", project.Anchor{Kind: "point", Point: [3]float64{100, 100, 100}}, false},
		{"point beside the top's edge", project.Anchor{Kind: "point", Point: [3]float64{21, 0, 10}}, true},
		{"point on the wall", project.Anchor{Kind: "point", Point: [3]float64{0.5, 10, 5}}, true},
		{"point outside the square", project.Anchor{Kind: "point", Point: [3]float64{25, 25, 10}}, false},
	}
	for _, c := range cases {
		if got := Fits(testMesh, c.a); got != c.want {
			t.Errorf("%s: Fits = %v, want %v", c.name, got, c.want)
		}
	}
	if Fits(nil, project.Anchor{Kind: "point"}) {
		t.Error("no triangles: nothing fits")
	}
}
