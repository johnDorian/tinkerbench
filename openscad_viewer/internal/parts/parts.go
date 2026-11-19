// Package parts turns the part() and feature() calls of a model into a listing: which named
// pieces there are, where they are in the source, their bounding boxes, and one STL per
// piece. Per-part renders run in the background (manager.go); this file has the pieces that
// don't run openscad.
//
// On disk: out/parts.json (the listing) and out/parts/<index>.stl, where index is the
// position in the listing. The unlabelled remainder, if any, is the last entry.
package parts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// Unlabelled is the name of the geometry outside every part.
const Unlabelled = "(unlabelled)"

// Box is a bounding box in model mm.
type Box struct {
	Min [3]float64 `json:"min"`
	Max [3]float64 `json:"max"`
}

// Part is one named piece. Ok is false while its render runs (Error empty), or when it failed.
type Part struct {
	Name      string `json:"name"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	BBox      *Box   `json:"bbox"` // null until the part is rendered
	Triangles int    `json:"triangles"`
}

// Feature is a named point of the model.
type Feature struct {
	Name string     `json:"name"`
	At   [3]float64 `json:"at"`
	File string     `json:"file"`
	Line int        `json:"line"`
}

// Listing is what GET /parts returns and what out/parts.json stores. ModelTime is the mtime
// of the model.stl the parts belong to.
type Listing struct {
	ModelTime *time.Time `json:"model_time"`
	Pending   bool       `json:"pending"`
	Parts     []Part     `json:"parts"`
	Features  []Feature  `json:"features"`
}

// Locate finds the first line of the project's .scad files that declares part("name") (kind
// "part") or feature("name") (kind "feature"). Single or double quotes, any spacing; the main
// file is searched first. It returns "", 0 when the name isn't written literally (for
// example when it is built from a variable).
func Locate(dir, kind, name string) (string, int) {
	if name == "" {
		return "", 0
	}
	re := regexp.MustCompile(`\b` + kind + `\s*\(\s*(?:"` + regexp.QuoteMeta(name) + `"|'` + regexp.QuoteMeta(name) + `')`)
	for _, f := range scadFiles(dir) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if k := strings.Index(line, "//"); k >= 0 {
				line = line[:k] // a call in a comment doesn't count
			}
			if re.MatchString(line) {
				return f, i + 1
			}
		}
	}
	return "", 0
}

// scadFiles lists the .scad files of a project dir, main.scad first.
func scadFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".scad") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a == project.MainFile) != (b == project.MainFile) {
			return a == project.MainFile
		}
		return a < b
	})
	return out
}

// ListingPath is out/parts.json of a project dir.
func ListingPath(dir string) string { return filepath.Join(render.OutDir(dir), "parts.json") }

// PartPath is the STL of the listing entry at index.
func PartPath(dir string, index int) string {
	return filepath.Join(render.OutDir(dir), "parts", strconv.Itoa(index)+".stl")
}

// Read returns the stored listing of a project dir, or nil when there is none yet.
func Read(dir string) (*Listing, error) {
	b, err := os.ReadFile(ListingPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var l Listing
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// write stores a listing; the caller holds the manager's lock.
func write(dir string, l Listing) error {
	if err := os.MkdirAll(render.OutDir(dir), 0o755); err != nil {
		return err
	}
	if l.Parts == nil {
		l.Parts = []Part{}
	}
	if l.Features == nil {
		l.Features = []Feature{}
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	tmp := ListingPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ListingPath(dir))
}

// Fits reports whether the triangles have geometry under an anchor: some triangle within 1 mm
// of the point and, for a face with a normal, also facing it (dot product above 0.9).
func Fits(tris []render.Triangle, a project.Anchor) bool {
	const maxDist = 1.0
	var n *[3]float64
	if a.Kind == project.KindFace && a.Normal != nil {
		u := unit(*a.Normal)
		n = &u
	}
	p := a.Point
	for _, t := range tris {
		if !nearBox(t, p, maxDist) {
			continue
		}
		c := closestPoint(p, t)
		if dist(p, c) > maxDist {
			continue
		}
		if n != nil && dot(triNormal(t), *n) <= 0.9 {
			continue
		}
		return true
	}
	return false
}

func nearBox(t render.Triangle, p [3]float64, d float64) bool {
	for k := range 3 {
		lo, hi := t[0][k], t[0][k]
		for v := 1; v < 3; v++ {
			lo = math.Min(lo, t[v][k])
			hi = math.Max(hi, t[v][k])
		}
		if p[k] < lo-d || p[k] > hi+d {
			return false
		}
	}
	return true
}

// closestPoint is the point of triangle t nearest to p (Ericson, Real-Time Collision
// Detection, 5.1.5).
func closestPoint(p [3]float64, t render.Triangle) [3]float64 {
	a, b, c := t[0], t[1], t[2]
	ab, ac, ap := sub(b, a), sub(c, a), sub(p, a)
	d1, d2 := dot(ab, ap), dot(ac, ap)
	if d1 <= 0 && d2 <= 0 {
		return a
	}
	bp := sub(p, b)
	d3, d4 := dot(ab, bp), dot(ac, bp)
	if d3 >= 0 && d4 <= d3 {
		return b
	}
	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 {
		v := d1 / (d1 - d3)
		return add(a, scale(ab, v))
	}
	cp := sub(p, c)
	d5, d6 := dot(ab, cp), dot(ac, cp)
	if d6 >= 0 && d5 <= d6 {
		return c
	}
	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 {
		w := d2 / (d2 - d6)
		return add(a, scale(ac, w))
	}
	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return add(b, scale(sub(c, b), w))
	}
	denom := 1 / (va + vb + vc)
	v, w := vb*denom, vc*denom
	return add(a, add(scale(ab, v), scale(ac, w)))
}

func triNormal(t render.Triangle) [3]float64 {
	n := cross(sub(t[1], t[0]), sub(t[2], t[0]))
	if l := length(n); l > 0 {
		return scale(n, 1/l)
	}
	return n
}

func sub(a, b [3]float64) [3]float64   { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add(a, b [3]float64) [3]float64   { return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func scale(a [3]float64, s float64) [3]float64 {
	return [3]float64{a[0] * s, a[1] * s, a[2] * s}
}
func dot(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func length(a [3]float64) float64 { return math.Sqrt(dot(a, a)) }
func dist(a, b [3]float64) float64 { return length(sub(a, b)) }
func unit(a [3]float64) [3]float64 {
	if l := length(a); l > 0 {
		return scale(a, 1/l)
	}
	return a
}
