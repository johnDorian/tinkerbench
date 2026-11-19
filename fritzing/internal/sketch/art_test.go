package sketch

import (
	"fmt"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

// renderedChip is what Fritzing would write for a chip at (xmm, ymm): pin rects in 1000 units per
// inch, pin1 moved by shift1 units to imitate artwork the exporter draws in the wrong place.
func renderedChip(partID int, xmm, ymm, shift1 float64) []byte {
	return renderedChips([]chipAt{{partID, xmm, ymm, shift1}}, 0)
}

type chipAt struct {
	id       int
	xmm, ymm float64
	shift1   float64
}

// renderedChips draws several chips and shifts the whole drawing by crop mm (the exporter crops
// the view to its content, so everything moves together).
func renderedChips(cs []chipAt, cropMM float64) []byte {
	k := 1000.0 / pxPerInch
	var b strings.Builder
	fmt.Fprintf(&b, "<?xml version='1.0'?>\n<svg xmlns='http://www.w3.org/2000/svg' width='3in' height='3in' viewBox='0 0 3000 3000'>\n")
	for _, c := range cs {
		x, y := (mmToPx(c.xmm)-mmToPx(cropMM))*k, (mmToPx(c.ymm)-mmToPx(cropMM))*k
		chipGroup(&b, c.id, x, y, c.shift1, k)
	}
	b.WriteString("</svg>")
	return []byte(b.String())
}

func chipGroup(b *strings.Builder, partID int, x, y, shift1, k float64) {
	fmt.Fprintf(b, "<path d='M0,0L1,1'/><g partID='%d'><g transform='translate(%.2f,%.2f)'><g id='breadboard'>", partID, x, y)
	for i, p := range [][2]float64{{0, 0}, {34, 0}, {0, 34}, {34, 34}} {
		dx := 0.0
		if i == 1 {
			dx = shift1
		}
		fmt.Fprintf(b, "<rect id='pin%d' x='%.2f' y='%.2f' width='%.2f' height='%.2f'/>", i, p[0]*k+dx, p[1]*k, 6*k, 6*k)
	}
	b.WriteString("</g></g></g>")
}

func TestAuditArt(t *testing.T) {
	s, _ := Parse(project.Template)
	placeIn(t, s, "breadboard", "A", 10, 5)
	ia, err := s.find("A")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.AuditArt(fakeLib{}, "breadboard", renderedChip(ia.index*10, 10, 5, 0)); len(got) != 0 {
		t.Fatalf("pins drawn where they belong: %+v", got)
	}
	got := s.AuditArt(fakeLib{}, "breadboard", renderedChip(ia.index*10, 10, 5, 300)) // 300 units = 7.6 mm
	if len(got) != 1 || got[0].Kind != "art-mismatch" || got[0].Level != LevelWarn {
		t.Fatalf("want one art-mismatch warning: %+v", got)
	}
	for _, want := range []string{"A (chip, breadboard view)", "1 of 4 connectors", "P1 is drawn at"} {
		if !strings.Contains(got[0].Text, want) {
			t.Errorf("missing %q in %s", want, got[0].Text)
		}
	}
	// a part missing from the render, or an empty render, is not an error
	if got := s.AuditArt(fakeLib{}, "breadboard", renderedChip(7, 10, 5, 300)); len(got) != 0 {
		t.Errorf("other part id: %+v", got)
	}
	if got := s.AuditArt(fakeLib{}, "breadboard", nil); len(got) != 0 {
		t.Errorf("empty: %+v", got)
	}
}

func TestAuditArtIgnoresCropOffset(t *testing.T) {
	s, _ := Parse(project.Template)
	placeIn(t, s, "breadboard", "A", 10, 5)
	placeIn(t, s, "breadboard", "B", 40, 5)
	ia, _ := s.find("A")
	ib, _ := s.find("B")
	// the exporter cropped the view 5 mm from the top-left: every pin moved together, nothing is wrong
	if got := s.AuditArt(fakeLib{}, "breadboard", renderedChips([]chipAt{{ia.index * 10, 10, 5, 0}, {ib.index * 10, 40, 5, 0}}, 5)); len(got) != 0 {
		t.Fatalf("a uniform shift is not a mismatch: %+v", got)
	}
	// ... but one chip with a stray pin is still caught
	got := s.AuditArt(fakeLib{}, "breadboard", renderedChips([]chipAt{{ia.index * 10, 10, 5, 0}, {ib.index * 10, 40, 5, 300}}, 5))
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "B (chip") {
		t.Fatalf("want B flagged: %+v", got)
	}
}
