package sketch

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

// XY is a position in mm in one view.
type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// AddOptions says how to place a new part.
type AddOptions struct {
	Title      string            // default: the part's label plus a number (LED1, R2)
	Properties map[string]string // e.g. resistance, color, pins
	// Position is the top-left corner in mm per view (breadboard, schematic, pcb). A view left out
	// gets a free spot to the right of what is already there.
	Position map[string]XY
	Rotation float64 // degrees clockwise, applied in every view
}

// layer height, so wires draw over parts and copper over the board; Fritzing re-sorts on load but
// the order has to be sensible
var layerZ = map[string]float64{
	"breadboardbreadboard": 1.5, "breadboard": 2.5, "breadboardWire": 3.5, "icon": 0.5,
	"schematic": 3.0, "schematicTrace": 5.5,
	"board": 1.5, "copper0": 6.0, "copper0trace": 6.5, "copper1": 9.0, "copper1trace": 9.5,
}

type editor struct {
	s   *Sketch
	res Resolver
	svg map[string]*SVGInfo
}

func (s *Sketch) editor(res Resolver) *editor {
	return &editor{s: s, res: res, svg: map[string]*SVGInfo{}}
}

// info returns the artwork size of a part in a view (nil if it has none).
func (e *editor) info(module, view string) *SVGInfo {
	k := module + "/" + view
	if v, ok := e.svg[k]; ok {
		return v
	}
	var out *SVGInfo
	if b, err := e.res.SVG(module, view); err == nil {
		if info, ok := ParseSVG(b); ok {
			out = info
		}
	}
	e.svg[k] = out
	return out
}

// rotation builds the matrix Fritzing stores for turning a w×h item by deg about its centre.
func rotation(deg, w, h float64) (m [6]float64, ok bool) {
	deg = math.Mod(deg, 360)
	if deg == 0 {
		return m, false
	}
	a := deg * math.Pi / 180
	c, sn := math.Cos(a), math.Sin(a)
	if math.Abs(c) < 1e-12 {
		c = 0
	}
	if math.Abs(sn) < 1e-12 {
		sn = 0
	}
	cx, cy := w/2, h/2
	return [6]float64{c, sn, -sn, c, cx - cx*c + cy*sn, cy - cx*sn - cy*c}, true
}

func setTransform(g *Node, deg, w, h float64) {
	for _, c := range g.Kids("transform") {
		g.Remove(c)
	}
	m, ok := rotation(deg, w, h)
	if !ok {
		return
	}
	g.Add(El("transform",
		"m11", num(m[0]), "m12", num(m[1]), "m13", "0",
		"m21", num(m[2]), "m22", num(m[3]), "m23", "0",
		"m31", num(m[4]), "m32", num(m[5]), "m33", "1"))
}

// nextFreeX is the right edge of everything placed in a view, in px.
func (e *editor) nextFreeX(view string) (x, top float64) {
	first := true
	for _, i := range e.s.all() {
		if i.isWire {
			continue
		}
		vn := i.viewNode(view)
		if vn == nil || vn.Child("geometry") == nil {
			continue
		}
		g := vn.Child("geometry")
		gx, gy := parseNum(g.Attr("x")), parseNum(g.Attr("y"))
		w := 0.0
		if inf := e.info(i.module, view); inf != nil {
			w = inf.W
		}
		if first || gy < top {
			top = gy
		}
		first = false
		x = math.Max(x, gx+w)
	}
	if first {
		return 0, 0
	}
	return x + mmToPx(5), top
}

// AddPart places a part from the library in the sketch and returns its title.
func (s *Sketch) AddPart(res Resolver, moduleID string, o AddOptions) (string, error) {
	f, fzpPath, err := res.FZP(moduleID)
	if err != nil {
		return "", err
	}
	e := s.editor(res)
	title := o.Title
	if title == "" {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = "P"
		}
		title = s.uniqueTitle(label)
	} else {
		for _, i := range s.all() {
			if i.title == title {
				return "", fmt.Errorf("title %q is already used", title)
			}
		}
	}
	idx := s.nextIndex()
	in := El("instance", "moduleIdRef", f.ModuleID, "modelIndex", strconv.Itoa(idx), "path", fzpPath)
	names := make([]string, 0, len(o.Properties))
	for n := range o.Properties {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		in.Add(El("property", "name", n, "value", o.Properties[n]))
	}
	in.Add(&Node{Name: "title", Text: title})
	views := in.Add(El("views"))
	n := 0
	for _, v := range parts.Views {
		layer := f.Layer(v)
		if layer == "" {
			continue
		}
		n++
		var x, y float64
		if p, ok := o.Position[v]; ok {
			x, y = mmToPx(p.X), mmToPx(p.Y)
		} else {
			x, y = e.nextFreeX(v)
		}
		z := layerZ[layer] + float64(idx%1000)*1e-5
		vn := views.Add(El(viewElement(v), "layer", layer))
		g := vn.Add(El("geometry", "z", num(z), "x", num(x), "y", num(y)))
		if inf := e.info(f.ModuleID, v); inf != nil {
			setTransform(g, o.Rotation, inf.W, inf.H)
		}
	}
	if n == 0 {
		return "", fmt.Errorf("part %s has no breadboard, schematic or pcb view", moduleID)
	}
	s.instances().Add(in)
	return title, nil
}

// Move changes where a part sits in one view (nil leaves a coordinate or the rotation as it is) and
// drags the wires attached to it along.
func (s *Sketch) Move(res Resolver, ref, view string, x, y, rotate *float64) error {
	if !validView(view) {
		return fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	i, err := s.find(ref)
	if err != nil {
		return err
	}
	if i.isWire {
		return fmt.Errorf("%s is a wire: wires follow the parts they connect; remove it and connect again to reroute", ref)
	}
	vn := i.viewNode(view)
	if vn == nil || vn.Child("geometry") == nil {
		return fmt.Errorf("%s is not in the %s view", i.title, view)
	}
	e := s.editor(res)
	g := vn.Child("geometry")
	if x != nil {
		g.SetAttr("x", num(mmToPx(*x)))
	}
	if y != nil {
		g.SetAttr("y", num(mmToPx(*y)))
	}
	if rotate != nil {
		inf := e.info(i.module, view)
		if inf == nil {
			return fmt.Errorf("%s has no %s artwork to rotate", i.title, view)
		}
		setTransform(g, *rotate, inf.W, inf.H)
	}
	e.relayoutWires(i, view)
	return nil
}

func validView(v string) bool { return v == parts.Breadboard || v == parts.Schematic || v == parts.PCB }

// SetProperty sets a property of a part (resistance, color, pins, ...); value "" removes it.
func (s *Sketch) SetProperty(ref, name, value string) error {
	i, err := s.find(ref)
	if err != nil {
		return err
	}
	if i.isWire {
		return fmt.Errorf("%s is a wire", ref)
	}
	for _, p := range i.n.Kids("property") {
		if p.Attr("name") == name {
			if value == "" {
				i.n.Remove(p)
			} else {
				p.SetAttr("value", value)
			}
			return nil
		}
	}
	if value == "" {
		return nil
	}
	p := El("property", "name", name, "value", value)
	for k, c := range i.n.Children { // properties come before <title>
		if c.Name == "title" {
			i.n.Children = append(i.n.Children[:k], append([]*Node{p}, i.n.Children[k:]...)...)
			return nil
		}
	}
	i.n.Add(p)
	return nil
}

// Rename gives a part another title.
func (s *Sketch) Rename(ref, title string) error {
	i, err := s.find(ref)
	if err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is empty")
	}
	for _, o := range s.all() {
		if o.title == title && o.index != i.index {
			return fmt.Errorf("title %q is already used", title)
		}
	}
	i.n.Child("title").Text = title
	return nil
}

// Remove deletes a part or wire together with every wire that was attached to it, and returns the
// titles that went. A wire with bends is a chain of wires: all of it goes.
func (s *Sketch) Remove(ref string) ([]string, error) { return s.RemoveOpt(ref, false) }

// RemoveOpt is Remove; onlySegment removes just the one straight wire of a chain.
func (s *Sketch) RemoveOpt(ref string, onlySegment bool) ([]string, error) {
	i, err := s.find(ref)
	if err != nil {
		return nil, err
	}
	var gone []string
	queue := []inst{i}
	seen := map[int]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur.index] {
			continue
		}
		seen[cur.index] = true
		gone = append(gone, cur.title)
		if cur.isWire && !onlySegment {
			queue = append(queue, s.wireNeighbors(cur)...)
		}
		s.instances().Remove(cur.n)
		for _, o := range s.all() {
			if s.dropConnects(o, cur.index) && !cur.isWire && o.isWire {
				queue = append(queue, o) // a wire lost an end: it goes too
			}
		}
	}
	return gone, nil
}

// wireNeighbors are the wires whose ends are joined to w's ends (the rest of a chain).
func (s *Sketch) wireNeighbors(w inst) []inst {
	byIdx := s.byIndex()
	var out []inst
	vn := w.viewNode(wireView(w.wireFlags()))
	if vn == nil {
		return nil
	}
	for _, c := range vn.Child("connectors").kids() {
		for _, cc := range c.Child("connects").kids() {
			idx, _ := strconv.Atoi(cc.Attr("modelIndex"))
			if t, ok := byIdx[idx]; ok && t.isWire {
				out = append(out, t)
			}
		}
	}
	return out
}

// dropConnects removes every connect to modelIndex from an instance and reports whether any was there.
func (s *Sketch) dropConnects(o inst, modelIndex int) bool {
	found := false
	views := o.n.Child("views")
	if views == nil {
		return false
	}
	for _, vn := range views.Children {
		cs := vn.Child("connectors")
		if cs == nil {
			continue
		}
		for _, c := range append([]*Node(nil), cs.Children...) {
			cn := c.Child("connects")
			if cn == nil {
				continue
			}
			for _, k := range append([]*Node(nil), cn.Children...) {
				if idx, _ := strconv.Atoi(k.Attr("modelIndex")); idx == modelIndex {
					cn.Remove(k)
					found = true
				}
			}
			if len(cn.Children) == 0 {
				c.Remove(cn)
			}
			if c.Child("connects") == nil && c.Child("leg") == nil {
				cs.Remove(c)
			}
		}
		if len(cs.Children) == 0 {
			vn.Remove(cs)
		}
	}
	return found
}

// Endpoint is one end of a connection: a part's title and one of its connectors (id or name).
type Endpoint struct {
	Part      string `json:"part"`
	Connector string `json:"connector"`
}

// ConnectOptions tune a new wire.
type ConnectOptions struct {
	Side  string // pcb only: "bottom" (copper0, default) or "top" (copper1)
	Color string // breadboard wire colour as "#rrggbb"
	// Via are waypoints in mm: the wire becomes a chain of straight wires through them.
	Via []XY
	// Style is "straight" (default) or "orthogonal": horizontal and vertical segments, the
	// waypoints are chosen by the server.
	Style string
}

// Connect draws a wire (breadboard), trace (schematic) or copper trace (pcb) between two
// connectors and returns the wire's title.
func (s *Sketch) Connect(res Resolver, view string, a, b Endpoint, o ConnectOptions) ([]string, error) {
	if !validView(view) {
		return nil, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	e := s.editor(res)
	ia, err := s.find(a.Part)
	if err != nil {
		return nil, err
	}
	ib, err := s.find(b.Part)
	if err != nil {
		return nil, err
	}
	if ia.isWire || ib.isWire {
		return nil, fmt.Errorf("connect parts, not wires")
	}
	var wantLayer string
	wireLayer, flags, mils, color := "breadboardWire", flagNormal, "22.2222", "#418dd9"
	switch view {
	case parts.Schematic:
		wireLayer, flags, mils, color = "schematicTrace", flagSchematic, "9.7222", "#404040"
	case parts.PCB:
		wantLayer, wireLayer, flags, mils, color = "copper0", "copper0trace", flagPCBTrace, "24", "#f28a00"
		if o.Side == "top" {
			wantLayer, wireLayer, color = "copper1", "copper1trace", "#f2c600"
		} else if o.Side != "" && o.Side != "bottom" {
			return nil, fmt.Errorf("side must be top or bottom")
		}
	}
	if view == parts.Breadboard && o.Color != "" {
		color = o.Color
	}
	pa, err := e.pin(ia, view, a.Connector, wantLayer)
	if err != nil {
		return nil, err
	}
	pb, err := e.pin(ib, view, b.Connector, wantLayer)
	if err != nil {
		return nil, err
	}
	if ia.index == ib.index && pa.connector == pb.connector {
		return nil, fmt.Errorf("both ends are %s.%s", ia.title, pa.connector)
	}
	via := o.Via
	if len(via) == 0 && (o.Style == "orthogonal" || o.Style == "orthogonal45") {
		if o.Style == "orthogonal45" {
			return nil, fmt.Errorf("style orthogonal45 is not implemented yet; use orthogonal")
		}
		var err error
		if via, err = s.planRoute(res, view, ia, pa, ib, pb); err != nil {
			return nil, err
		}
	} else if o.Style != "" && o.Style != "straight" && o.Style != "orthogonal" {
		return nil, fmt.Errorf("style must be straight or orthogonal")
	}
	if err := s.checkVia(res, view, via); err != nil {
		return nil, err
	}
	pts := [][2]float64{{pa.x, pa.y}}
	for _, v := range via {
		pts = append(pts, [2]float64{mmToPx(v.X), mmToPx(v.Y)})
	}
	pts = append(pts, [2]float64{pb.x, pb.y})

	var titles []string
	var idxs []int
	var nodes []*Node
	for k := 0; k+1 < len(pts); k++ {
		title := s.uniqueTitle("Wire")
		idx := s.nextIndex()
		w := El("instance", "moduleIdRef", wireModule, "modelIndex", strconv.Itoa(idx), "path", ":/resources/parts/core/wire.fzp")
		w.Add(&Node{Name: "title", Text: title})
		vn := w.Add(El("views")).Add(El(viewElement(view), "layer", wireLayer))
		if view == parts.PCB && wireLayer == "copper0trace" {
			vn.SetAttr("bottom", "true")
		}
		vn.Add(El("geometry", "z", num(layerZ[wireLayer]+float64(idx%1000)*1e-5),
			"x", num(pts[k][0]), "y", num(pts[k][1]), "x1", "0", "y1", "0", "x2", num(pts[k+1][0]-pts[k][0]), "y2", num(pts[k+1][1]-pts[k][1]), "wireFlags", strconv.Itoa(flags)))
		vn.Add(El("wireExtras", "mils", mils, "color", color, "opacity", "1", "banded", "0"))
		vn.Add(El("connectors"))
		s.instances().Add(w)
		titles = append(titles, title)
		idxs = append(idxs, idx)
		nodes = append(nodes, vn.Child("connectors"))
	}
	// ends: a wire's connector k lists what it is plugged into
	addEnd := func(cs *Node, k int, target int, targetConn, targetLayer string) {
		c := cs.Add(El("connector", "connectorId", "connector"+strconv.Itoa(k), "layer", wireLayer))
		c.Add(El("geometry", "x", "0", "y", "0"))
		c.Add(El("connects")).Add(El("connect", "connectorId", targetConn, "modelIndex", strconv.Itoa(target), "layer", targetLayer))
	}
	last := len(idxs) - 1
	for k := range idxs {
		if k == 0 {
			addEnd(nodes[k], 0, ia.index, pa.connector, pa.layer)
		} else {
			addEnd(nodes[k], 0, idxs[k-1], "connector1", wireLayer)
		}
		if k == last {
			addEnd(nodes[k], 1, ib.index, pb.connector, pb.layer)
		} else {
			addEnd(nodes[k], 1, idxs[k+1], "connector0", wireLayer)
		}
	}
	// and the parts remember the wires at the two ends
	for _, p := range []struct {
		in   inst
		pin  pinPoint
		wire int
		k    int
	}{{ia, pa, idxs[0], 0}, {ib, pb, idxs[last], 1}} {
		pc := p.in.viewNode(view).Ensure("connectors")
		var target *Node
		for _, x := range pc.Kids("connector") {
			if x.Attr("connectorId") == p.pin.connector {
				target = x
			}
		}
		if target == nil {
			target = pc.Add(El("connector", "connectorId", p.pin.connector, "layer", p.pin.layer))
			target.Add(El("geometry", "x", "0", "y", "0"))
		}
		target.Ensure("connects").Add(El("connect", "connectorId", "connector"+strconv.Itoa(p.k), "modelIndex", strconv.Itoa(p.wire), "layer", wireLayer))
	}
	return titles, nil
}

type pinPoint struct {
	x, y      float64 // scene px
	connector string
	layer     string
}

// pin finds where a connector of an instance sits in a view, in scene pixels.
func (e *editor) pin(i inst, view, connector, wantLayer string) (pinPoint, error) {
	vn := i.viewNode(view)
	if vn == nil || vn.Child("geometry") == nil {
		return pinPoint{}, fmt.Errorf("%s is not in the %s view", i.title, view)
	}
	f, _, err := e.res.FZP(i.module)
	if err != nil {
		return pinPoint{}, err
	}
	c, err := f.Connector(connector)
	if err != nil {
		return pinPoint{}, err
	}
	pins := c.Pins(view)
	if len(pins) == 0 {
		return pinPoint{}, fmt.Errorf("%s.%s has no %s connection point", i.title, c.ID, view)
	}
	pick := pins[0]
	for _, p := range pins {
		if wantLayer != "" && p.Layer == wantLayer {
			pick = p
		}
	}
	if wantLayer != "" && pick.Layer != wantLayer {
		return pinPoint{}, fmt.Errorf("%s.%s has no copper on the %s layer; use the other side", i.title, c.ID, wantLayer)
	}
	inf := e.info(i.module, view)
	if inf == nil {
		return pinPoint{}, fmt.Errorf("no %s artwork for %s, cannot place a wire on it", view, i.module)
	}
	lx, ly, ok := inf.Pin(pick.SvgID, pick.TerminalID, pick.LegID)
	if !ok {
		return pinPoint{}, fmt.Errorf("connector %s of %s not found in its %s artwork", c.ID, i.module, view)
	}
	g := vn.Child("geometry")
	gx, gy := parseNum(g.Attr("x")), parseNum(g.Attr("y"))
	if t := g.Child("transform"); t != nil {
		m := aff{parseNum(t.Attr("m11")), parseNum(t.Attr("m12")), parseNum(t.Attr("m21")), parseNum(t.Attr("m22")), parseNum(t.Attr("m31")), parseNum(t.Attr("m32"))}
		lx, ly = m.apply(lx, ly)
	}
	return pinPoint{x: gx + lx, y: gy + ly, connector: c.ID, layer: pick.Layer}, nil
}

// relayoutWires moves the wire ends that are attached to part i in view onto its connectors.
func (e *editor) relayoutWires(i inst, view string) {
	byIdx := e.s.byIndex()
	for _, w := range e.s.all() {
		if !w.isWire || wireView(w.wireFlags()) != view {
			continue
		}
		vn := w.viewNode(view)
		if vn == nil {
			continue
		}
		g := vn.Child("geometry")
		if g == nil {
			continue
		}
		var pts [2]*pinPoint
		touched := false
		for _, c := range vn.Child("connectors").kids() {
			k := 0
			if c.Attr("connectorId") == "connector1" {
				k = 1
			}
			for _, cc := range c.Child("connects").kids() {
				idx, _ := strconv.Atoi(cc.Attr("modelIndex"))
				t, ok := byIdx[idx]
				if !ok || t.isWire {
					continue
				}
				if t.index == i.index {
					touched = true
				}
				want := ""
				if view == parts.PCB {
					want = cc.Attr("layer")
				}
				if p, err := e.pin(t, view, cc.Attr("connectorId"), want); err == nil {
					pts[k] = &p
				}
			}
		}
		if !touched {
			continue
		}
		x1, y1 := parseNum(g.Attr("x")), parseNum(g.Attr("y"))
		x2, y2 := x1+parseNum(g.Attr("x2")), y1+parseNum(g.Attr("y2"))
		if pts[0] != nil {
			x1, y1 = pts[0].x, pts[0].y
		}
		if pts[1] != nil {
			x2, y2 = pts[1].x, pts[1].y
		}
		g.SetAttr("x", num(x1))
		g.SetAttr("y", num(y1))
		g.SetAttr("x2", num(x2-x1))
		g.SetAttr("y2", num(y2-y1))
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// WireViewOf returns the view a wire belongs to ("" when ref is not a wire).
func (s *Sketch) WireViewOf(ref string) (view string, err error) {
	i, err := s.find(ref)
	if err != nil {
		return "", err
	}
	if !i.isWire {
		return "", fmt.Errorf("%s is not a wire", ref)
	}
	return wireView(i.wireFlags()), nil
}
