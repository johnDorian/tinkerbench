package sketch

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

type pt = [2]float64

// facing is the direction a pin leaves its part: the unit vector towards the nearest edge of the
// part's box ({0,0} for parts without a clear side, such as breadboards).
func facing(p lpart, x, y float64) pt {
	if p.base {
		return pt{}
	}
	d := []float64{x - p.box.x0, p.box.x1 - x, y - p.box.y0, p.box.y1 - y}
	dirs := []pt{{-1, 0}, {1, 0}, {0, -1}, {0, 1}}
	best := 0
	for k := range d {
		if d[k] < d[best] {
			best = k
		}
	}
	return dirs[best]
}

// simplify drops repeated points and joints on a straight line; ok is false when the route
// turns back on itself.
func simplify(p []pt) (out []pt, ok bool) {
	const e = 0.05
	for _, q := range p {
		if len(out) > 0 && math.Abs(out[len(out)-1][0]-q[0]) < e && math.Abs(out[len(out)-1][1]-q[1]) < e {
			continue
		}
		out = append(out, q)
	}
	for k := 1; k+1 < len(out); {
		a, b, c := out[k-1], out[k], out[k+1]
		cross := (b[0]-a[0])*(c[1]-b[1]) - (b[1]-a[1])*(c[0]-b[0])
		dot := (b[0]-a[0])*(c[0]-b[0]) + (b[1]-a[1])*(c[1]-b[1])
		if math.Abs(cross) < 1e-6 {
			if dot < 0 {
				return nil, false
			}
			out = append(out[:k], out[k+1:]...)
			continue
		}
		k++
	}
	return out, len(out) >= 2
}

type routeScore struct {
	viol      int
	why       []string
	crossings int
	length    float64
	bends     int
}

func (m *lmodel) score(p []pt, own map[string]bool, nets map[string]bool) routeScore {
	var sc routeScore
	sc.bends = len(p) - 2
	for k := 0; k+1 < len(p); k++ {
		g := seg{x1: p[k][0], y1: p[k][1], x2: p[k+1][0], y2: p[k+1][1]}
		sc.length += g.length()
		for _, part := range m.parts {
			if part.base {
				continue
			}
			for c, r := range part.pins {
				pin := part.in.title + "." + c
				if own[pin] || nets[m.net(pin)] {
					continue
				}
				if r.hitBySegment(g.x1, g.y1, g.x2, g.y2) {
					sc.viol++
					sc.why = append(sc.why, "runs over "+pinName(m.res, part.in, c))
				}
			}
			if own["part:"+part.in.title] {
				if crossesBody(part, g) {
					sc.viol++
					sc.why = append(sc.why, "cuts through "+part.in.title)
				}
			} else if part.box.hitBySegment(g.x1, g.y1, g.x2, g.y2) {
				sc.viol++
				sc.why = append(sc.why, "crosses "+part.in.title)
			}
		}
		for _, w := range m.wires {
			if nets[w.net] {
				continue
			}
			if _, _, ok := intersect(g, w); ok {
				sc.crossings++
			} else if overlapCollinear(g, w) {
				sc.viol++
				sc.why = append(sc.why, "runs on top of "+w.title)
			}
		}
	}
	return sc
}

// overlapCollinear: two axis-aligned segments on the same line that share more than 0.5 mm.
func overlapCollinear(a, b seg) bool {
	const e = 0.2
	min, max := math.Min, math.Max
	if math.Abs(a.x1-a.x2) < e && math.Abs(b.x1-b.x2) < e && math.Abs(a.x1-b.x1) < e {
		return min(max(a.y1, a.y2), max(b.y1, b.y2))-max(min(a.y1, a.y2), min(b.y1, b.y2)) > mmToPx(0.5)
	}
	if math.Abs(a.y1-a.y2) < e && math.Abs(b.y1-b.y2) < e && math.Abs(a.y1-b.y1) < e {
		return min(max(a.x1, a.x2), max(b.x1, b.x2))-max(min(a.x1, a.x2), min(b.x1, b.x2)) > mmToPx(0.5)
	}
	return false
}

// planRoute picks the waypoints (mm) of an orthogonal route between two pins: horizontal and
// vertical segments that leave each pin on the side it faces. The cheapest legal route wins
// (fewest crossings, then shortest, then fewest bends); with no legal route it says what is in
// the way.
func (s *Sketch) planRoute(res Resolver, view string, ia inst, pa pinPoint, ib inst, pb pinPoint) ([]XY, error) {
	m := s.model(res, view)
	own := map[string]bool{ia.title + "." + pa.connector: true, ib.title + "." + pb.connector: true}
	nets := map[string]bool{m.net(ia.title + "." + pa.connector): true, m.net(ib.title + "." + pb.connector): true}
	var da, db pt
	for _, p := range m.parts {
		if p.in.index == ia.index {
			da = facing(p, pa.x, pa.y)
		}
		if p.in.index == ib.index {
			db = facing(p, pb.x, pb.y)
		}
	}
	margin := mmToPx(2.54)
	A, B := pt{pa.x, pa.y}, pt{pb.x, pb.y}
	// the end parts may be crossed only near their own pins
	endOwn := map[string]bool{"part:" + ia.title: true, "part:" + ib.title: true}
	for k := range own {
		endOwn[k] = true
	}

	var xs, ys []float64
	grid := mmToPx(1.27)
	snap := func(v float64) float64 { return math.Round(v/grid) * grid }
	add := func(l *[]float64, v ...float64) {
		for _, x := range v {
			*l = append(*l, snap(x))
		}
	}
	add(&xs, (A[0]+B[0])/2, A[0]-margin, A[0]+margin, B[0]-margin, B[0]+margin)
	add(&ys, (A[1]+B[1])/2, A[1]-margin, A[1]+margin, B[1]-margin, B[1]+margin)
	for _, p := range m.parts {
		if !p.base {
			add(&xs, p.box.x0-margin, p.box.x1+margin)
			add(&ys, p.box.y0-margin, p.box.y1+margin)
		}
	}

	candidates := func(stubbed bool) [][]pt {
		P, Q := A, B
		var head, tail []pt
		if stubbed && da != (pt{}) {
			P = pt{A[0] + da[0]*margin, A[1] + da[1]*margin}
			head = []pt{A}
		}
		if stubbed && db != (pt{}) {
			Q = pt{B[0] + db[0]*margin, B[1] + db[1]*margin}
			tail = []pt{B}
		}
		var mids [][]pt
		mids = append(mids, []pt{P, Q})
		mids = append(mids, []pt{P, {Q[0], P[1]}, Q}, []pt{P, {P[0], Q[1]}, Q})
		for _, x := range xs {
			mids = append(mids, []pt{P, {x, P[1]}, {x, Q[1]}, Q})
		}
		for _, y := range ys {
			mids = append(mids, []pt{P, {P[0], y}, {Q[0], y}, Q})
		}
		var out [][]pt
		for _, mid := range mids {
			full := append(append(append([]pt{}, head...), mid...), reverse(tail)...)
			if p, ok := simplify(full); ok {
				if !orthogonal(p) {
					continue
				}
				if da != (pt{}) && stubbed && !leaves(p[0], p[1], da) {
					continue
				}
				if db != (pt{}) && stubbed && !leaves(p[len(p)-1], p[len(p)-2], db) {
					continue
				}
				out = append(out, p)
			}
		}
		return out
	}

	type best struct {
		p  []pt
		sc routeScore
	}
	var legal *best
	var worst *best
	for _, stubbed := range []bool{true, false} {
		for _, c := range candidates(stubbed) {
			sc := m.score(c, endOwn, nets)
			if sc.viol > 0 {
				if worst == nil || sc.viol < worst.sc.viol {
					worst = &best{c, sc}
				}
				continue
			}
			if legal == nil || less(sc, legal.sc) {
				legal = &best{c, sc}
			}
		}
		if legal != nil {
			break
		}
	}
	if legal == nil {
		reason := "no candidate route"
		if worst != nil {
			seen := map[string]bool{}
			var w []string
			for _, r := range worst.sc.why {
				if !seen[r] {
					seen[r] = true
					w = append(w, r)
				}
			}
			reason = "the best try " + strings.Join(w, ", ")
		}
		return nil, fmt.Errorf("no legal orthogonal route from %s.%s to %s.%s: %s; move a part or give via waypoints",
			ia.title, pa.connector, ib.title, pb.connector, reason)
	}
	var via []XY
	for _, p := range legal.p[1 : len(legal.p)-1] {
		via = append(via, XY{X: round2(pxToMM(p[0])), Y: round2(pxToMM(p[1]))})
	}
	return via, nil
}

func less(a, b routeScore) bool {
	if a.crossings != b.crossings {
		return a.crossings < b.crossings
	}
	if math.Abs(a.length-b.length) > 0.5 {
		return a.length < b.length
	}
	return a.bends < b.bends
}

func reverse(p []pt) []pt {
	out := make([]pt, len(p))
	for i, q := range p {
		out[len(p)-1-i] = q
	}
	return out
}

func orthogonal(p []pt) bool {
	for k := 0; k+1 < len(p); k++ {
		if math.Abs(p[k][0]-p[k+1][0]) > 0.05 && math.Abs(p[k][1]-p[k+1][1]) > 0.05 {
			return false
		}
	}
	return true
}

// leaves: the segment from a towards b goes in direction d.
func leaves(a, b, d pt) bool {
	return (b[0]-a[0])*d[0]+(b[1]-a[1])*d[1] > 0
}

// checkVia refuses waypoints that sit on a pin: the joint would short it.
func (s *Sketch) checkVia(res Resolver, view string, via []XY) error {
	if len(via) == 0 {
		return nil
	}
	m := s.model(res, view)
	for _, v := range via {
		x, y := mmToPx(v.X), mmToPx(v.Y)
		for _, p := range m.parts {
			if p.base {
				continue
			}
			for c, r := range p.pins {
				if x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1 {
					return fmt.Errorf("waypoint (%.1f, %.1f) mm lies on %s (pin-contact): move it", v.X, v.Y, pinName(res, p.in, c))
				}
			}
		}
	}
	return nil
}

// ---- chains: re-route and tidy ----

type chainEnd struct {
	part, connector, layer string
}

// chainOfWire returns the wires of the chain w belongs to.
func (s *Sketch) chainOfWire(w inst) []inst {
	seen := map[int]bool{w.index: true}
	out := []inst{w}
	for k := 0; k < len(out); k++ {
		for _, n := range s.wireNeighbors(out[k]) {
			if !seen[n.index] {
				seen[n.index] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// chainEnds finds the two parts' connectors a chain is plugged into, its colour and pcb side.
func (s *Sketch) chainEnds(w inst) (ends []chainEnd, view, color, side string, err error) {
	byIdx := s.byIndex()
	view = wireView(w.wireFlags())
	for _, c := range s.chainOfWire(w) {
		vn := c.viewNode(view)
		if vn == nil {
			continue
		}
		if x := vn.Child("wireExtras"); x != nil && color == "" {
			color = x.Attr("color")
		}
		if vn.Attr("layer") == "copper1trace" {
			side = "top"
		}
		for _, cn := range vn.Child("connectors").kids() {
			for _, cc := range cn.Child("connects").kids() {
				idx, _ := strconv.Atoi(cc.Attr("modelIndex"))
				if t, ok := byIdx[idx]; ok && !t.isWire {
					ends = append(ends, chainEnd{t.title, cc.Attr("connectorId"), cc.Attr("layer")})
				}
			}
		}
	}
	if len(ends) != 2 {
		return nil, view, "", "", fmt.Errorf("%s is not plugged into a part at both ends (%d ends): it cannot be re-routed", w.title, len(ends))
	}
	return ends, view, color, side, nil
}

// RerouteWire replaces a wire or chain by a new route between the same two connectors, keeping its
// colour. style is "orthogonal" (default when no via is given) or "straight"; via are explicit
// waypoints in mm. Returns the titles of the new wires.
func (s *Sketch) RerouteWire(res Resolver, ref, style string, via []XY) ([]string, error) {
	w, err := s.find(ref)
	if err != nil {
		return nil, err
	}
	if !w.isWire {
		return nil, fmt.Errorf("%s is a part, not a wire", ref)
	}
	if style == "" && len(via) == 0 {
		style = "orthogonal"
	}
	ends, view, color, side, err := s.chainEnds(w)
	if err != nil {
		return nil, err
	}
	backup := s.String()
	if _, err := s.RemoveOpt(w.title, false); err != nil {
		return nil, err
	}
	o := ConnectOptions{Via: via, Style: style, Side: side}
	if view == parts.Breadboard {
		o.Color = color
	}
	titles, err := s.Connect(res, view, Endpoint{ends[0].part, ends[0].connector}, Endpoint{ends[1].part, ends[1].connector}, o)
	if err != nil {
		if old, perr := Parse(backup); perr == nil {
			s.root = old.root
		}
		return nil, err
	}
	return titles, nil
}

// LayoutSummary is the numbers of a LayoutReport without the part list.
type LayoutSummary struct {
	Wires         int     `json:"wires"`
	Bends         int     `json:"bends"`
	Crossings     int     `json:"crossings"`
	Errors        int     `json:"errors"`
	Warnings      int     `json:"warnings"`
	WireLengthMM  float64 `json:"wire_length_mm"`
	OrthogonalPct float64 `json:"orthogonal_pct"`
	PartsWidthMM  float64 `json:"parts_width_mm"`
	PartsHeightMM float64 `json:"parts_height_mm"`
	Tidiness      float64 `json:"tidiness"`
}

func (r LayoutReport) Summary() LayoutSummary {
	return LayoutSummary{r.Wires, r.Bends, r.Crossings, r.Count(LevelError), r.Count(LevelWarn), r.WireLength, r.OrthogonalPct, r.BBoxW, r.BBoxH, r.Tidiness}
}

// TidyResult is what TidyView did (or would do).
type TidyResult struct {
	View     string        `json:"view"`
	Before   LayoutSummary `json:"before"`
	After    LayoutSummary `json:"after"`
	Rerouted []string      `json:"rerouted"`
	Failed   []string      `json:"failed,omitempty"` // wires left as they were, with the reason
	Issues   []string      `json:"issues,omitempty"` // findings after tidying
}

// TidyView re-routes every wire of a view orthogonally, shortest first. Part positions never change.
func (s *Sketch) TidyView(res Resolver, view string) (TidyResult, error) {
	if !validView(view) {
		return TidyResult{}, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	before := s.CheckLayout(res, view)
	out := TidyResult{View: view, Before: before.Summary(), Rerouted: []string{}}
	m := s.model(res, view)
	type item struct {
		ref string
		len float64
	}
	var items []item
	for _, c := range m.chains {
		l := 0.0
		for _, i := range c.segs {
			l += m.wires[i].length()
		}
		items = append(items, item{c.id, l})
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].len < items[b].len })
	for _, it := range items {
		titles, err := s.RerouteWire(res, it.ref, "orthogonal", nil)
		if err != nil {
			out.Failed = append(out.Failed, it.ref+": "+err.Error())
			continue
		}
		out.Rerouted = append(out.Rerouted, strings.Join(titles, "+"))
	}
	after := s.CheckLayout(res, view)
	out.After = after.Summary()
	out.Issues = after.Brief()
	return out, nil
}
