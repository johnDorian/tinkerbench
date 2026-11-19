package userparts

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
)

const svgBB = `<svg xmlns="http://www.w3.org/2000/svg" width="0.5in" height="0.3in" viewBox="0 0 50 30">
<rect id="body" x="5" y="5" width="40" height="20"/><rect id="connector0pin" x="0" y="10" width="5" height="2"/><rect id="connector1pin" x="45" y="10" width="5" height="2"/></svg>`

func fzp(id, extra string) string {
	return `<?xml version="1.0"?><module moduleId="` + id + `"><title>Test Widget</title><label>W</label>
<description>A widget for tests</description><tags><tag>widget</tag></tags>
<properties><property name="family">test family</property><property name="package">THT</property></properties>
<views>
<iconView><layers image="icon/widget.svg"><layer layerId="icon"/></layers></iconView>
<breadboardView><layers image="breadboard/widget.svg"><layer layerId="breadboard"/></layers></breadboardView>` + extra + `
</views>
<connectors>
<connector id="connector0" name="a" type="male"><description>first</description><views><breadboardView><p layer="breadboard" svgId="connector0pin"/></breadboardView></views></connector>
<connector id="connector1" name="b" type="male"><views><breadboardView><p layer="breadboard" svgId="` + "nope" + `"/></breadboardView></views></connector>
</connectors></module>`
}

func bundle(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			panic(err)
		}
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func good(id string) []byte {
	return bundle(map[string]string{"widget.fzp": fzp(id, ""), "svg.breadboard.widget.svg": svgBB, "svg.icon.widget.svg": svgBB})
}

func TestImportSearchDelete(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := l.Import(good("widget-1"), "upload", false, func(id string) bool { return id == "core-thing" })
	if err != nil {
		t.Fatal(err)
	}
	if res.ModuleID != "widget-1" || res.Connectors != 2 || len(res.Views) != 1 {
		t.Fatalf("%+v", res)
	}
	var sawPin, sawView bool
	for _, w := range res.Warnings {
		sawPin = sawPin || strings.Contains(w, "breadboard view: 1 of 2 connectors")
		sawView = sawView || strings.Contains(w, "no schematic view")
	}
	if !sawPin || !sawView {
		t.Errorf("warnings: %v", res.Warnings)
	}
	if _, err := l.Import(good("widget-1"), "upload", false, nil); err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("duplicate: %v", err)
	}
	if r, err := l.Import(good("widget-1"), "upload", true, nil); err != nil || !r.Replaced {
		t.Errorf("overwrite: %v %+v", err, r)
	}
	if _, err := l.Import(good("core-thing"), "upload", true, func(id string) bool { return id == "core-thing" }); err == nil || !strings.Contains(err.Error(), "own library") {
		t.Errorf("clash with core: %v", err)
	}

	hits := l.Search(parts.Query{Text: "widget tests"})
	if len(hits) != 1 || hits[0].Source != "user" || hits[0].Family != "test family" || hits[0].Properties["package"] != "THT" {
		t.Fatalf("%+v", hits)
	}
	if len(l.Search(parts.Query{Text: "nothing like it"})) != 0 || len(l.Search(parts.Query{})) != 1 {
		t.Error("search filters")
	}
	if len(l.Search(parts.Query{Properties: map[string]string{"package": "SMD"}})) != 0 || len(l.Search(parts.Query{Properties: map[string]string{"package": "th"}})) != 1 {
		t.Error("property filter")
	}
	p, err := l.Get("widget-1")
	if err != nil || len(p.Connectors) != 2 || p.Connectors[0].Description != "first" || len(p.Views) != 1 || p.Tags[0] != "widget" {
		t.Fatalf("%v %+v", err, p)
	}
	if b, err := l.SVG("widget-1", "icon"); err != nil || !bytes.Contains(b, []byte("<svg")) {
		t.Errorf("icon: %v", err)
	}
	if _, err := l.SVG("widget-1", "pcb"); err == nil {
		t.Error("pcb should be missing")
	}
	files, err := l.Files("widget-1")
	if err != nil || len(files) != 3 {
		t.Errorf("files %v %v", files, err)
	}

	l2, err := Open(dir) // survives a restart
	if err != nil || !l2.Has("widget-1") || l2.Source("widget-1") != "upload" {
		t.Fatalf("reopen: %v", err)
	}
	if err := l.Delete("widget-1"); err != nil || l.Has("widget-1") {
		t.Fatal(err)
	}
	if err := l.Delete("widget-1"); err == nil {
		t.Error("second delete")
	}
	if l3, _ := Open(dir); l3.Count() != 0 {
		t.Error("delete did not stick")
	}
}

func TestImportRejects(t *testing.T) {
	l, _ := Open(t.TempDir())
	cases := map[string][]byte{
		"not a zip":     []byte("hello"),
		"no fzp":        bundle(map[string]string{"a.svg": svgBB}),
		"two parts":     bundle(map[string]string{"a.fzp": fzp("a", ""), "b.fzp": fzp("b", ""), "svg.breadboard.widget.svg": svgBB}),
		"path":          bundle(map[string]string{"../evil.fzp": fzp("a", "")}),
		"missing art":   bundle(map[string]string{"widget.fzp": fzp("a", "")}),
		"script":        bundle(map[string]string{"widget.fzp": fzp("a", ""), "svg.breadboard.widget.svg": strings.Replace(svgBB, "<rect", "<script>1</script><rect", 1)}),
		"no size":       bundle(map[string]string{"widget.fzp": fzp("a", ""), "svg.breadboard.widget.svg": `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`}),
		"no moduleId":   bundle(map[string]string{"widget.fzp": strings.Replace(fzp("a", ""), `moduleId="a"`, "", 1), "svg.breadboard.widget.svg": svgBB}),
		"no connectors": bundle(map[string]string{"widget.fzp": `<module moduleId="x"><title>t</title><views><breadboardView><layers image="breadboard/widget.svg"/></breadboardView></views></module>`, "svg.breadboard.widget.svg": svgBB}),
	}
	for name, data := range cases {
		if _, err := l.Import(data, "upload", false, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if l.Count() != 0 {
		t.Error("something was stored")
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(good("fetched")) }))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "local address") {
		t.Errorf("loopback must be refused: %v", err)
	}
	b, err := fetch(context.Background(), srv.URL, nil) // the test server is on loopback
	if err != nil || len(b) == 0 {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("file url accepted")
	}
	u, _ := NormalizeURL("https://github.com/adafruit/Fritzing-Library/blob/master/parts/x.fzpz")
	if u != "https://raw.githubusercontent.com/adafruit/Fritzing-Library/master/parts/x.fzpz" {
		t.Error(u)
	}
}
