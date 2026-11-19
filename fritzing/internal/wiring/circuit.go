package wiring

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

// Circuit runs electrical sanity checks on the nets of one view, going by pin names (GND, 5V, VCC,
// anode ...). It finds the mistakes a picture hides: a supply wired to ground, two rails on one
// net, a part with no ground, an LED straight across the supply. Levels: "error" (will not work or
// can damage something), "warn" (probably wrong), "info" (worth knowing).
func Circuit(in Input, view string) []Warning {
	if !parts.ValidView(view) {
		view = parts.Breadboard
	}
	nm := &namer{res: in.Res, fzp: map[string]*parts.FZP{}, pins: map[string]map[string]pinInfo{}, title: map[string]string{}}
	for _, p := range in.Summary.Parts {
		nm.title[p.Title] = p.ModuleID
	}
	skip := func(part string) bool { return nm.isBreadboard(nm.title[part]) }

	var nets [][]string // pins of every net, breadboard holes removed
	netOf := map[string]int{}
	for _, sn := range in.Summary.Nets {
		if sn.View != view {
			continue
		}
		var pins []string
		for _, p := range sn.Pins {
			if t, _, _ := strings.Cut(p, "."); !skip(t) {
				pins = append(pins, p)
			}
		}
		if len(pins) < 2 {
			continue
		}
		for _, p := range pins {
			netOf[p] = len(nets)
		}
		nets = append(nets, pins)
	}

	out := []Warning{}
	add := func(level, kind, text string) {
		out = append(out, Warning{Level: level, Kind: kind, Text: fmt.Sprintf("%s (%s)", text, view)})
	}

	// supply and ground on one net
	for _, pins := range nets {
		var gnd, sup []string
		rails := map[string][]string{}
		for _, p := range pins {
			switch r := nm.role(p); r {
			case "":
			case roleGround:
				gnd = append(gnd, nm.label(p))
			default:
				sup = append(sup, nm.label(p))
				if r == "5 V" || r == "3.3 V" {
					rails[r] = append(rails[r], nm.label(p))
				}
			}
		}
		if len(gnd) > 0 && len(sup) > 0 {
			add("error", "power-short", fmt.Sprintf("a supply pin and ground are on the same net: %s and %s", strings.Join(sup, ", "), strings.Join(gnd, ", ")))
		}
		if len(rails) > 1 {
			var l []string
			for r, p := range rails {
				l = append(l, r+" ("+strings.Join(p, ", ")+")")
			}
			sort.Strings(l)
			add("error", "rail-short", "two different supply voltages are joined: "+strings.Join(l, " and "))
		}
	}

	// parts that use the circuit but have no ground or no supply
	for _, p := range in.Summary.Parts {
		if skip(p.Title) {
			continue
		}
		f := nm.load(p.ModuleID)
		if f == nil {
			continue
		}
		var used, hasGnd, gndUsed, hasSup, supUsed int
		for _, c := range f.Connectors {
			pin := p.Title + "." + c.ID
			_, connected := netOf[pin]
			if connected {
				used++
			}
			switch nm.role(pin) {
			case roleGround:
				hasGnd++
				if connected {
					gndUsed++
				}
			case "":
			default:
				hasSup++
				if connected {
					supUsed++
				}
			}
		}
		if used == 0 {
			continue // an unused part is reported as "unconnected"
		}
		if hasGnd > 0 && gndUsed == 0 {
			add("warn", "no-ground", p.Title+" has ground pins but none is connected")
		}
		if hasSup > 0 && supUsed == 0 && hasGnd > 0 {
			add("info", "no-power", p.Title+" has supply pins but none is connected")
		}
	}

	// an LED straight across the supply
	for _, p := range in.Summary.Parts {
		f := nm.load(p.ModuleID)
		if f == nil || skip(p.Title) {
			continue
		}
		var anode, cathode string
		for _, c := range f.Connectors {
			switch strings.ToLower(strings.TrimSpace(c.Name)) {
			case "anode", "+":
				anode = p.Title + "." + c.ID
			case "cathode", "-":
				cathode = p.Title + "." + c.ID
			}
		}
		if anode == "" || cathode == "" || !strings.Contains(strings.ToLower(f.Title+" "+p.ModuleID), "led") {
			continue
		}
		a, okA := netOf[anode]
		c, okC := netOf[cathode]
		if okA && okC && a != c && netHas(nm, nets[a], false) && netHas(nm, nets[c], true) {
			add("warn", "led-no-resistor", p.Title+" sits directly between a supply and ground: it needs a series resistor")
		}
	}

	// ESP8266 boot-strapping pins
	for _, p := range in.Summary.Parts {
		f := nm.load(p.ModuleID)
		if f == nil || skip(p.Title) {
			continue
		}
		mod := strings.ToLower(f.Title + " " + p.ModuleID)
		if !strings.Contains(mod, "wemos") && !strings.Contains(mod, "nodemcu") && !strings.Contains(mod, "esp8266") && !strings.Contains(mod, "esp-8266") {
			continue
		}
		for _, c := range f.Connectors {
			note := bootPins[strings.ToUpper(strings.TrimSpace(c.Name))]
			if _, ok := netOf[p.Title+"."+c.ID]; ok && note != "" {
				add("info", "boot-pin", fmt.Sprintf("%s.%s %s", p.Title, strings.TrimSpace(c.Name), note))
			}
		}
	}

	sort.SliceStable(out, func(a, b int) bool { return levelOrder[out[a].Level] < levelOrder[out[b].Level] })
	return out
}

var levelOrder = map[string]int{"error": 0, "warn": 1, "info": 2}

// bootPins are the ESP8266 pins that decide how it starts; D1 mini / NodeMCU names.
var bootPins = map[string]string{
	"D3": "is GPIO0: it must be high at boot (low enters the flasher), so nothing may pull it low",
	"D4": "is GPIO2: it must be high at boot and drives the on-board LED",
	"D8": "is GPIO15: it must be low at boot, so nothing may pull it high",
}

const roleGround = "gnd"

// role classifies a pin by its name: roleGround, a supply rail ("5 V", "3.3 V", "VCC", "VIN") or "".
func (n *namer) role(pin string) string {
	part, id, _ := strings.Cut(pin, ".")
	m, ok := n.title[part]
	if !ok {
		return ""
	}
	f := n.load(m)
	if f == nil {
		return ""
	}
	for _, c := range f.Connectors {
		if c.ID != id {
			continue
		}
		name := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(c.Name), " ", ""))
		desc := strings.ToLower(c.Description)
		switch {
		case name == "G" && (strings.Contains(desc, "gate") || strings.Contains(strings.ToLower(f.Title+m), "fet")):
			return "" // a transistor's gate, not ground
		case name == "G", name == "GND", name == "GROUND", name == "VSS", name == "AGND", name == "DGND", strings.HasPrefix(name, "GND"):
			return roleGround
		case name == "5V", name == "+5V", name == "5V0", name == "VBUS", name == "USB5V":
			return "5 V"
		case name == "3V3", name == "3.3V", name == "+3V3", name == "3V":
			return "3.3 V"
		case name == "VIN":
			return "VIN"
		case name == "VCC", name == "VDD", name == "V+", name == "+V":
			return "VCC"
		}
	}
	return ""
}

// netHas reports whether a net contains a ground pin (ground=true) or a supply pin.
func netHas(n *namer, pins []string, ground bool) bool {
	for _, p := range pins {
		r := n.role(p)
		if ground && r == roleGround || !ground && r != "" && r != roleGround {
			return true
		}
	}
	return false
}
