package parts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests need a real parts library: FRITZING_TEST_DATA is a folder with parts.db and parts/
// (from /usr/share/fritzing in the image: parts.db.distrib and parts/).
func open(t *testing.T) *Catalog {
	t.Helper()
	dir := os.Getenv("FRITZING_TEST_DATA")
	if dir == "" {
		t.Skip("FRITZING_TEST_DATA not set")
	}
	c, err := Open(filepath.Join(dir, "parts.db"), filepath.Join(dir, "parts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSearch(t *testing.T) {
	c := open(t)
	hits, err := c.Search(Query{Text: "red led 5mm"})
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	found := false
	for _, h := range hits {
		if h.ModuleID == "5mmColorLEDModuleID" {
			found = true
		}
		if h.Obsolete {
			t.Errorf("obsolete part listed: %+v", h)
		}
	}
	if !found {
		t.Errorf("5mm LED not found in %+v", hits)
	}
	// narrowing by property
	hits, err = c.Search(Query{Family: "resistor", Properties: map[string]string{"package": "THT"}, Limit: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("resistor THT: %v %v", hits, err)
	}
	for _, h := range hits {
		if !strings.Contains(strings.ToLower(h.Family), "resistor") || !strings.HasPrefix(strings.ToLower(h.Properties["package"]), "tht") {
			t.Errorf("not a THT resistor: %+v", h)
		}
	}
	if hits, _ := c.Search(Query{Text: "100%_;drop table parts"}); len(hits) != 0 {
		t.Errorf("odd input matched: %v", hits)
	}
}

func TestGetAndFZP(t *testing.T) {
	c := open(t)
	p, err := c.Get("5mmColorLEDModuleID")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Connectors) != 2 || p.Connectors[0].Name != "cathode" || len(p.Views) != 3 {
		t.Errorf("part: %+v", p)
	}
	f, _, err := c.FZP(p.ModuleID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Layer(Breadboard) != "breadboard" || f.Layer(PCB) != "copper0" || f.Layer(Schematic) != "schematic" {
		t.Errorf("layers: %q %q %q", f.Layer(Breadboard), f.Layer(PCB), f.Layer(Schematic))
	}
	k, err := f.Connector("anode")
	if err != nil || k.ID != "connector1" {
		t.Fatalf("anode: %v %v", k, err)
	}
	if pins := k.Pins(Schematic); len(pins) != 1 || pins[0].SvgID != "connector1pin" || pins[0].TerminalID != "connector1terminal" {
		t.Errorf("schematic pins: %+v", pins)
	}
	for _, v := range append(Views, "icon") {
		if b, err := c.SVG(p.ModuleID, v); err != nil || !strings.Contains(string(b), "<svg") {
			t.Errorf("svg %s: %v", v, err)
		}
	}
	if _, err := c.Get("nope"); err == nil {
		t.Error("unknown part found")
	}
	if _, err := c.SVGFile("core/x.fzp", "../../../etc/passwd"); err == nil {
		t.Error("path escape allowed")
	}
}

func TestFacets(t *testing.T) {
	c := open(t)
	fs, err := c.Families("led", 5)
	if err != nil || len(fs) == 0 {
		t.Fatalf("%v %v", fs, err)
	}
	vs, err := c.PropertyValues("package", "resistor", 5)
	if err != nil || len(vs) == 0 {
		t.Fatalf("%v %v", vs, err)
	}
}
