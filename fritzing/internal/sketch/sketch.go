package sketch

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

// Resolver finds part definitions and artwork: the parts database plus a project's own parts.
type Resolver interface {
	FZP(moduleID string) (*parts.FZP, string, error)
	SVG(moduleID, view string) ([]byte, error)
}

const wireModule = "WireModuleID"

// Wire flags, as Fritzing stores them in a wire's geometry: they say which view the wire lives in.
const (
	flagPCBTrace  = 4
	flagNormal    = 64 // breadboard wire
	flagSchematic = 128
)

// viewElement is the name of a view's element inside an instance.
func viewElement(view string) string { return view + "View" }

// Sketch is a parsed .fz file.
type Sketch struct {
	root *Node
}

// Parse reads a sketch.
func Parse(src string) (*Sketch, error) {
	root, err := parseXML([]byte(src))
	if err != nil {
		return nil, err
	}
	if root.Name != "module" {
		return nil, fmt.Errorf("not a Fritzing sketch: root element is <%s>, want <module>", root.Name)
	}
	return &Sketch{root: root}, nil
}

// String serializes the sketch.
func (s *Sketch) String() string { return serialize(s.root) }

func (s *Sketch) instances() *Node { return s.root.Ensure("instances") }

// inst is one <instance>.
type inst struct {
	n      *Node
	index  int
	module string
	title  string
	isWire bool
}

func (s *Sketch) all() []inst {
	var out []inst
	for _, n := range s.instances().Kids("instance") {
		idx, _ := strconv.Atoi(n.Attr("modelIndex"))
		t := ""
		if c := n.Child("title"); c != nil {
			t = strings.TrimSpace(c.Text)
		}
		out = append(out, inst{n: n, index: idx, module: n.Attr("moduleIdRef"), title: t, isWire: n.Attr("moduleIdRef") == wireModule})
	}
	return out
}

func (s *Sketch) byIndex() map[int]inst {
	m := map[int]inst{}
	for _, i := range s.all() {
		m[i.index] = i
	}
	return m
}

// find looks an instance up by its title ("LED1") or model index.
func (s *Sketch) find(ref string) (inst, error) {
	var byNum *inst
	n, numErr := strconv.Atoi(ref)
	for _, i := range s.all() {
		if i.title == ref {
			return i, nil
		}
		if numErr == nil && i.index == n {
			i := i
			byNum = &i
		}
	}
	if byNum != nil {
		return *byNum, nil
	}
	return inst{}, fmt.Errorf("no part or wire %q in the sketch (parts: %s)", ref, strings.Join(s.titles(), ", "))
}

func (s *Sketch) titles() []string {
	var t []string
	for _, i := range s.all() {
		if !i.isWire {
			t = append(t, i.title)
		}
	}
	return t
}

func (s *Sketch) nextIndex() int {
	max := 5000
	for _, i := range s.all() {
		if i.index > max {
			max = i.index
		}
	}
	return max + 1
}

// uniqueTitle returns label+N not used yet ("LED1", "LED2", ...).
func (s *Sketch) uniqueTitle(label string) string {
	used := map[string]bool{}
	for _, i := range s.all() {
		used[i.title] = true
	}
	for n := 1; ; n++ {
		if t := label + strconv.Itoa(n); !used[t] {
			return t
		}
	}
}

// wireView says which view a wire belongs to, from its flags.
func wireView(flags int) string {
	switch {
	case flags&flagSchematic != 0:
		return parts.Schematic
	case flags&flagPCBTrace != 0:
		return parts.PCB
	}
	return parts.Breadboard
}

func (i inst) wireFlags() int {
	if ve := i.n.Child("views"); ve != nil {
		for _, v := range parts.Views {
			if e := ve.Child(viewElement(v)); e != nil {
				if g := e.Child("geometry"); g != nil && g.HasAttr("wireFlags") {
					f, _ := strconv.Atoi(g.Attr("wireFlags"))
					return f
				}
			}
		}
	}
	return 0
}

// viewNode returns the instance's <xxxView> element, or nil.
func (i inst) viewNode(view string) *Node {
	if v := i.n.Child("views"); v != nil {
		return v.Child(viewElement(view))
	}
	return nil
}

// PartInfo describes a part in the sketch.
type PartInfo struct {
	Title      string             `json:"title"`
	ModuleID   string             `json:"module_id"`
	Properties map[string]string  `json:"properties,omitempty"`
	Positions  map[string]Placing `json:"positions"` // per view, in mm
}

// Placing is where a part sits in one view.
type Placing struct {
	X        float64 `json:"x_mm"`
	Y        float64 `json:"y_mm"`
	Rotation float64 `json:"rotation_deg,omitempty"`
}

// WireInfo describes a wire (a breadboard wire, schematic trace or PCB trace).
type WireInfo struct {
	Title string `json:"title"`
	View  string `json:"view"`
	From  string `json:"from"` // "LED1.connector0", "" for an open end
	To    string `json:"to"`
	Layer string `json:"layer,omitempty"`
	// Color is the wire colour as "#rrggbb" (breadboard wires; traces carry their layer colour).
	Color string `json:"color,omitempty"`
}

// Net is the set of pins that are electrically joined in one view.
type Net struct {
	View string   `json:"view"`
	Pins []string `json:"pins"`
}

// Summary is what is in a sketch.
type Summary struct {
	Parts []PartInfo `json:"parts"`
	Wires []WireInfo `json:"wires"`
	// Nets are the groups of pins joined by wires, direct contact (parts plugged into a breadboard)
	// or a part's internal buses, per view. Only nets with two or more pins are listed.
	Nets []Net `json:"nets"`
}

// Describe lists the parts, wires and nets. res is optional; without it internal buses are not
// followed.
func (s *Sketch) Describe(res Resolver) Summary {
	sum := Summary{Parts: []PartInfo{}, Wires: []WireInfo{}, Nets: []Net{}}
	byIdx := s.byIndex()
	for _, i := range s.all() {
		if i.isWire {
			sum.Wires = append(sum.Wires, s.wireInfo(i, byIdx))
			continue
		}
		p := PartInfo{Title: i.title, ModuleID: i.module, Positions: map[string]Placing{}}
		for _, pr := range i.n.Kids("property") {
			if p.Properties == nil {
				p.Properties = map[string]string{}
			}
			p.Properties[pr.Attr("name")] = pr.Attr("value")
		}
		for _, v := range parts.Views {
			if vn := i.viewNode(v); vn != nil {
				if g := vn.Child("geometry"); g != nil {
					pl := Placing{X: pxToMM(parseNum(g.Attr("x"))), Y: pxToMM(parseNum(g.Attr("y")))}
					if t := g.Child("transform"); t != nil {
						pl.Rotation = math.Round(math.Atan2(parseNum(t.Attr("m12")), parseNum(t.Attr("m11")))*180/math.Pi*10) / 10
					}
					pl.X, pl.Y = round2(pl.X), round2(pl.Y)
					p.Positions[v] = pl
				}
			}
		}
		sum.Parts = append(sum.Parts, p)
	}
	sum.Nets = s.nets(res, byIdx)
	return sum
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// kids is the children of a possibly nil node.
func (n *Node) kids() []*Node {
	if n == nil {
		return nil
	}
	return n.Children
}

// wireEnds returns the two connections of a wire in its own view as "Title.connector" ("" if open).
func (s *Sketch) wireEnds(i inst, view string, byIdx map[int]inst) [2]string {
	var out [2]string
	vn := i.viewNode(view)
	if vn == nil {
		return out
	}
	for _, c := range vn.Child("connectors").kids() {
		k := 0
		if c.Attr("connectorId") == "connector1" {
			k = 1
		}
		for _, cc := range c.Child("connects").kids() {
			idx, _ := strconv.Atoi(cc.Attr("modelIndex"))
			if t, ok := byIdx[idx]; ok {
				out[k] = t.title + "." + cc.Attr("connectorId")
			}
		}
	}
	return out
}

func (s *Sketch) wireInfo(i inst, byIdx map[int]inst) WireInfo {
	v := wireView(i.wireFlags())
	e := s.wireEnds(i, v, byIdx)
	w := WireInfo{Title: i.title, View: v, From: e[0], To: e[1]}
	if vn := i.viewNode(v); vn != nil {
		w.Layer = vn.Attr("layer")
		if x := vn.Child("wireExtras"); x != nil {
			w.Color = strings.ToLower(x.Attr("color"))
		}
	}
	return w
}

func (s *Sketch) nets(res Resolver, byIdx map[int]inst) []Net {
	type key struct{ view, pin string }
	parent := map[key]key{}
	var find func(k key) key
	find = func(k key) key {
		p, ok := parent[k]
		if !ok || p == k {
			parent[k] = k
			return k
		}
		r := find(p)
		parent[k] = r
		return r
	}
	union := func(a, b key) { parent[find(a)] = find(b) }
	isWire := map[string]bool{}
	for _, i := range s.all() {
		isWire[i.title] = i.isWire
	}
	joinConnects := func(i inst, v string, vn *Node) {
		for _, c := range vn.Child("connectors").kids() {
			for _, cc := range c.Child("connects").kids() {
				idx, _ := strconv.Atoi(cc.Attr("modelIndex"))
				if t, ok := byIdx[idx]; ok {
					union(key{v, i.title + "." + c.Attr("connectorId")}, key{v, t.title + "." + cc.Attr("connectorId")})
				}
			}
		}
	}
	buses := map[string][][]string{}
	for _, i := range s.all() {
		if i.isWire {
			v := wireView(i.wireFlags())
			if vn := i.viewNode(v); vn != nil {
				union(key{v, i.title + ".connector0"}, key{v, i.title + ".connector1"})
				joinConnects(i, v, vn)
			}
			continue
		}
		for _, v := range parts.Views {
			vn := i.viewNode(v)
			if vn == nil {
				continue
			}
			joinConnects(i, v, vn)
			if res == nil {
				continue
			}
			if _, ok := buses[i.module]; !ok {
				buses[i.module] = nil
				if f, _, err := res.FZP(i.module); err == nil {
					for _, b := range f.Buses {
						var m []string
						for _, x := range b.Members {
							m = append(m, x.ConnectorID)
						}
						buses[i.module] = append(buses[i.module], m)
					}
				}
			}
			for _, b := range buses[i.module] {
				for _, m := range b[1:] {
					union(key{v, i.title + "." + b[0]}, key{v, i.title + "." + m})
				}
			}
		}
	}
	groups := map[key][]string{}
	for k := range parent {
		name, _, _ := strings.Cut(k.pin, ".")
		if isWire[name] {
			continue
		}
		r := find(k)
		groups[r] = append(groups[r], k.pin)
	}
	out := []Net{}
	for r, pins := range groups {
		if len(pins) < 2 {
			continue
		}
		sort.Strings(pins)
		out = append(out, Net{View: r.view, Pins: pins})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].View != out[b].View {
			return out[a].View < out[b].View
		}
		return out[a].Pins[0] < out[b].Pins[0]
	})
	return out
}

// ModuleIDs returns the module ids of all parts (not wires) in the sketch.
func (s *Sketch) ModuleIDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, i := range s.all() {
		if !i.isWire && !seen[i.module] {
			seen[i.module] = true
			out = append(out, i.module)
		}
	}
	return out
}
