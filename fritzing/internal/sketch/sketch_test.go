package sketch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

func catalog(t *testing.T) *parts.Catalog {
	t.Helper()
	dir := os.Getenv("FRITZING_TEST_DATA")
	if dir == "" {
		t.Skip("FRITZING_TEST_DATA not set")
	}
	c, err := parts.Open(filepath.Join(dir, "parts.db"), filepath.Join(dir, "parts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestRoundTrip(t *testing.T) {
	s, err := Parse(project.Template)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.String(); got != project.Template {
		t.Errorf("round trip changed the template:\n%s", got)
	}
	if _, err := Parse("<module"); err == nil {
		t.Error("broken xml accepted")
	}
	if _, err := Parse("<foo/>"); err == nil {
		t.Error("wrong root accepted")
	}
}

func TestBuildCircuit(t *testing.T) {
	cat := catalog(t)
	s, _ := Parse(project.Template)

	led, err := s.AddPart(cat, "5mmColorLEDModuleID", AddOptions{Properties: map[string]string{"color": "Red (633nm)"}})
	if err != nil || led != "LED1" {
		t.Fatalf("led: %q %v", led, err)
	}
	r, err := s.AddPart(cat, "ResistorModuleID", AddOptions{Properties: map[string]string{"resistance": "220"}, Rotation: 90})
	if err != nil || r != "R1" {
		t.Fatalf("resistor: %q %v", r, err)
	}
	if _, err := s.AddPart(cat, "NoSuchModuleID", AddOptions{}); err == nil {
		t.Error("unknown part added")
	}
	if _, err := s.AddPart(cat, "5mmColorLEDModuleID", AddOptions{Title: "LED1"}); err == nil {
		t.Error("duplicate title accepted")
	}
	for _, view := range parts.Views {
		w, err := s.Connect(cat, view, Endpoint{"LED1", "anode"}, Endpoint{"R1", "connector0"}, ConnectOptions{})
		if err != nil {
			t.Fatalf("connect %s: %v", view, err)
		}
		if !strings.HasPrefix(w[0], "Wire") {
			t.Errorf("wire title %q", w)
		}
	}
	if _, err := s.Connect(cat, "schematic", Endpoint{"LED1", "nope"}, Endpoint{"R1", "connector0"}, ConnectOptions{}); err == nil {
		t.Error("unknown connector accepted")
	}

	sum := s.Describe(cat)
	if len(sum.Parts) != 2 || len(sum.Wires) != 3 {
		t.Fatalf("summary: %+v", sum)
	}
	nets := map[string][]string{}
	for _, n := range sum.Nets {
		nets[n.View] = n.Pins
	}
	for _, view := range parts.Views {
		if got := strings.Join(nets[view], " "); got != "LED1.connector1 R1.connector0" {
			t.Errorf("%s net = %q", view, got)
		}
	}
	if sum.Parts[1].Positions["breadboard"].Rotation != 90 {
		t.Errorf("rotation lost: %+v", sum.Parts[1])
	}

	// moving a part drags its wires
	x, y := 50.0, 20.0
	if err := s.Move(cat, "LED1", "schematic", &x, &y, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProperty("R1", "resistance", "1k"); err != nil {
		t.Fatal(err)
	}
	if out := s.String(); !strings.Contains(out, `value="1k"`) || strings.Contains(out, `value="220"`) {
		t.Error("property not updated")
	}

	// removing a part takes its wires and every back reference with it
	gone, err := s.Remove("R1")
	if err != nil || len(gone) != 4 {
		t.Fatalf("remove: %v %v", gone, err)
	}
	if out := s.String(); strings.Contains(out, "<connect ") {
		t.Errorf("dangling connects left:\n%s", out)
	}
	if _, err := Parse(s.String()); err != nil {
		t.Errorf("output not parseable: %v", err)
	}
	if dir := os.Getenv("FRITZING_TEST_OUT"); dir != "" {
		s2, _ := Parse(project.Template)
		s2.AddPart(cat, "5mmColorLEDModuleID", AddOptions{})
		s2.AddPart(cat, "ResistorModuleID", AddOptions{Properties: map[string]string{"resistance": "220"}})
		for _, v := range parts.Views {
			if _, err := s2.Connect(cat, v, Endpoint{"LED1", "anode"}, Endpoint{"R1", "connector0"}, ConnectOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		os.WriteFile(filepath.Join(dir, "sketch.fz"), []byte(s2.String()), 0o644)
	}
}
