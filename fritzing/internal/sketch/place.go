package sketch

import (
	"fmt"
	"math"
	"sort"
)

// SnapMM rounds v (mm) to the grid; a grid of 0 leaves it alone.
func SnapMM(v, grid float64) float64 {
	if grid <= 0 {
		return v
	}
	return round2(math.Round(v/grid) * grid)
}

// shift moves a part by a distance in scene px and drags its wires along.
func (e *editor) shift(i inst, view string, dx, dy float64) {
	g := i.viewNode(view).Child("geometry")
	g.SetAttr("x", num(parseNum(g.Attr("x"))+dx))
	g.SetAttr("y", num(parseNum(g.Attr("y"))+dy))
	e.relayoutWires(i, view)
}

// pinBox is the box around a part's pins (the artwork box if it has none): alignment goes by pins,
// since that is what wires meet.
func (p lpart) pinBox() rect {
	if p.base || len(p.pins) == 0 {
		return p.box
	}
	first := true
	var r rect
	for _, q := range p.pins {
		if first {
			r, first = q, false
			continue
		}
		r = rect{math.Min(r.x0, q.x0), math.Min(r.y0, q.y0), math.Max(r.x1, q.x1), math.Max(r.y1, q.y1)}
	}
	return r
}

// AlignParts lines parts up in one view. mode: left, right, top, bottom, hcenter (same centre x,
// stacked in a column), vcenter (same centre y, a row), distribute_h / distribute_v (equal gaps
// between the outermost two, order by position). The reference is the box of the pins.
func (s *Sketch) AlignParts(res Resolver, view string, titles []string, mode string) error {
	if !validView(view) {
		return fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	if len(titles) < 2 {
		return fmt.Errorf("give at least two parts")
	}
	m := s.model(res, view)
	byTitle := map[string]lpart{}
	for _, p := range m.parts {
		byTitle[p.in.title] = p
	}
	var ps []lpart
	for _, t := range titles {
		p, ok := byTitle[t]
		if !ok {
			return fmt.Errorf("%s is not a part in the %s view", t, view)
		}
		ps = append(ps, p)
	}
	ref := func(p lpart) rect { return p.pinBox() }
	var d map[string][2]float64 = map[string][2]float64{}
	switch mode {
	case "left", "right", "top", "bottom", "hcenter", "vcenter":
		var target float64
		switch mode {
		case "left":
			target = math.Inf(1)
			for _, p := range ps {
				target = math.Min(target, ref(p).x0)
			}
		case "right":
			target = math.Inf(-1)
			for _, p := range ps {
				target = math.Max(target, ref(p).x1)
			}
		case "top":
			target = math.Inf(1)
			for _, p := range ps {
				target = math.Min(target, ref(p).y0)
			}
		case "bottom":
			target = math.Inf(-1)
			for _, p := range ps {
				target = math.Max(target, ref(p).y1)
			}
		case "hcenter", "vcenter":
			for _, p := range ps { // centre of the first part
				r := ref(ps[0])
				_ = p
				if mode == "hcenter" {
					target = (r.x0 + r.x1) / 2
				} else {
					target = (r.y0 + r.y1) / 2
				}
			}
		}
		for _, p := range ps {
			r := ref(p)
			switch mode {
			case "left":
				d[p.in.title] = [2]float64{target - r.x0, 0}
			case "right":
				d[p.in.title] = [2]float64{target - r.x1, 0}
			case "top":
				d[p.in.title] = [2]float64{0, target - r.y0}
			case "bottom":
				d[p.in.title] = [2]float64{0, target - r.y1}
			case "hcenter":
				d[p.in.title] = [2]float64{target - (r.x0+r.x1)/2, 0}
			case "vcenter":
				d[p.in.title] = [2]float64{0, target - (r.y0+r.y1)/2}
			}
		}
	case "distribute_h", "distribute_v":
		if len(ps) < 3 {
			return fmt.Errorf("distribute needs at least three parts")
		}
		h := mode == "distribute_h"
		sort.SliceStable(ps, func(a, b int) bool {
			if h {
				return ref(ps[a]).x0 < ref(ps[b]).x0
			}
			return ref(ps[a]).y0 < ref(ps[b]).y0
		})
		size := func(p lpart) float64 {
			if h {
				return ref(p).x1 - ref(p).x0
			}
			return ref(p).y1 - ref(p).y0
		}
		start := func(p lpart) float64 {
			if h {
				return ref(p).x0
			}
			return ref(p).y0
		}
		total := start(ps[len(ps)-1]) + size(ps[len(ps)-1]) - start(ps[0])
		used := 0.0
		for _, p := range ps {
			used += size(p)
		}
		gap := (total - used) / float64(len(ps)-1)
		pos := start(ps[0])
		for _, p := range ps {
			if h {
				d[p.in.title] = [2]float64{pos - start(p), 0}
			} else {
				d[p.in.title] = [2]float64{0, pos - start(p)}
			}
			pos += size(p) + gap
		}
	default:
		return fmt.Errorf("mode must be left, right, top, bottom, hcenter, vcenter, distribute_h or distribute_v")
	}
	for _, p := range ps {
		if v := d[p.in.title]; v != [2]float64{} {
			m.e.shift(p.in, view, v[0], v[1])
		}
	}
	return nil
}

// PlaceNear puts a part next to another one: side is left, right, above or below, gap in mm
// between the artwork boxes, centred on the anchor and snapped to the 2.54 mm grid (the gap is
// never made smaller by snapping).
func (s *Sketch) PlaceNear(res Resolver, view, part, anchor, side string, gapMM float64) error {
	if !validView(view) {
		return fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	m := s.model(res, view)
	var p, a *lpart
	for k := range m.parts {
		switch m.parts[k].in.title {
		case part:
			p = &m.parts[k]
		case anchor:
			a = &m.parts[k]
		}
	}
	if p == nil || a == nil {
		return fmt.Errorf("%s and %s must both be parts in the %s view", part, anchor, view)
	}
	if p == a {
		return fmt.Errorf("a part cannot be placed near itself")
	}
	grid, gap := mmToPx(2.54), mmToPx(gapMM)
	w, h := p.box.x1-p.box.x0, p.box.y1-p.box.y0
	cx, cy := (a.box.x0+a.box.x1)/2, (a.box.y0+a.box.y1)/2
	x, y := cx-w/2, cy-h/2
	switch side {
	case "right":
		x = math.Ceil((a.box.x1+gap)/grid) * grid
		y = math.Round(y/grid) * grid
	case "left":
		x = math.Floor((a.box.x0-gap-w)/grid) * grid
		y = math.Round(y/grid) * grid
	case "below":
		y = math.Ceil((a.box.y1+gap)/grid) * grid
		x = math.Round(x/grid) * grid
	case "above":
		y = math.Floor((a.box.y0-gap-h)/grid) * grid
		x = math.Round(x/grid) * grid
	default:
		return fmt.Errorf("side must be left, right, above or below")
	}
	m.e.shift(p.in, view, x-p.box.x0, y-p.box.y0)
	return nil
}
