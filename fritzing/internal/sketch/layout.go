package sketch

import (
	"bytes"
	"fmt"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Layout issue levels.
const (
	LevelError = "error" // can short or break the circuit
	LevelWarn  = "warn"  // looks wrong, may be harmless
	LevelInfo  = "info"  // harmless, reported for tidiness
)

// LayoutIssue is one finding of CheckLayout.
type LayoutIssue struct {
	Level string `json:"level"`
	Kind  string `json:"kind"` // pin-contact, crossing, wire-over-pin, wire-over-part, parts-overlap
	Text  string `json:"text"`
}

// LayoutReport is the result of the geometry checks for one view. Lengths are in mm.
type LayoutReport struct {
	View       string        `json:"view"`
	Issues     []LayoutIssue `json:"issues"`
	Wires      int           `json:"wires"`
	WireLength float64       `json:"wire_length_mm"`
	Crossings  int           `json:"crossings"` // all crossings, harmless ones included
	// softCrossings are different-net crossings that are not an error (breadboard, schematic);
	// they only cost a little tidiness.
	softCrossings int
	// Wires counts connections (a wire with bends is one); Segments the straight pieces.
	Segments      int     `json:"segments"`
	Bends         int     `json:"bends"`
	Diagonals     int     `json:"diagonal_segments"`
	LongWires     int     `json:"long_wires"`
	OrthogonalPct float64 `json:"orthogonal_pct"`
	// BBoxW/H: bounding box of all parts except breadboards and boards, mm.
	BBoxW float64 `json:"parts_width_mm"`
	BBoxH float64 `json:"parts_height_mm"`
	// Tidiness is one number, 0-100, for better or worse: 100 minus 20 per error, 10 per warning,
	// 3 per diagonal segment, 0.5 per bend and 1 per long wire.
	Tidiness float64 `json:"tidiness"`
	// Parts says where everything is, in mm: each part's box and the centre of every pin, so a
	// move can be planned from numbers.
	Parts []LayoutPart `json:"parts"`
}

// LayoutPart is one part's placement in a view (mm, y down).
type LayoutPart struct {
	Title string               `json:"title"`
	X     float64              `json:"x_mm"`
	Y     float64              `json:"y_mm"`
	W     float64              `json:"w_mm"`
	H     float64              `json:"h_mm"`
	Pins  map[string]LayoutPin `json:"pins"` // by pin name (id when unnamed)
}

// LayoutPin is a pin's area: centre and size.
type LayoutPin struct {
	X float64 `json:"x_mm"`
	Y float64 `json:"y_mm"`
	W float64 `json:"w_mm"`
	H float64 `json:"h_mm"`
}

// Errors and Warnings count the issues by level.
func (r LayoutReport) Count(level string) int {
	n := 0
	for _, i := range r.Issues {
		if i.Level == level {
			n++
		}
	}
	return n
}

// rect is an axis-aligned rectangle in scene px.
type rect struct{ x0, y0, x1, y1 float64 }

func (a rect) overlaps(b rect) bool {
	const eps = 0.01 // px; touching edges do not count
	return a.x0 < b.x1-eps && b.x0 < a.x1-eps && a.y0 < b.y1-eps && b.y0 < a.y1-eps
}

// hitBySegment reports whether the segment p-q passes through the rectangle.
func (a rect) hitBySegment(px, py, qx, qy float64) bool {
	// Liang–Barsky clipping
	t0, t1 := 0.0, 1.0
	dx, dy := qx-px, qy-py
	for _, c := range [4][2]float64{{-dx, px - a.x0}, {dx, a.x1 - px}, {-dy, py - a.y0}, {dy, a.y1 - py}} {
		p, q := c[0], c[1]
		if p == 0 {
			if q < 0 {
				return false
			}
			continue
		}
		t := q / p
		if p < 0 {
			if t > t1 {
				return false
			}
			t0 = math.Max(t0, t)
		} else {
			if t < t0 {
				return false
			}
			t1 = math.Min(t1, t)
		}
	}
	return t1-t0 > 1e-9
}

type seg struct {
	title          string
	x1, y1, x2, y2 float64
	ends           [2]string
	net            string
	chain          string
	color          string // breadboard wire colour, lower-case "#rrggbb"; "" when unknown
}

func (g seg) length() float64 { return math.Hypot(g.x2-g.x1, g.y2-g.y1) }

// intersect returns the crossing point of two segments, if they properly cross.
func intersect(a, b seg) (x, y float64, ok bool) {
	d := (a.x2-a.x1)*(b.y2-b.y1) - (a.y2-a.y1)*(b.x2-b.x1)
	if math.Abs(d) < 1e-9 {
		return 0, 0, false // parallel
	}
	t := ((b.x1-a.x1)*(b.y2-b.y1) - (b.y1-a.y1)*(b.x2-b.x1)) / d
	u := ((b.x1-a.x1)*(a.y2-a.y1) - (b.y1-a.y1)*(a.x2-a.x1)) / d
	const e = 1e-6
	if t < e || t > 1-e || u < e || u > 1-e {
		return 0, 0, false // meets only at an end
	}
	return a.x1 + t*(a.x2-a.x1), a.y1 + t*(a.y2-a.y1), true
}

func isBase(module, title string) bool {
	m, t := strings.ToLower(module), strings.ToLower(strings.TrimSpace(title))
	return strings.Contains(m, "breadboard") || strings.Contains(m, "pcb") && strings.Contains(m, "rect") ||
		strings.Contains(t, "breadboard") || strings.HasPrefix(t, "pcb")
}

// partRect is a part's bounding box in a view, from its artwork and transform.
func (e *editor) placed(i inst, view string) (inf *SVGInfo, g *Node, m aff, ok bool) {
	vn := i.viewNode(view)
	if vn == nil || vn.Child("geometry") == nil {
		return nil, nil, m, false
	}
	inf = e.info(i.module, view)
	if inf == nil {
		return nil, nil, m, false
	}
	g = vn.Child("geometry")
	m = identity
	if t := g.Child("transform"); t != nil {
		m = aff{parseNum(t.Attr("m11")), parseNum(t.Attr("m12")), parseNum(t.Attr("m21")), parseNum(t.Attr("m22")), parseNum(t.Attr("m31")), parseNum(t.Attr("m32"))}
	}
	m = mul(aff{1, 0, 0, 1, parseNum(g.Attr("x")), parseNum(g.Attr("y"))}, m)
	return inf, g, m, true
}

func aabb(m aff, x0, y0, x1, y1 float64) rect {
	var b box
	for _, p := range [][2]float64{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
		b.add(m.apply(p[0], p[1]))
	}
	return rect{b.minX, b.minY, b.maxX, b.maxY}
}

// pinRect is the area a connector covers in a view, scene px.
func (e *editor) pinRect(i inst, view, connector string) (rect, bool) {
	inf, _, m, ok := e.placed(i, view)
	if !ok {
		return rect{}, false
	}
	f, _, err := e.res.FZP(i.module)
	if err != nil {
		return rect{}, false
	}
	c, err := f.Connector(connector)
	if err != nil {
		return rect{}, false
	}
	pins := c.Pins(view)
	if len(pins) == 0 || pins[0].SvgID == "" {
		return rect{}, false
	}
	// the connector's own element, plus the whole leg for bendable-leg parts (a wire over the leg
	// is as bad as one over its tip)
	var b box
	found := false
	for _, id := range []string{pins[0].SvgID, pins[0].LegID} {
		if id == "" {
			continue
		}
		if x, ok := inf.elementBox(id); ok {
			b.add(x.minX, x.minY)
			b.add(x.maxX, x.maxY)
			found = true
		}
	}
	if !found {
		return rect{}, false
	}
	return aabb(m, (b.minX-inf.vbX)*inf.sx, (b.minY-inf.vbY)*inf.sy, (b.maxX-inf.vbX)*inf.sx, (b.maxY-inf.vbY)*inf.sy), true
}

func mm(px float64) float64 { return round2(pxToMM(px)) }

// lpart is a part's placement in a view: artwork box and the area of every pin, scene px.
type lpart struct {
	in   inst
	box  rect
	base bool
	pins map[string]rect // connector id -> area
}

// chain is a run of wires joined end to end (bends are ordinary wires whose ends are connected).
type chain struct {
	id    string   // title of the chain's first wire
	segs  []int    // indexes into lmodel.wires
	ends  []string // "Part.connector" at the real ends (parts, not wires)
	net   string
	parts map[string]bool
}

// lmodel is the geometry of one view: parts, nets, and wires grouped into chains.
type lmodel struct {
	e      *editor
	res    Resolver
	view   string
	byIdx  map[int]inst
	parts  []lpart
	netOf  map[string]string
	wires  []seg
	chains []*chain
}

func (m *lmodel) net(pin string) string {
	if n, ok := m.netOf[pin]; ok {
		return n
	}
	return "pin:" + pin
}

func (s *Sketch) model(res Resolver, view string) *lmodel {
	e := s.editor(res)
	m := &lmodel{e: e, res: res, view: view, byIdx: s.byIndex(), netOf: map[string]string{}}
	for k, n := range s.nets(res, m.byIdx) {
		if n.View != view {
			continue
		}
		for _, p := range n.Pins {
			m.netOf[p] = "net" + strconv.Itoa(k)
		}
	}
	for _, i := range s.all() {
		if i.isWire {
			continue
		}
		inf, _, mt, ok := e.placed(i, view)
		if !ok {
			continue
		}
		p := lpart{in: i, box: aabb(mt, 0, 0, inf.W, inf.H), base: isBase(i.module, i.title), pins: map[string]rect{}}
		if f, _, err := res.FZP(i.module); err == nil {
			for _, c := range f.Connectors {
				if r, ok := e.pinRect(i, view, c.ID); ok {
					p.pins[c.ID] = r
				}
			}
		}
		m.parts = append(m.parts, p)
	}
	sort.Slice(m.parts, func(a, b int) bool { return m.parts[a].in.title < m.parts[b].in.title })

	isWire := map[string]bool{}
	for _, i := range s.all() {
		if i.isWire {
			isWire[i.title] = true
		}
	}
	var raw [][2]string // all ends including joints
	for _, w := range s.all() {
		if !w.isWire || wireView(w.wireFlags()) != view {
			continue
		}
		g := w.viewNode(view).Child("geometry")
		if g == nil {
			continue
		}
		x, y := parseNum(g.Attr("x")), parseNum(g.Attr("y"))
		all := s.wireEnds(w, view, m.byIdx)
		sg := seg{title: w.title, x1: x, y1: y, x2: x + parseNum(g.Attr("x2")), y2: y + parseNum(g.Attr("y2"))}
		if x := w.viewNode(view).Child("wireExtras"); x != nil {
			sg.color = strings.ToLower(x.Attr("color"))
		}
		for k, end := range all {
			if t, _, _ := strings.Cut(end, "."); !isWire[t] {
				sg.ends[k] = end
			}
		}
		m.wires = append(m.wires, sg)
		raw = append(raw, all)
	}
	// chains: union wires whose ends meet
	parent := make([]int, len(m.wires))
	for i := range parent {
		parent[i] = i
	}
	var find func(i int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	byTitle := map[string]int{}
	for i, w := range m.wires {
		byTitle[w.title] = i
	}
	for i, all := range raw {
		for _, end := range all {
			t, _, _ := strings.Cut(end, ".")
			if j, ok := byTitle[t]; ok {
				parent[find(i)] = find(j)
			}
		}
	}
	byRoot := map[int]*chain{}
	for i := range m.wires {
		r := find(i)
		c := byRoot[r]
		if c == nil {
			c = &chain{parts: map[string]bool{}}
			byRoot[r] = c
			m.chains = append(m.chains, c)
		}
		c.segs = append(c.segs, i)
	}
	for _, c := range m.chains {
		c.id = m.wires[c.segs[0]].title
		for _, i := range c.segs {
			for _, end := range m.wires[i].ends {
				if end == "" {
					continue
				}
				c.ends = append(c.ends, end)
				t, _, _ := strings.Cut(end, ".")
				c.parts[t] = true
				if c.net == "" {
					c.net = m.net(end)
				}
			}
		}
		for _, i := range c.segs {
			m.wires[i].net = c.net
			m.wires[i].chain = c.id
		}
	}
	return m
}

const (
	longWireBreadboardMM = 60.0
	longWireSchematicMM  = 80.0
	nearPinMM            = 1.0
	diagonalTolDeg       = 0.5
)

const (
	parallelTouchMM  = 1.0 // closer than this and the two wires merge into one stripe
	parallelCloseMM  = 2.0
	parallelMinRunMM = 3.0 // shorter overlaps are just two wires passing a corner
	parallelTolDeg   = 3.0
)

// runsAlong reports whether two segments run (nearly) parallel with an overlap longer than
// parallelMinRunMM; overlap and gap are in px (gap = distance between the two lines).
func runsAlong(a, b seg) (overlap, gap float64, ok bool) {
	la, lb := a.length(), b.length()
	if la < mmToPx(parallelMinRunMM) || lb < mmToPx(parallelMinRunMM) {
		return 0, 0, false
	}
	ux, uy := (a.x2-a.x1)/la, (a.y2-a.y1)/la
	vx, vy := (b.x2-b.x1)/lb, (b.y2-b.y1)/lb
	if math.Abs(ux*vy-uy*vx) > math.Sin(parallelTolDeg*math.Pi/180) {
		return 0, 0, false
	}
	// project b's ends onto a's axis (t along, d across)
	proj := func(x, y float64) (t, d float64) {
		dx, dy := x-a.x1, y-a.y1
		return dx*ux + dy*uy, math.Abs(dx*uy - dy*ux)
	}
	t1, d1 := proj(b.x1, b.y1)
	t2, d2 := proj(b.x2, b.y2)
	lo, hi := math.Max(0, math.Min(t1, t2)), math.Min(la, math.Max(t1, t2))
	if hi-lo < mmToPx(parallelMinRunMM) {
		return 0, 0, false
	}
	return hi - lo, (d1 + d2) / 2, true
}

func (g seg) diagonal() bool {
	if g.length() < mmToPx(0.5) {
		return false
	}
	a := math.Abs(math.Atan2(g.y2-g.y1, g.x2-g.x1)) * 180 / math.Pi // 0..180
	d := math.Min(math.Min(a, math.Abs(a-90)), 180-a)
	return d > diagonalTolDeg
}

func (r rect) grow(d float64) rect { return rect{r.x0 - d, r.y0 - d, r.x1 + d, r.y1 + d} }

// CheckLayout runs the geometry checks on one view: wires crossing, pins touching pins of other
// nets, wires running over foreign pins or parts, and overlapping parts. Wires are straight
// segments, so the intersection tests are exact; part boxes come from the artwork and can be a
// little larger than the pins, which is why box overlaps are warnings and pin contact is an error.
// A chain of wires (a wire with bends) counts as one connection.
func (s *Sketch) CheckLayout(res Resolver, view string) LayoutReport {
	rep := LayoutReport{View: view, Issues: []LayoutIssue{}, Parts: []LayoutPart{}}
	if !validView(view) || res == nil {
		return rep
	}
	m := s.model(res, view)
	ps, wires, net := m.parts, m.wires, m.net

	var bb box
	for _, p := range ps {
		lp := LayoutPart{Title: p.in.title, X: mm(p.box.x0), Y: mm(p.box.y0), W: mm(p.box.x1 - p.box.x0), H: mm(p.box.y1 - p.box.y0), Pins: map[string]LayoutPin{}}
		if !p.base { // a breadboard has hundreds of holes
			bb.add(p.box.x0, p.box.y0)
			bb.add(p.box.x1, p.box.y1)
			for c, r := range p.pins {
				name := strings.TrimPrefix(pinName(res, p.in, c), p.in.title+".")
				if _, dup := lp.Pins[name]; dup {
					name += " [" + c + "]"
				}
				lp.Pins[name] = LayoutPin{X: mm((r.x0 + r.x1) / 2), Y: mm((r.y0 + r.y1) / 2), W: mm(r.x1 - r.x0), H: mm(r.y1 - r.y0)}
			}
		}
		rep.Parts = append(rep.Parts, lp)
	}
	if len(ps) > 0 && bb.maxX > bb.minX {
		rep.BBoxW, rep.BBoxH = mm(bb.maxX-bb.minX), mm(bb.maxY-bb.minY)
	}
	for _, w := range wires {
		rep.WireLength += mm(w.length())
	}
	rep.Segments = len(wires)
	rep.Wires = len(m.chains)
	rep.WireLength = round2(rep.WireLength)

	// bends and diagonals
	for _, c := range m.chains {
		for k := 1; k < len(c.segs); k++ {
			a, b := wires[c.segs[k-1]], wires[c.segs[k]]
			cross := (a.x2-a.x1)*(b.y2-b.y1) - (a.y2-a.y1)*(b.x2-b.x1)
			if math.Abs(cross) > 1e-6*a.length()*b.length() {
				rep.Bends++
			}
		}
	}
	for _, w := range wires {
		if w.diagonal() {
			rep.Diagonals++
			rep.Issues = append(rep.Issues, LayoutIssue{LevelInfo, "diagonal",
				fmt.Sprintf("%s runs diagonally from (%.1f, %.1f) to (%.1f, %.1f) mm", w.title, mm(w.x1), mm(w.y1), mm(w.x2), mm(w.y2))})
		}
	}
	rep.OrthogonalPct = 100
	if len(wires) > 0 {
		rep.OrthogonalPct = round2(100 * float64(len(wires)-rep.Diagonals) / float64(len(wires)))
	}
	limit := map[string]float64{parts.Breadboard: longWireBreadboardMM, parts.Schematic: longWireSchematicMM}[view]
	if limit > 0 {
		for _, c := range m.chains {
			l := 0.0
			for _, i := range c.segs {
				l += mm(wires[i].length())
			}
			if l > limit {
				rep.LongWires++
				rep.Issues = append(rep.Issues, LayoutIssue{LevelInfo, "long-wire",
					fmt.Sprintf("%s is %.0f mm long (over %.0f mm): move its parts closer", c.id, l, limit)})
			}
		}
	}

	// 1. pins of different parts that touch, on different nets
	for a := range ps {
		for b := a + 1; b < len(ps); b++ {
			if ps[a].base || ps[b].base || !ps[a].box.overlaps(ps[b].box) {
				continue
			}
			for _, ca := range sortedKeys(ps[a].pins) {
				for _, cb := range sortedKeys(ps[b].pins) {
					if !ps[a].pins[ca].overlaps(ps[b].pins[cb]) {
						continue
					}
					pa, pb := ps[a].in.title+"."+ca, ps[b].in.title+"."+cb
					if net(pa) == net(pb) {
						continue
					}
					rep.Issues = append(rep.Issues, LayoutIssue{LevelError, "pin-contact",
						fmt.Sprintf("%s overlaps %s (nets differ): they touch and can short", pinName(res, ps[a].in, ca), pinName(res, ps[b].in, cb))})
				}
			}
		}
	}

	// 2. parts overlapping (not a part sitting on its breadboard or board)
	for a := range ps {
		for b := a + 1; b < len(ps); b++ {
			if ps[a].base || ps[b].base || !ps[a].box.overlaps(ps[b].box) {
				continue
			}
			rep.Issues = append(rep.Issues, LayoutIssue{LevelWarn, "parts-overlap",
				fmt.Sprintf("%s and %s overlap (artwork boxes; check the pins if they look fine)", ps[a].in.title, ps[b].in.title)})
		}
	}

	// 3. wires: over foreign pins and parts
	chainOf := map[string]*chain{}
	for _, c := range m.chains {
		chainOf[c.id] = c
	}
	for _, w := range wires {
		endParts := chainOf[w.chain].parts
		for _, p := range ps {
			if p.base {
				continue
			}
			for _, c := range sortedKeys(p.pins) {
				pin := p.in.title + "." + c
				if net(pin) == w.net {
					continue
				}
				r := p.pins[c]
				if r.hitBySegment(w.x1, w.y1, w.x2, w.y2) {
					rep.Issues = append(rep.Issues, LayoutIssue{LevelError, "wire-over-pin",
						fmt.Sprintf("%s runs over %s, which is on another net (wire (%.1f, %.1f) to (%.1f, %.1f) mm; pin at (%.1f, %.1f) mm)",
							w.title, pinName(res, p.in, c), mm(w.x1), mm(w.y1), mm(w.x2), mm(w.y2), mm((r.x0+r.x1)/2), mm((r.y0+r.y1)/2))})
				} else if r.grow(mmToPx(nearPinMM)).hitBySegment(w.x1, w.y1, w.x2, w.y2) {
					rep.Issues = append(rep.Issues, LayoutIssue{LevelInfo, "wire-near-pin",
						fmt.Sprintf("%s passes within %.0f mm of %s, which is on another net", w.title, nearPinMM, pinName(res, p.in, c))})
				}
			}
			if !endParts[p.in.title] {
				if p.box.hitBySegment(w.x1, w.y1, w.x2, w.y2) {
					rep.Issues = append(rep.Issues, LayoutIssue{LevelWarn, "wire-over-part",
						fmt.Sprintf("%s passes across %s (box (%.1f, %.1f), %.1f x %.1f mm; wire (%.1f, %.1f) to (%.1f, %.1f) mm)", w.title, p.in.title,
							mm(p.box.x0), mm(p.box.y0), mm(p.box.x1-p.box.x0), mm(p.box.y1-p.box.y0), mm(w.x1), mm(w.y1), mm(w.x2), mm(w.y2))})
				}
			} else if crossesBody(p, w) {
				// a wire that ends on this part but cuts through its body on the way to the pin
				rep.Issues = append(rep.Issues, LayoutIssue{LevelWarn, "wire-over-part",
					fmt.Sprintf("%s ends on %s but runs across its body first (box (%.1f, %.1f), %.1f x %.1f mm; wire (%.1f, %.1f) to (%.1f, %.1f) mm)", w.title, p.in.title,
						mm(p.box.x0), mm(p.box.y0), mm(p.box.x1-p.box.x0), mm(p.box.y1-p.box.y0), mm(w.x1), mm(w.y1), mm(w.x2), mm(w.y2))})
			}
		}
	}

	// 4. wires crossing wires
	for a := range wires {
		for b := a + 1; b < len(wires); b++ {
			wa, wb := wires[a], wires[b]
			if wa.chain == wb.chain || sharesEnd(wa, wb) {
				continue
			}
			x, y, ok := intersect(wa, wb)
			if !ok {
				continue
			}
			rep.Crossings++
			lvl, note := LevelWarn, ""
			switch {
			case wa.net != "" && wa.net == wb.net:
				lvl, note = LevelInfo, " (same net, harmless)"
			case view == "breadboard" && wa.color != "" && wa.color == wb.color:
				// Nothing electrical, but two wires of one colour crossing look joined.
				lvl, note = LevelWarn, fmt.Sprintf(" (different nets, same colour %s: looks joined, recolour one)", wa.color)
			case view != "pcb":
				// Breadboard wires and schematic traces only connect at their ends, so two
				// of them lying across each other are not joined. On a PCB the same copper
				// layer would short, so there it stays a warning.
				lvl, note = LevelInfo, " (different nets, not connected: wires only join at their ends)"
				rep.softCrossings++
			}
			rep.Issues = append(rep.Issues, LayoutIssue{lvl, "crossing",
				fmt.Sprintf("%s crosses %s at (%.1f, %.1f) mm%s", wa.title, wb.title, mm(x), mm(y), note)})
		}
	}

	// 5. wires of different nets running side by side
	for a := range wires {
		for b := a + 1; b < len(wires); b++ {
			wa, wb := wires[a], wires[b]
			if wa.chain == wb.chain || sharesEnd(wa, wb) || (wa.net != "" && wa.net == wb.net) {
				continue
			}
			if overlap, gap, ok := runsAlong(wa, wb); ok {
				switch {
				case gap < mmToPx(parallelTouchMM):
					rep.Issues = append(rep.Issues, LayoutIssue{LevelWarn, "parallel-close",
						fmt.Sprintf("%s and %s run on top of each other for %.1f mm (%.1f mm apart): they read as one wire", wa.title, wb.title, mm(overlap), mm(gap))})
				case gap < mmToPx(parallelCloseMM):
					rep.Issues = append(rep.Issues, LayoutIssue{LevelInfo, "parallel-close",
						fmt.Sprintf("%s and %s run side by side for %.1f mm, only %.1f mm apart", wa.title, wb.title, mm(overlap), mm(gap))})
				}
			}
		}
	}

	order := map[string]int{LevelError: 0, LevelWarn: 1, LevelInfo: 2}
	sort.SliceStable(rep.Issues, func(a, b int) bool { return order[rep.Issues[a].Level] < order[rep.Issues[b].Level] })
	rep.Tidiness = tidiness(rep)
	return rep
}

// crossesBody reports whether a wire attached to part p passes through p's box away from p's own
// pins (those are allowed to sit on the box edge or inside it).
func crossesBody(p lpart, w seg) bool {
	if p.base {
		return false
	}
	inner := p.box.grow(-mmToPx(0.4))
	if inner.x1 <= inner.x0 || inner.y1 <= inner.y0 {
		return false
	}
	var pins []rect
	for _, r := range p.pins {
		pins = append(pins, r.grow(mmToPx(1.2)))
	}
	l := w.length()
	n := int(l/mmToPx(0.25)) + 1
	inside := 0
	for k := 0; k <= n; k++ {
		t := float64(k) / float64(n)
		x, y := w.x1+t*(w.x2-w.x1), w.y1+t*(w.y2-w.y1)
		if x <= inner.x0 || x >= inner.x1 || y <= inner.y0 || y >= inner.y1 {
			continue
		}
		near := false
		for _, r := range pins {
			if x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1 {
				near = true
				break
			}
		}
		if !near {
			inside++
		}
	}
	return float64(inside)*l/float64(n) > mmToPx(1)
}

// tidiness is 0-100, 100 for a clean drawing: 20 per error, 10 per warning (PCB crossing,
// same-colour breadboard crossing, wires on top of each other, wire over a part, overlapping parts), 1 per breadboard/schematic crossing of different nets, 3 per diagonal
// wire, 0.5 per bend and 1 per long wire.
func tidiness(r LayoutReport) float64 {
	score := 100.0 - 20*float64(r.Count(LevelError)) - 10*float64(r.Count(LevelWarn)) -
		float64(r.softCrossings) - 3*float64(r.Diagonals) - 0.5*float64(r.Bends) - float64(r.LongWires)
	return math.Round(math.Max(0, score)*10) / 10
}

func sharesEnd(a, b seg) bool {
	for _, x := range a.ends {
		for _, y := range b.ends {
			if x != "" && x == y {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]rect) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

// pinName is "R1.1 (pin 1)" style text for a connector: the part title and the connector's name.
func pinName(res Resolver, i inst, connector string) string {
	if f, _, err := res.FZP(i.module); err == nil {
		for _, c := range f.Connectors {
			if c.ID == connector && strings.TrimSpace(c.Name) != "" {
				return i.title + "." + strings.TrimSpace(c.Name)
			}
		}
	}
	return i.title + "." + connector
}

// Brief is a one-line summary for tool results: "layout ok" or the errors and warnings.
func (r LayoutReport) Brief() []string {
	var out []string
	for _, i := range r.Issues {
		if i.Level != LevelInfo {
			out = append(out, strings.ToUpper(i.Level)+": "+i.Text)
		}
	}
	return out
}

// FocusBox is the area of a view around a part, or around a wire (its whole chain), in scene px,
// grown by marginMM on every side. It is what a zoomed snapshot shows.
func (s *Sketch) FocusBox(res Resolver, view, around string, marginMM float64) (x0, y0, x1, y1 float64, err error) {
	if !validView(view) || res == nil {
		return 0, 0, 0, 0, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	m := s.model(res, view)
	var b box
	for _, p := range m.parts {
		if strings.EqualFold(p.in.title, around) {
			b.add(p.box.x0, p.box.y0)
			b.add(p.box.x1, p.box.y1)
		}
	}
	if !b.any {
		for _, w := range m.wires {
			if !strings.EqualFold(w.title, around) {
				continue
			}
			for _, o := range m.wires {
				if o.chain == w.chain {
					b.add(o.x1, o.y1)
					b.add(o.x2, o.y2)
				}
			}
		}
	}
	if !b.any {
		return 0, 0, 0, 0, fmt.Errorf("no part or wire %q in the %s view", around, view)
	}
	g := mmToPx(marginMM)
	return b.minX - g, b.minY - g, b.maxX + g, b.maxY + g, nil
}

// MMToPx converts millimetres to Fritzing scene pixels.
func MMToPx(mm float64) float64 { return mmToPx(mm) }

var (
	rootViewBoxRe = regexp.MustCompile(`viewBox\s*=\s*'[^']*'`)
	rootWidthRe   = regexp.MustCompile(`\bwidth\s*=\s*'[^']*'`)
	rootHeightRe  = regexp.MustCompile(`\bheight\s*=\s*'[^']*'`)
)

// CropView narrows a rendered view SVG to an area given in scene px. Fritzing writes its exports
// in 1000 units per inch from the scene origin, so the area maps to the viewBox by 1000/90.
func CropView(svg []byte, x0, y0, x1, y1 float64) ([]byte, error) {
	start := bytes.Index(svg, []byte("<svg"))
	if start < 0 {
		return nil, fmt.Errorf("not an SVG")
	}
	end := start + bytes.IndexByte(svg[start:], '>')
	if end < start {
		return nil, fmt.Errorf("not an SVG")
	}
	head := string(svg[start : end+1])
	if !rootViewBoxRe.MatchString(head) {
		return nil, fmt.Errorf("the rendered SVG has no viewBox to crop")
	}
	k := 1000.0 / pxPerInch
	x0, y0 = math.Max(0, x0)*k, math.Max(0, y0)*k
	w, h := (x1*k - x0), (y1*k - y0)
	if w <= 1 || h <= 1 {
		return nil, fmt.Errorf("the area is empty or outside the drawing")
	}
	head = rootViewBoxRe.ReplaceAllString(head, fmt.Sprintf("viewBox='%.2f %.2f %.2f %.2f'", x0, y0, w, h))
	head = rootWidthRe.ReplaceAllString(head, fmt.Sprintf("width='%.4fin'", w/1000))
	head = rootHeightRe.ReplaceAllString(head, fmt.Sprintf("height='%.4fin'", h/1000))
	return append(append(append([]byte{}, svg[:start]...), head...), svg[end+1:]...), nil
}
