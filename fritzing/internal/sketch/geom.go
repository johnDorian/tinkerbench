package sketch

import (
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"strings"
)

// Fritzing's scene is measured in 90 dpi pixels.
const (
	pxPerInch = 90.0
	mmPerInch = 25.4
)

func pxToMM(px float64) float64 { return px / pxPerInch * mmPerInch }
func mmToPx(mm float64) float64 { return mm / mmPerInch * pxPerInch }

// aff is a 2D affine map: x' = a*x + c*y + e, y' = b*x + d*y + f (the SVG matrix(a b c d e f)).
type aff [6]float64

var identity = aff{1, 0, 0, 1, 0, 0}

// mul returns p∘q: apply q first, then p.
func mul(p, q aff) aff {
	return aff{
		p[0]*q[0] + p[2]*q[1], p[1]*q[0] + p[3]*q[1],
		p[0]*q[2] + p[2]*q[3], p[1]*q[2] + p[3]*q[3],
		p[0]*q[4] + p[2]*q[5] + p[4], p[1]*q[4] + p[3]*q[5] + p[5],
	}
}

func (m aff) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

var (
	numRe       = regexp.MustCompile(`[-+]?(?:\d*\.\d+|\d+\.?)(?:[eE][-+]?\d+)?`)
	transformRe = regexp.MustCompile(`(\w+)\s*\(([^)]*)\)`)
	cmdRe       = regexp.MustCompile(`[MmLlHhVvCcSsQqTtAaZz]|[-+]?(?:\d*\.\d+|\d+\.?)(?:[eE][-+]?\d+)?`)
)

func floats(s string) []float64 {
	var out []float64
	for _, m := range numRe.FindAllString(s, -1) {
		out = append(out, parseNum(m))
	}
	return out
}

// parseTransform reads an SVG transform attribute (matrix, translate, scale, rotate, skewX/Y).
func parseTransform(s string) aff {
	m := identity
	for _, t := range transformRe.FindAllStringSubmatch(s, -1) {
		v := floats(t[2])
		get := func(i int, def float64) float64 {
			if i < len(v) {
				return v[i]
			}
			return def
		}
		var t2 aff
		switch t[1] {
		case "matrix":
			if len(v) < 6 {
				continue
			}
			t2 = aff{v[0], v[1], v[2], v[3], v[4], v[5]}
		case "translate":
			t2 = aff{1, 0, 0, 1, get(0, 0), get(1, 0)}
		case "scale":
			t2 = aff{get(0, 1), 0, 0, get(1, get(0, 1)), 0, 0}
		case "rotate":
			a := get(0, 0) * math.Pi / 180
			c, sn := math.Cos(a), math.Sin(a)
			t2 = aff{c, sn, -sn, c, 0, 0}
			if len(v) >= 3 {
				t2 = mul(aff{1, 0, 0, 1, v[1], v[2]}, mul(t2, aff{1, 0, 0, 1, -v[1], -v[2]}))
			}
		case "skewX":
			t2 = aff{1, 0, math.Tan(get(0, 0) * math.Pi / 180), 1, 0, 0}
		case "skewY":
			t2 = aff{1, math.Tan(get(0, 0) * math.Pi / 180), 0, 1, 0, 0}
		default:
			continue
		}
		m = mul(m, t2)
	}
	return m
}

type box struct {
	minX, minY, maxX, maxY float64
	any                    bool
}

func (b *box) add(x, y float64) {
	if !b.any {
		*b = box{x, y, x, y, true}
		return
	}
	b.minX, b.maxX = math.Min(b.minX, x), math.Max(b.maxX, x)
	b.minY, b.maxY = math.Min(b.minY, y), math.Max(b.maxY, y)
}

// pathPoints calls f for every on-curve point of a path (control points are skipped: a pin only
// needs its centre, and the extra precision of a Bézier extent isn't worth a parser).
func pathPoints(d string, f func(x, y float64)) {
	toks := cmdRe.FindAllString(d, -1)
	var cmd byte
	var cx, cy, sx, sy float64
	i := 0
	next := func() (float64, bool) {
		if i < len(toks) && !(len(toks[i]) == 1 && strings.ContainsAny(toks[i], "MmLlHhVvCcSsQqTtAaZz")) {
			v := parseNum(toks[i])
			i++
			return v, true
		}
		return 0, false
	}
	for i < len(toks) {
		if t := toks[i]; len(t) == 1 && strings.ContainsAny(t, "MmLlHhVvCcSsQqTtAaZz") {
			cmd = t[0]
			i++
			if cmd == 'Z' || cmd == 'z' {
				cx, cy = sx, sy
				continue
			}
		} else if cmd == 0 {
			i++
			continue
		}
		rel := cmd >= 'a'
		get := func(n int) ([]float64, bool) {
			vs := make([]float64, 0, n)
			for len(vs) < n {
				v, ok := next()
				if !ok {
					return nil, false
				}
				vs = append(vs, v)
			}
			return vs, true
		}
		switch cmd {
		case 'M', 'm', 'L', 'l', 'T', 't':
			v, ok := get(2)
			if !ok {
				continue
			}
			if rel {
				v[0], v[1] = v[0]+cx, v[1]+cy
			}
			cx, cy = v[0], v[1]
			if cmd == 'M' || cmd == 'm' {
				sx, sy = cx, cy
				if cmd == 'M' {
					cmd = 'L'
				} else {
					cmd = 'l'
				}
			}
		case 'H', 'h':
			v, ok := get(1)
			if !ok {
				continue
			}
			if rel {
				cx += v[0]
			} else {
				cx = v[0]
			}
		case 'V', 'v':
			v, ok := get(1)
			if !ok {
				continue
			}
			if rel {
				cy += v[0]
			} else {
				cy = v[0]
			}
		case 'C', 'c':
			v, ok := get(6)
			if !ok {
				continue
			}
			if rel {
				v[4], v[5] = v[4]+cx, v[5]+cy
			}
			cx, cy = v[4], v[5]
		case 'S', 's', 'Q', 'q':
			v, ok := get(4)
			if !ok {
				continue
			}
			if rel {
				v[2], v[3] = v[2]+cx, v[3]+cy
			}
			cx, cy = v[2], v[3]
		case 'A', 'a':
			v, ok := get(7)
			if !ok {
				continue
			}
			if rel {
				v[5], v[6] = v[5]+cx, v[6]+cy
			}
			cx, cy = v[5], v[6]
		default:
			i++
			continue
		}
		f(cx, cy)
	}
}

// SVGInfo is what the sketch tools need to know about a part's artwork.
type SVGInfo struct {
	W, H float64 // size in scene pixels
	// scale from SVG user units to scene pixels
	sx, sy   float64
	vbX, vbY float64
	data     []byte
}

func unitToPx(v string, illustrator bool) (float64, bool) {
	v = strings.TrimSpace(v)
	m := numRe.FindString(v)
	if m == "" {
		return 0, false
	}
	n := parseNum(m)
	switch strings.TrimSpace(strings.TrimPrefix(v, m)) {
	case "in":
		return n * pxPerInch, true
	case "mm":
		return n / mmPerInch * pxPerInch, true
	case "cm":
		return n / 2.54 * pxPerInch, true
	case "pt":
		return n / 72 * pxPerInch, true
	case "pc":
		return n / 6 * pxPerInch, true
	default: // px or unitless: Illustrator files are 72 dpi, everything else 90
		if illustrator {
			return n / 72 * pxPerInch, true
		}
		return n, true
	}
}

// ParseSVG reads an SVG's size and viewBox.
func ParseSVG(data []byte) (*SVGInfo, bool) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	illustrator := bytes.Contains(data[:min(len(data), 2048)], []byte("Illustrator"))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "svg" {
			continue
		}
		var w, h string
		var vb []float64
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "width":
				w = a.Value
			case "height":
				h = a.Value
			case "viewBox":
				vb = floats(a.Value)
			}
		}
		info := &SVGInfo{data: data, sx: 1, sy: 1}
		wpx, wok := unitToPx(w, illustrator)
		hpx, hok := unitToPx(h, illustrator)
		if len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
			if !wok {
				wpx, wok = vb[2], true
			}
			if !hok {
				hpx, hok = vb[3], true
			}
			info.sx, info.sy = wpx/vb[2], hpx/vb[3]
			info.vbX, info.vbY = vb[0], vb[1]
		}
		if !wok || !hok {
			return nil, false
		}
		info.W, info.H = wpx, hpx
		return info, true
	}
}

// Pin returns where a wire attaches to the element with the given ids, in the part's own
// coordinates (scene pixels, origin at the part's top-left). The terminal point is preferred
// when the part names one, and the free end of a bendable leg when it has one.
func (s *SVGInfo) Pin(svgID, terminalID, legID string) (x, y float64, ok bool) {
	for _, id := range []string{legID, terminalID, svgID} {
		if id == "" {
			continue
		}
		b, found := s.elementBox(id)
		if !found {
			continue
		}
		px := func(ux, uy float64) (float64, float64) {
			return (ux - s.vbX) * s.sx, (uy - s.vbY) * s.sy
		}
		if id == legID {
			// a leg is a line from the body to its end; the end is the corner farthest from the centre
			cx, cy := s.W/2, s.H/2
			ax, ay := px(b.minX, b.minY)
			bx, by := px(b.maxX, b.maxY)
			cx1, cy1 := px(b.minX, b.maxY)
			dx, dy := px(b.maxX, b.minY)
			best, bd := [2]float64{ax, ay}, -1.0
			for _, p := range [][2]float64{{ax, ay}, {bx, by}, {cx1, cy1}, {dx, dy}} {
				if d := math.Hypot(p[0]-cx, p[1]-cy); d > bd {
					best, bd = p, d
				}
			}
			return best[0], best[1], true
		}
		x, y = px((b.minX+b.maxX)/2, (b.minY+b.maxY)/2)
		return x, y, true
	}
	return 0, 0, false
}

// elementBox finds the element with id and returns its bounding box in the SVG's user units.
func (s *SVGInfo) elementBox(id string) (box, bool) {
	dec := xml.NewDecoder(bytes.NewReader(s.data))
	dec.Strict = false
	stack := []aff{identity}
	var target aff
	depth, found := 0, false
	var b box
	for {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			return b, found && b.any
		}
		switch t := tok.(type) {
		case xml.StartElement:
			at := map[string]string{}
			for _, a := range t.Attr {
				at[a.Name.Local] = a.Value
			}
			m := stack[len(stack)-1]
			if tr := at["transform"]; tr != "" {
				m = mul(m, parseTransform(tr))
			}
			stack = append(stack, m)
			if !found && at["id"] == id {
				found, target, depth = true, m, len(stack)
			}
			if found {
				// the element itself, and shapes nested inside it when it is a group
				addShape(&b, t.Name.Local, at, m)
				_ = target
			}
		case xml.EndElement:
			if found && len(stack) == depth {
				return b, b.any
			}
			stack = stack[:len(stack)-1]
		}
	}
}

func addShape(b *box, name string, at map[string]string, m aff) {
	f := func(k string) float64 { return parseNum(at[k]) }
	add := func(x, y float64) { b.add(m.apply(x, y)) }
	switch name {
	case "rect":
		x, y, w, h := f("x"), f("y"), f("width"), f("height")
		add(x, y)
		add(x+w, y)
		add(x, y+h)
		add(x+w, y+h)
	case "circle":
		cx, cy, r := f("cx"), f("cy"), f("r")
		add(cx-r, cy-r)
		add(cx+r, cy-r)
		add(cx-r, cy+r)
		add(cx+r, cy+r)
	case "ellipse":
		cx, cy, rx, ry := f("cx"), f("cy"), f("rx"), f("ry")
		add(cx-rx, cy-ry)
		add(cx+rx, cy-ry)
		add(cx-rx, cy+ry)
		add(cx+rx, cy+ry)
	case "line":
		add(f("x1"), f("y1"))
		add(f("x2"), f("y2"))
	case "polyline", "polygon":
		v := floats(at["points"])
		for i := 0; i+1 < len(v); i += 2 {
			add(v[i], v[i+1])
		}
	case "path":
		pathPoints(at["d"], add)
	}
}
