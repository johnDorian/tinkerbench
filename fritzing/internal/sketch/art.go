package sketch

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Fritzing's exporter writes each part into the view SVG again, and for some artwork it gets that
// wrong: the WeMos D1 mini draws its pin rings in a group with rotate(90 cx cy), the export drops
// the transform, and the rings land in a row beside the board. Wires still end on the pin the part
// definition names, so the picture lies. AuditArt compares where each connector is drawn in the
// rendered view with where our own reading of the part's SVG puts it.

const artToleranceMM = 1.0

// partGroup returns the part's group from a rendered view SVG, wrapped into a document of its own,
// in the view's own units (1000 per inch). Fritzing names the group partID='<modelIndex>0'.
func partGroup(rendered []byte, partID string) []byte {
	dec := xml.NewDecoder(bytes.NewReader(rendered))
	dec.Strict = false
	depth, start := 0, int64(-1)
	for {
		before := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if start < 0 {
				if t.Name.Local == "g" {
					for _, a := range t.Attr {
						if a.Name.Local == "partID" && a.Value == partID {
							start, depth = before, 1
						}
					}
				}
				continue
			}
			depth++
		case xml.EndElement:
			if start >= 0 {
				depth--
				if depth == 0 {
					body := rendered[start:dec.InputOffset()]
					return append(append([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1px" height="1px" viewBox="0 0 1 1">`), body...), []byte(`</svg>`)...)
				}
			}
		}
	}
}

// renderedUnitToPx converts the exporter's 1000 units per inch to scene px.
const renderedUnitToPx = pxPerInch / 1000.0

var rotateRe = regexp.MustCompile(`rotate\(`)

// artPin is one connector seen both ways: where the part puts it and where the export draws it.
type artPin struct {
	part         inst
	name         string
	got, want    [2]float64 // scene px
	module, view string
}

// AuditArt checks every part of the view against its rendered picture and returns one issue per
// part whose connectors are drawn somewhere else than the part says. Fritzing crops an exported
// view to the drawing, so every part is shifted by the same amount; that shared offset (the most
// common displacement of all pins) is removed before pins are compared.
func (s *Sketch) AuditArt(res Resolver, view string, rendered []byte) []LayoutIssue {
	var out []LayoutIssue
	if !validView(view) || res == nil || len(rendered) == 0 {
		return out
	}
	e := s.editor(res)
	var pins []artPin
	checked := map[string]int{}
	order := []inst{}
	for _, i := range s.all() {
		if i.isWire || isBase(i.module, i.title) {
			continue
		}
		inf, _, mt, ok := e.placed(i, view)
		if !ok {
			continue
		}
		f, _, err := res.FZP(i.module)
		if err != nil {
			continue
		}
		grp := partGroup(rendered, strconv.Itoa(i.index*10))
		if grp == nil {
			continue
		}
		drawn, ok := ParseSVG(grp)
		if !ok {
			continue
		}
		order = append(order, i)
		for _, c := range f.Connectors {
			cp := c.Pins(view)
			if len(cp) == 0 || cp[0].SvgID == "" {
				continue
			}
			id := cp[0].SvgID
			want, ok1 := inf.elementBox(id)
			got, ok2 := drawn.elementBox(id)
			if !ok1 || !ok2 {
				continue
			}
			wr := aabb(mt, (want.minX-inf.vbX)*inf.sx, (want.minY-inf.vbY)*inf.sy, (want.maxX-inf.vbX)*inf.sx, (want.maxY-inf.vbY)*inf.sy)
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = c.ID
			}
			pins = append(pins, artPin{part: i, name: name, module: i.module, view: view,
				got:  [2]float64{(got.minX + got.maxX) / 2 * renderedUnitToPx, (got.minY + got.maxY) / 2 * renderedUnitToPx},
				want: [2]float64{(wr.x0 + wr.x1) / 2, (wr.y0 + wr.y1) / 2}})
			checked[i.title]++
		}
	}
	if len(pins) == 0 {
		return out
	}

	// the shared offset: the displacement most pins agree on (bins of 1 mm)
	bin := mmToPx(1)
	type cell struct{ x, y int }
	count := map[cell][]int{}
	for k, p := range pins {
		c := cell{int(math.Round((p.got[0] - p.want[0]) / bin)), int(math.Round((p.got[1] - p.want[1]) / bin))}
		count[c] = append(count[c], k)
	}
	var best []int
	for _, members := range count {
		if len(members) > len(best) {
			best = members
		}
	}
	var ox, oy float64
	if len(best) >= 2 {
		for _, k := range best {
			ox += pins[k].got[0] - pins[k].want[0]
			oy += pins[k].got[1] - pins[k].want[1]
		}
		ox, oy = ox/float64(len(best)), oy/float64(len(best))
	}

	type miss struct {
		name         string
		dist         float64
		gotX, gotY   float64
		wantX, wantY float64
	}
	bad := map[string][]miss{}
	for _, p := range pins {
		gx, gy := p.got[0]-ox, p.got[1]-oy
		if d := math.Hypot(gx-p.want[0], gy-p.want[1]); d > mmToPx(artToleranceMM) {
			bad[p.part.title] = append(bad[p.part.title], miss{p.name, d, gx, gy, p.want[0], p.want[1]})
		}
	}
	for _, i := range order {
		ms := bad[i.title]
		if len(ms) == 0 {
			continue
		}
		sort.Slice(ms, func(a, b int) bool { return ms[a].dist > ms[b].dist })
		w := ms[0]
		text := fmt.Sprintf("%s (%s, %s view): %d of %d connectors are drawn away from where the part puts them, e.g. %s is drawn at (%.1f, %.1f) mm but sits at (%.1f, %.1f) mm, %.1f mm off. Wires still end on the real pin, so the picture is wrong, not the circuit.",
			i.title, i.module, view, len(ms), checked[i.title], w.name, mm(w.gotX), mm(w.gotY), mm(w.wantX), mm(w.wantY), mm(w.dist))
		if svg, err := res.SVG(i.module, view); err == nil && rotateRe.Match(svg) {
			text += " The artwork uses rotate() in groups, which Fritzing's export drops: flatten them into matrix(...) in an imported copy of the part."
		}
		out = append(out, LayoutIssue{LevelWarn, "art-mismatch", text})
	}
	return out
}
