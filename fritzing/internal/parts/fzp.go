package parts

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// FZP is the part of a Fritzing part definition (.fzp) that the sketch tools need.
type FZP struct {
	ModuleID    string   `xml:"moduleId,attr"`
	Title       string   `xml:"title"`
	Label       string   `xml:"label"`
	Description string   `xml:"description"`
	Tags        []string `xml:"tags>tag"`
	Props       []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:",chardata"`
	} `xml:"properties>property"`
	Views struct {
		Icon       fzpView `xml:"iconView"`
		Breadboard fzpView `xml:"breadboardView"`
		Schematic  fzpView `xml:"schematicView"`
		PCB        fzpView `xml:"pcbView"`
	} `xml:"views"`
	Connectors []FZPConnector `xml:"connectors>connector"`
	Buses      []struct {
		ID      string `xml:"id,attr"`
		Members []struct {
			ConnectorID string `xml:"connectorId,attr"`
		} `xml:"nodeMember"`
	} `xml:"buses>bus"`
}

type fzpView struct {
	Layers struct {
		Image string `xml:"image,attr"`
		Layer []struct {
			ID string `xml:"layerId,attr"`
		} `xml:"layer"`
	} `xml:"layers"`
}

// FZPConnector is one pin of a part and where it is drawn in each view.
type FZPConnector struct {
	ID          string `xml:"id,attr"`
	Name        string `xml:"name,attr"`
	Type        string `xml:"type,attr"`
	Description string `xml:"description"`
	Views       struct {
		Breadboard fzpPins `xml:"breadboardView"`
		Schematic  fzpPins `xml:"schematicView"`
		PCB        fzpPins `xml:"pcbView"`
	} `xml:"views"`
}

type fzpPins struct {
	P []Pin `xml:"p"`
}

// Pin is the SVG element that draws a connector on one layer.
type Pin struct {
	Layer      string `xml:"layer,attr"`
	SvgID      string `xml:"svgId,attr"`
	LegID      string `xml:"legId,attr"`
	TerminalID string `xml:"terminalId,attr"`
}

// The three views of a sketch.
const (
	Breadboard = "breadboard"
	Schematic  = "schematic"
	PCB        = "pcb"
)

// Views lists the view names in the order the UI shows them.
var Views = []string{Breadboard, Schematic, PCB}

// ParseFZP reads a part definition.
func ParseFZP(b []byte) (*FZP, error) {
	var f FZP
	if err := xml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse fzp: %w", err)
	}
	if f.ModuleID == "" {
		return nil, fmt.Errorf("parse fzp: no moduleId")
	}
	f.Title = strings.TrimSpace(f.Title)
	return &f, nil
}

func (f *FZP) view(v string) *fzpView {
	switch v {
	case "icon":
		return &f.Views.Icon
	case Breadboard:
		return &f.Views.Breadboard
	case Schematic:
		return &f.Views.Schematic
	case PCB:
		return &f.Views.PCB
	}
	return nil
}

// Layer is the layer an instance of the part sits on in view (its first layer, which is what
// Fritzing writes into the .fz); "" if the part has no such view.
func (f *FZP) Layer(view string) string {
	if v := f.view(view); v != nil && len(v.Layers.Layer) > 0 {
		return v.Layers.Layer[0].ID
	}
	return ""
}

// Image is the part's SVG for a view, relative to parts/svg/<core|contrib|...>/.
func (f *FZP) Image(view string) string {
	if v := f.view(view); v != nil {
		return v.Layers.Image
	}
	return ""
}

// Connector looks a pin up by id ("connector3") or, if no id matches, by a unique name ("anode").
func (f *FZP) Connector(idOrName string) (*FZPConnector, error) {
	for i := range f.Connectors {
		if f.Connectors[i].ID == idOrName {
			return &f.Connectors[i], nil
		}
	}
	var hit *FZPConnector
	for i := range f.Connectors {
		if strings.EqualFold(f.Connectors[i].Name, idOrName) {
			if hit != nil {
				return nil, fmt.Errorf("%s: connector name %q is ambiguous, use the id (see get_part)", f.ModuleID, idOrName)
			}
			hit = &f.Connectors[i]
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("%s has no connector %q (see get_part)", f.ModuleID, idOrName)
	}
	return hit, nil
}

// Pins returns the SVG elements of a connector in a view; the first is the one wires attach to.
func (c *FZPConnector) Pins(view string) []Pin {
	switch view {
	case Breadboard:
		return c.Views.Breadboard.P
	case Schematic:
		return c.Views.Schematic.P
	case PCB:
		return c.Views.PCB.P
	}
	return nil
}

// ValidView reports whether v is breadboard, schematic or pcb.
func ValidView(v string) bool { return v == Breadboard || v == Schematic || v == PCB }
