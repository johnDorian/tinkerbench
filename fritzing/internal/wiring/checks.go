package wiring

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

// warnings finds view mismatches (a connection drawn in one view only) and loose ends.
func warnings(in Input, nm *namer, byView map[string][]vnet, wires map[string][]sketch.WireInfo, skip func(string) bool) []Warning {
	out := []Warning{}

	// connectivity per view: pin -> net index
	conn := map[string]map[string]int{}
	for v, list := range byView {
		m := map[string]int{}
		for i, n := range list {
			for _, p := range n.pins {
				m[p] = i
			}
		}
		conn[v] = m
	}

	// View consistency. Breadboard and schematic are always compared; pcb only once it has nets,
	// since most sketches never get a board.
	compare := []string{parts.Breadboard, parts.Schematic}
	if len(byView[parts.PCB]) > 0 {
		compare = append(compare, parts.PCB)
	}
	for _, a := range compare {
		for _, b := range compare {
			if a == b {
				continue
			}
			for _, n := range byView[a] {
				// pins of this net grouped by the net they are in in the other view
				groups := map[int][]string{}
				for _, p := range n.pins {
					g, ok := conn[b][p]
					if !ok {
						g = -1 - len(groups) // each unconnected pin on its own
					}
					groups[g] = append(groups[g], p)
				}
				if len(groups) < 2 {
					continue
				}
				// the biggest group is the part that is wired in b too
				var gs [][]string
				for _, g := range groups {
					gs = append(gs, g)
				}
				sort.Slice(gs, func(i, j int) bool {
					if len(gs[i]) != len(gs[j]) {
						return len(gs[i]) > len(gs[j])
					}
					return gs[i][0] < gs[j][0]
				})
				var parts_ []string
				for _, g := range gs {
					var l []string
					for _, p := range g {
						l = append(l, nm.label(p))
					}
					parts_ = append(parts_, strings.Join(l, " + "))
				}
				kind := "wired in " + a + " only"
				out = append(out, Warning{Level: "warn", Kind: "view-mismatch", Text: fmt.Sprintf(
					"%s: %s is joined in the %s but not in the %s (%s)",
					n.name, strings.Join(parts_, " and "), a, b, kind)})
			}
		}
	}

	// loose ends: wires with an open end
	for _, v := range parts.Views {
		for _, wi := range wires[v] {
			switch {
			case wi.From == "" && wi.To == "":
				out = append(out, Warning{Level: "warn", Kind: "open-wire", Text: fmt.Sprintf("%s (%s) is not connected at either end", wi.Title, v)})
			case wi.From == "" || wi.To == "":
				end := wi.From
				if end == "" {
					end = wi.To
				}
				out = append(out, Warning{Level: "warn", Kind: "open-wire", Text: fmt.Sprintf("%s (%s) has an open end, the other end is on %s", wi.Title, v, nm.label(end))})
			}
		}
	}

	// parts with no connections at all (in any view), and parts left out of a view
	for _, p := range in.Summary.Parts {
		if skip(p.Title + ".") {
			continue
		}
		used := false
		for _, v := range parts.Views {
			for pin := range conn[v] {
				if t, _, _ := strings.Cut(pin, "."); t == p.Title {
					used = true
				}
			}
		}
		if !used {
			out = append(out, Warning{Level: "warn", Kind: "unconnected", Text: p.Title + " has no connections in any view"})
		}
		if f := nm.load(p.ModuleID); f != nil {
			for _, v := range compare {
				if _, ok := p.Positions[v]; !ok && f.Image(v) != "" {
					out = append(out, Warning{Level: "info", Kind: "missing-in-view", Text: fmt.Sprintf("%s is not in the %s view", p.Title, v)})
				}
			}
		}
	}
	return out
}

// ---- parts list ----

// shown properties in this order; everything else is appended alphabetically, except a few
// that only repeat the title.
var skipProps = map[string]bool{"family": true, "editable pin labels": true, "chip label": true, "hole size": true}

func bom(ps []sketch.PartInfo, nm *namer) []BOMLine {
	type key struct{ module, props string }
	idx := map[key]int{}
	var out []BOMLine
	for _, p := range ps {
		props, pkg := fmtProps(p.Properties)
		k := key{p.ModuleID, props + "|" + pkg}
		i, ok := idx[k]
		if !ok {
			name := p.ModuleID
			if f := nm.load(p.ModuleID); f != nil && f.Title != "" {
				name = f.Title
			}
			out = append(out, BOMLine{Name: name, ModuleID: p.ModuleID, Properties: props, Package: pkg})
			i = len(out) - 1
			idx[k] = i
		}
		out[i].Titles = append(out[i].Titles, p.Title)
		out[i].Count++
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	if out == nil {
		out = []BOMLine{}
	}
	return out
}

func fmtProps(m map[string]string) (props, pkg string) {
	var names []string
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	var shown []string
	for _, k := range names {
		v := strings.TrimSpace(m[k])
		lk := strings.ToLower(k)
		if v == "" || skipProps[lk] {
			continue
		}
		if lk == "package" {
			pkg = v
			continue
		}
		switch lk {
		case "resistance":
			v = FormatResistance(v)
		case "capacitance":
			v = FormatCapacitance(v)
		}
		shown = append(shown, v)
	}
	return strings.Join(shown, ", "), pkg
}

// ---- unit formatting ----

var numUnit = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*([a-zA-Zµμ]*)\s*Ω?\s*$`)

func trimFloat(f float64) string {
	return strconv.FormatFloat(math.Round(f*1e6)/1e6, 'f', -1, 64)
}

// FormatResistance: "1000" -> "1 kΩ", "4.7k" -> "4.7 kΩ", "220Ω" -> "220 Ω".
func FormatResistance(v string) string {
	m := numUnit.FindStringSubmatch(v)
	if m == nil {
		return v
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return v
	}
	switch strings.ToLower(m[2]) {
	case "", "r", "ohm", "ohms":
	case "k":
		f *= 1e3
	case "m":
		if m[2] == "M" {
			f *= 1e6
		} else {
			f /= 1e3 // mΩ
		}
	case "g":
		f *= 1e9
	default:
		return v
	}
	switch {
	case f >= 1e9:
		return trimFloat(f/1e9) + " GΩ"
	case f >= 1e6:
		return trimFloat(f/1e6) + " MΩ"
	case f >= 1e3:
		return trimFloat(f/1e3) + " kΩ"
	}
	return trimFloat(f) + " Ω"
}

// FormatCapacitance: "100nF" -> "100 nF", "0.1uF" -> "100 nF", "1000000pF" -> "1 µF".
// A bare number is left alone, since the unit is not known.
func FormatCapacitance(v string) string {
	m := numUnit.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(v), "Ω"))
	if m == nil || m[2] == "" {
		return v
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return v
	}
	u := strings.TrimSuffix(strings.TrimSuffix(m[2], "F"), "f")
	switch u {
	case "p", "P":
		f *= 1e-12
	case "n", "N":
		f *= 1e-9
	case "u", "µ", "μ", "U":
		f *= 1e-6
	case "m":
		f *= 1e-3
	case "":
	default:
		return v
	}
	switch {
	case f >= 1:
		return trimFloat(f) + " F"
	case f >= 1e-3:
		return trimFloat(f*1e3) + " mF"
	case f >= 1e-6:
		return trimFloat(f*1e6) + " µF"
	case f >= 1e-9:
		return trimFloat(f*1e9) + " nF"
	}
	return trimFloat(f*1e12) + " pF"
}
