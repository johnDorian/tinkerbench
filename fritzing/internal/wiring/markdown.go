package wiring

import (
	"fmt"
	"strings"
)

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// Markdown renders the description the way the UI shows it: warnings, notes, connections, pin
// usage, parts.
func (w Wiring) Markdown() string {
	var b strings.Builder
	title := w.Project
	if title == "" {
		title = "Project"
	}
	fmt.Fprintf(&b, "# %s: wiring\n\n", title)
	if w.Stale {
		fmt.Fprintf(&b, "> Notes were written at sketch v%d, the sketch is now v%d: check them.\n\n", w.NotesVersion, w.SketchVersion)
	}
	var warn, info []Warning
	for _, x := range w.Warnings {
		if x.Level == "info" {
			info = append(info, x)
		} else {
			warn = append(warn, x)
		}
	}
	if len(warn) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, x := range warn {
			if x.Level == "error" {
				fmt.Fprintf(&b, "- **ERROR:** %s\n", x.Text)
			} else {
				fmt.Fprintf(&b, "- %s\n", x.Text)
			}
		}
		b.WriteString("\n")
	}
	if n := strings.TrimSpace(w.Notes); n != "" {
		b.WriteString(n + "\n\n")
	} else {
		b.WriteString("_No notes yet. Write purpose, power, build steps and what to check with set_notes._\n\n")
	}

	fmt.Fprintf(&b, "## Connections (%s view)\n\n", w.View)
	if len(w.Connections) == 0 {
		b.WriteString("_Nothing is connected in this view._\n\n")
	} else {
		colour := false
		for _, c := range w.Connections {
			colour = colour || c.Color != ""
		}
		if colour {
			b.WriteString("| Net | From | To | Wire |\n|---|---|---|---|\n")
		} else {
			b.WriteString("| Net | From | To |\n|---|---|---|\n")
		}
		for _, c := range w.Connections {
			if colour {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", cell(c.Net), cell(c.From), cell(c.To), cell(c.Color))
			} else {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(c.Net), cell(c.From), cell(c.To))
			}
		}
		b.WriteString("\n")
	}

	if l := w.Layout; l != nil && l.Wires > 0 {
		fmt.Fprintf(&b, "Layout: %d wires, %.0f mm of wire, %d crossings.\n\n", l.Wires, l.LengthMM, l.Crossings)
	}

	if len(w.PinUsage) > 0 {
		b.WriteString("## Pins in use\n\n")
		for _, p := range w.PinUsage {
			fmt.Fprintf(&b, "**%s**\n\n", p.Part)
			for _, l := range p.Pins {
				fmt.Fprintf(&b, "- %s, net %s → %s\n", l.Pin, l.Net, strings.Join(l.Goes, ", "))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Parts\n\n")
	if len(w.Parts) == 0 {
		b.WriteString("_The sketch has no parts._\n\n")
	} else {
		b.WriteString("| Qty | Part | Value | Package | Titles |\n|---|---|---|---|---|\n")
		for _, p := range w.Parts {
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", p.Count, cell(p.Name), cell(p.Properties), cell(p.Package), strings.Join(p.Titles, ", "))
		}
		b.WriteString("\n")
	}
	if len(info) > 0 {
		b.WriteString("## Notes from the checker\n\n")
		for _, x := range info {
			fmt.Fprintf(&b, "- %s\n", x.Text)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
