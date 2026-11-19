package sketch

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

// Diff says in words what changed between two states of a sketch, so an edit can be confirmed
// without looking at a picture.
type Diff struct {
	Parts       PartsDiff                 `json:"parts"`
	Connections map[string]ConnectionDiff `json:"connections,omitempty"` // per view: nets joined or split
	Routes      map[string][]string       `json:"routes,omitempty"`      // per view: wires drawn differently
	Layout      map[string]LayoutDiff     `json:"layout,omitempty"`      // per view: numbers and findings
	Unchanged   bool                      `json:"unchanged"`
}

// PartsDiff lists parts by title.
type PartsDiff struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"` // module swapped, property changed, moved, rotated
}

// ConnectionDiff lists nets (groups of joined pins) that appeared or disappeared.
type ConnectionDiff struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// LayoutDiff compares CheckLayout of the two states.
type LayoutDiff struct {
	Before LayoutNumbers `json:"before"`
	After  LayoutNumbers `json:"after"`
	// Findings counts per "level kind" that differ, e.g. "warn crossing": [2, 0].
	Findings map[string][2]int `json:"findings,omitempty"`
	// New are the errors and warnings that exist only after the change; Fixed only before.
	New   []string `json:"new_problems,omitempty"`
	Fixed []string `json:"fixed_problems,omitempty"`
}

// LayoutNumbers are the headline numbers of a layout report.
type LayoutNumbers struct {
	Wires     int     `json:"wires"`
	Bends     int     `json:"bends"`
	Crossings int     `json:"crossings"`
	LengthMM  float64 `json:"wire_length_mm"`
	Tidiness  float64 `json:"tidiness"`
}

func numbers(r LayoutReport) LayoutNumbers {
	return LayoutNumbers{r.Wires, r.Bends, r.Crossings, r.WireLength, r.Tidiness}
}

// Compare diffs two sketches. res resolves parts for both (the current library is used for the
// old one too).
func Compare(res Resolver, before, after *Sketch) Diff {
	d := Diff{Connections: map[string]ConnectionDiff{}, Routes: map[string][]string{}, Layout: map[string]LayoutDiff{}}
	sa, sb := before.Describe(res), after.Describe(res)

	old := map[string]PartInfo{}
	for _, p := range sa.Parts {
		old[p.Title] = p
	}
	seen := map[string]bool{}
	for _, p := range sb.Parts {
		seen[p.Title] = true
		o, ok := old[p.Title]
		if !ok {
			d.Parts.Added = append(d.Parts.Added, fmt.Sprintf("%s (%s)", p.Title, p.ModuleID))
			continue
		}
		d.Parts.Changed = append(d.Parts.Changed, partChanges(o, p)...)
	}
	for _, p := range sa.Parts {
		if !seen[p.Title] {
			d.Parts.Removed = append(d.Parts.Removed, fmt.Sprintf("%s (%s)", p.Title, p.ModuleID))
		}
	}

	for _, v := range parts.Views {
		var cd ConnectionDiff
		na, nb := netSet(sa, v), netSet(sb, v)
		for k := range nb {
			if !na[k] {
				cd.Added = append(cd.Added, k)
			}
		}
		for k := range na {
			if !nb[k] {
				cd.Removed = append(cd.Removed, k)
			}
		}
		sort.Strings(cd.Added)
		sort.Strings(cd.Removed)
		if len(cd.Added)+len(cd.Removed) > 0 {
			d.Connections[v] = cd
		}

		ra, rb := before.routes(res, v), after.routes(res, v)
		var changes []string
		for k, b := range rb {
			a, ok := ra[k]
			switch {
			case !ok:
				changes = append(changes, fmt.Sprintf("new wire %s (%d bends, %.1f mm, %s)", k, b.bends, b.lengthMM, b.color))
			case a.sig != b.sig:
				changes = append(changes, fmt.Sprintf("rerouted %s: %d bends %.1f mm -> %d bends %.1f mm", k, a.bends, a.lengthMM, b.bends, b.lengthMM))
			case a.color != b.color:
				changes = append(changes, fmt.Sprintf("recoloured %s: %s -> %s", k, a.color, b.color))
			}
		}
		for k := range ra {
			if _, ok := rb[k]; !ok {
				changes = append(changes, "removed wire "+k)
			}
		}
		sort.Strings(changes)
		if len(changes) > 0 {
			d.Routes[v] = changes
		}

		la, lb := before.CheckLayout(res, v), after.CheckLayout(res, v)
		ld := LayoutDiff{Before: numbers(la), After: numbers(lb), Findings: map[string][2]int{}}
		count := func(r LayoutReport) map[string]int {
			c := map[string]int{}
			for _, i := range r.Issues {
				c[i.Level+" "+i.Kind]++
			}
			return c
		}
		ca, cb := count(la), count(lb)
		for k := range ca {
			if ca[k] != cb[k] {
				ld.Findings[k] = [2]int{ca[k], cb[k]}
			}
		}
		for k := range cb {
			if ca[k] != cb[k] {
				ld.Findings[k] = [2]int{ca[k], cb[k]}
			}
		}
		ta, tb := problemTexts(la), problemTexts(lb)
		for t := range tb {
			if !ta[t] {
				ld.New = append(ld.New, t)
			}
		}
		for t := range ta {
			if !tb[t] {
				ld.Fixed = append(ld.Fixed, t)
			}
		}
		sort.Strings(ld.New)
		sort.Strings(ld.Fixed)
		if ld.Before != ld.After || len(ld.Findings) > 0 {
			d.Layout[v] = ld
		}
	}
	sort.Strings(d.Parts.Added)
	sort.Strings(d.Parts.Removed)
	sort.Strings(d.Parts.Changed)
	d.Unchanged = len(d.Parts.Added)+len(d.Parts.Removed)+len(d.Parts.Changed)+len(d.Connections)+len(d.Routes)+len(d.Layout) == 0
	return d
}

func partChanges(a, b PartInfo) []string {
	var out []string
	if a.ModuleID != b.ModuleID {
		out = append(out, fmt.Sprintf("%s: part swapped %s -> %s", b.Title, a.ModuleID, b.ModuleID))
	}
	keys := map[string]bool{}
	for k := range a.Properties {
		keys[k] = true
	}
	for k := range b.Properties {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		if a.Properties[k] != b.Properties[k] {
			out = append(out, fmt.Sprintf("%s: %s %q -> %q", b.Title, k, a.Properties[k], b.Properties[k]))
		}
	}
	for _, v := range parts.Views {
		pa, oka := a.Positions[v]
		pb, okb := b.Positions[v]
		switch {
		case oka && okb:
			if math.Hypot(pa.X-pb.X, pa.Y-pb.Y) > 0.05 {
				out = append(out, fmt.Sprintf("%s moved in %s: (%.1f, %.1f) -> (%.1f, %.1f) mm", b.Title, v, pa.X, pa.Y, pb.X, pb.Y))
			}
			if math.Abs(pa.Rotation-pb.Rotation) > 0.05 {
				out = append(out, fmt.Sprintf("%s rotated in %s: %.0f -> %.0f deg", b.Title, v, pa.Rotation, pb.Rotation))
			}
		case okb:
			out = append(out, fmt.Sprintf("%s added to the %s view", b.Title, v))
		case oka:
			out = append(out, fmt.Sprintf("%s removed from the %s view", b.Title, v))
		}
	}
	return out
}

func netSet(s Summary, view string) map[string]bool {
	out := map[string]bool{}
	for _, n := range s.Nets {
		if n.View != view {
			continue
		}
		p := append([]string{}, n.Pins...)
		sort.Strings(p)
		out[strings.Join(p, " + ")] = true
	}
	return out
}

func problemTexts(r LayoutReport) map[string]bool {
	out := map[string]bool{}
	for _, i := range r.Issues {
		if i.Level != LevelInfo {
			out[i.Text] = true
		}
	}
	return out
}

// route describes one wire (a chain of segments) for the diff.
type route struct {
	bends    int
	lengthMM float64
	color    string
	sig      string
}

// routes keys every wire of a view by its two ends, so a re-drawn wire is recognised although the
// titles of its segments changed.
func (s *Sketch) routes(res Resolver, view string) map[string]route {
	out := map[string]route{}
	m := s.model(res, view)
	for _, c := range m.chains {
		ends := append([]string{}, c.ends...)
		sort.Strings(ends)
		key := strings.Join(ends, " - ")
		r := route{bends: len(c.segs) - 1}
		var pts []string
		for _, i := range c.segs {
			w := m.wires[i]
			r.lengthMM += mm(w.length())
			if r.color == "" {
				r.color = w.color
			}
			pts = append(pts, fmt.Sprintf("%.0f,%.0f>%.0f,%.0f", w.x1, w.y1, w.x2, w.y2))
		}
		sort.Strings(pts)
		r.sig = strings.Join(pts, ";")
		r.lengthMM = round2(r.lengthMM)
		out[key] = r
	}
	return out
}
