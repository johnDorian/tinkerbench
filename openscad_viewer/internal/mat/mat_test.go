package mat

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// segment is one stroked straight line from the content stream, in mm from the top-left,
// with the width it was stroked at.
type segment struct {
	x1, y1, x2, y2, width float64
}

// parseContent reads the stream of object 4 and returns its stroked segments (widths in mm)
// and the text anchors (baselines in mm, from the top-left).
func parseContent(t *testing.T, pdf []byte, h float64) ([]segment, [][2]float64) {
	t.Helper()
	s := string(pdf)
	start := strings.Index(s, "4 0 obj\n")
	if start < 0 {
		t.Fatal("no object 4")
	}
	body := s[start:]
	body = body[strings.Index(body, "stream\n")+len("stream\n"):]
	body = body[:strings.Index(body, "endstream")]

	toMM := func(pt float64) float64 { return pt * 25.4 / 72 }
	var segs []segment
	var texts [][2]float64
	width := 0.0
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[1] == "w":
			width = toMM(mustFloat(t, f[0]))
		case len(f) == 7 && f[2] == "m" && f[5] == "l" && f[6] == "S":
			segs = append(segs, segment{
				x1: toMM(mustFloat(t, f[0])), y1: h - toMM(mustFloat(t, f[1])),
				x2: toMM(mustFloat(t, f[3])), y2: h - toMM(mustFloat(t, f[4])),
				width: width,
			})
		case strings.Contains(line, " Td "):
			// "BT /F1 9 Tf x y Td (text) Tj ET": the Td operands are the two numbers after "Tf".
			for i, tok := range f {
				if tok == "Td" {
					x := toMM(mustFloat(t, f[i-2]))
					y := h - toMM(mustFloat(t, f[i-1]))
					texts = append(texts, [2]float64{x, y})
				}
			}
		}
	}
	return segs, texts
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("not a number %q: %v", s, err)
	}
	return v
}

// frame gives the outer top-left corner of the mat's frame, in mm, as the drawing places it.
func frame(m Mat, w float64) (ox, oy, outer float64) {
	outer = m.InnerMM + 2*bandMM
	return (w - outer) / 2, frameTopMM, outer
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestAll(t *testing.T) {
	want := []struct {
		name, paper string
		inner       float64
		title       string
	}{
		{"a4-100", "A4", 100, "Measuring mat, 100 mm"},
		{"a4-150", "A4", 150, "Measuring mat, 150 mm"},
		{"letter-100", "Letter", 100, "Measuring mat, 100 mm"},
		{"letter-150", "Letter", 150, "Measuring mat, 150 mm"},
	}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d variants, want %d", len(all), len(want))
	}
	for i, w := range want {
		got := all[i]
		if got.Name != w.name || got.Paper != w.paper || got.InnerMM != w.inner || got.Title != w.title {
			t.Errorf("All()[%d] = %+v, want %+v", i, got, w)
		}
		if m, ok := ByName(w.name); !ok || m != got {
			t.Errorf("ByName(%q) = %+v, %v", w.name, m, ok)
		}
	}
	if _, ok := ByName("a4-200"); ok {
		t.Error("ByName found a variant that does not exist")
	}
	if _, ok := ByName("../a4-100"); ok {
		t.Error("ByName accepted a path")
	}
}

func TestPDFStructure(t *testing.T) {
	for _, m := range All() {
		pdf := m.PDF()
		if !bytes.HasPrefix(pdf, []byte("%PDF-1.4\n")) {
			t.Errorf("%s: header %q", m.Name, pdf[:12])
		}
		if !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
			t.Errorf("%s: does not end with %%%%EOF", m.Name)
		}
		if bytes.Contains(pdf, []byte("CreationDate")) || bytes.Contains(pdf, []byte("ModDate")) {
			t.Errorf("%s: contains a timestamp", m.Name)
		}

		// startxref names the xref table, and every entry names the object it points at.
		re := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`)
		sm := re.FindSubmatch(pdf)
		if sm == nil {
			t.Fatalf("%s: no startxref", m.Name)
		}
		xref, _ := strconv.Atoi(string(sm[1]))
		if !bytes.HasPrefix(pdf[xref:], []byte("xref\n")) {
			t.Fatalf("%s: startxref %d does not point at xref", m.Name, xref)
		}
		table := strings.Split(string(pdf[xref:]), "\n")
		var count int
		if _, err := fmt.Sscanf(table[1], "0 %d", &count); err != nil {
			t.Fatalf("%s: xref header %q", m.Name, table[1])
		}
		if count != 8 {
			t.Errorf("%s: xref has %d entries, want 8 (free entry + 7 objects)", m.Name, count)
		}
		if table[2] != "0000000000 65535 f " {
			t.Errorf("%s: entry 0 is %q", m.Name, table[2])
		}
		for i := 1; i < count; i++ {
			entry := table[2+i]
			if len(entry) != 19 {
				t.Errorf("%s: entry %d is %d bytes, want 19 plus newline", m.Name, i, len(entry))
			}
			off, err := strconv.Atoi(entry[:10])
			if err != nil {
				t.Fatalf("%s: entry %d: %q", m.Name, i, entry)
			}
			want := fmt.Sprintf("%d 0 obj\n", i)
			if !bytes.HasPrefix(pdf[off:], []byte(want)) {
				t.Errorf("%s: entry %d points at %q, want %q", m.Name, i, pdf[off:off+12], want)
			}
		}
		if !strings.Contains(string(pdf), "/Root 1 0 R /Info 7 0 R") {
			t.Errorf("%s: trailer does not name Root and Info", m.Name)
		}
	}
}

func TestMediaBoxMatchesPaper(t *testing.T) {
	// Sizes in points, from the paper sizes in mm (A4 210 x 297, Letter 8.5 x 11 in).
	want := map[string][2]float64{
		"A4":     {210 * 72 / 25.4, 297 * 72 / 25.4},
		"Letter": {612, 792},
	}
	re := regexp.MustCompile(`/MediaBox \[0 0 ([\d.]+) ([\d.]+)\]`)
	for _, m := range All() {
		sm := re.FindSubmatch(m.PDF())
		if sm == nil {
			t.Fatalf("%s: no MediaBox", m.Name)
		}
		w, h := mustFloat(t, string(sm[1])), mustFloat(t, string(sm[2]))
		if !approx(w, want[m.Paper][0], 0.001) || !approx(h, want[m.Paper][1], 0.001) {
			t.Errorf("%s: MediaBox %g x %g, want %g x %g", m.Name, w, h, want[m.Paper][0], want[m.Paper][1])
		}
	}
}

func TestFrameFitsWithMargins(t *testing.T) {
	const margin = 10.0
	for _, m := range All() {
		size := paperSizes[m.Paper]
		w, h := size[0], size[1]
		segs, texts := parseContent(t, m.PDF(), h)
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		grow := func(x, y float64) {
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		for _, s := range segs {
			grow(s.x1, s.y1)
			grow(s.x2, s.y2)
		}
		for _, p := range texts {
			grow(p[0], p[1])
		}
		// The orientation triangle is a fill, not a segment: its corners are known from the layout.
		ox, oy, _ := frame(m, w)
		grow(ox-markGapMM-markLegMM, oy-markGapMM-markLegMM)

		if minX < margin || minY < margin || maxX > w-margin || maxY > h-margin {
			t.Errorf("%s: drawing spans x %.1f..%.1f, y %.1f..%.1f mm on a %g x %g page; need %g mm margins",
				m.Name, minX, maxX, minY, maxY, w, h, margin)
		}
		// The frame itself must be where the layout says, and its outer square is S+20.
		var gotOuter float64
		for _, s := range segs {
			if approx(s.y1, s.y2, 1e-3) && approx(s.y1, oy, 1e-3) {
				gotOuter = math.Max(gotOuter, math.Abs(s.x2-s.x1))
			}
		}
		if !approx(gotOuter, m.InnerMM+20, 1e-3) {
			t.Errorf("%s: outer top edge is %.3f mm, want %g", m.Name, gotOuter, m.InnerMM+20)
		}
	}
}

func TestInnerSquareEdges(t *testing.T) {
	for _, m := range All() {
		size := paperSizes[m.Paper]
		w, h := size[0], size[1]
		segs, _ := parseContent(t, m.PDF(), h)
		ox, oy, outer := frame(m, w)
		ix, iy := ox+bandMM, oy+bandMM
		ex, ey := ix+m.InnerMM, iy+m.InnerMM

		// Each edge of the white square is a 0.4 mm line that spans the whole square.
		edges := []struct {
			name string
			seg  segment
		}{
			{"top inner", segment{x1: ix, y1: iy, x2: ex, y2: iy}},
			{"bottom inner", segment{x1: ix, y1: ey, x2: ex, y2: ey}},
			{"left inner", segment{x1: ix, y1: iy, x2: ix, y2: ey}},
			{"right inner", segment{x1: ex, y1: iy, x2: ex, y2: ey}},
			{"top outer", segment{x1: ox, y1: oy, x2: ox + outer, y2: oy}},
			{"bottom outer", segment{x1: ox, y1: oy + outer, x2: ox + outer, y2: oy + outer}},
			{"left outer", segment{x1: ox, y1: oy, x2: ox, y2: oy + outer}},
			{"right outer", segment{x1: ox + outer, y1: oy, x2: ox + outer, y2: oy + outer}},
		}
		for _, e := range edges {
			found := false
			for _, s := range segs {
				if approx(s.width, edgeLineMM, 1e-3) && sameLine(s, e.seg) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: no %.1f mm line for the %s edge %v", m.Name, edgeLineMM, e.name, e.seg)
			}
		}
	}
}

// sameLine reports whether s covers the same line as want (its ends may be further out).
func sameLine(s, want segment) bool {
	const eps = 1e-3
	if approx(s.y1, s.y2, eps) && approx(want.y1, want.y2, eps) {
		return approx(s.y1, want.y1, eps) && math.Min(s.x1, s.x2) <= math.Min(want.x1, want.x2)+eps &&
			math.Max(s.x1, s.x2) >= math.Max(want.x1, want.x2)-eps
	}
	if approx(s.x1, s.x2, eps) && approx(want.x1, want.x2, eps) {
		return approx(s.x1, want.x1, eps) && math.Min(s.y1, s.y2) <= math.Min(want.y1, want.y2)+eps &&
			math.Max(s.y1, s.y2) >= math.Max(want.y1, want.y2)-eps
	}
	return false
}

func TestNoMarksInsideWhiteSquare(t *testing.T) {
	const eps = 1e-3
	for _, m := range All() {
		size := paperSizes[m.Paper]
		w, h := size[0], size[1]
		segs, _ := parseContent(t, m.PDF(), h)
		ox, oy, _ := frame(m, w)
		ix, iy := ox+bandMM, oy+bandMM
		ex, ey := ix+m.InnerMM, iy+m.InnerMM
		for _, s := range segs {
			if approx(s.y1, s.y2, eps) && s.y1 > iy+eps && s.y1 < ey-eps {
				lo, hi := math.Min(s.x1, s.x2), math.Max(s.x1, s.x2)
				if hi > ix+eps && lo < ex-eps {
					t.Errorf("%s: line %v crosses the white square", m.Name, s)
				}
			}
			if approx(s.x1, s.x2, eps) && s.x1 > ix+eps && s.x1 < ex-eps {
				lo, hi := math.Min(s.y1, s.y2), math.Max(s.y1, s.y2)
				if hi > iy+eps && lo < ey-eps {
					t.Errorf("%s: line %v crosses the white square", m.Name, s)
				}
			}
		}
	}
}

func TestGridWidths(t *testing.T) {
	m, _ := ByName("a4-100")
	segs, _ := parseContent(t, m.PDF(), 297)
	ox, oy, outer := frame(m, 210)
	// A horizontal line at each whole mm of the top band: 1 mm lines thin, every fifth heavier.
	for k := 1; k <= int(bandMM); k++ {
		y := oy + float64(k)
		want := gridLineMM
		if k%5 == 0 {
			want = majorLineMM
		}
		if k == int(bandMM) {
			want = edgeLineMM
		}
		found := false
		for _, s := range segs {
			if approx(s.y1, y, 1e-3) && approx(s.y2, y, 1e-3) && approx(s.x1, ox, 1e-3) && approx(s.x2, ox+outer, 1e-3) {
				found = true
				if !approx(s.width, want, 1e-3) {
					t.Errorf("line %d mm from the edge is %.3f mm wide, want %.2f", k, s.width, want)
				}
			}
		}
		if !found {
			t.Errorf("no full-width grid line at %d mm from the edge", k)
		}
	}
}

func TestBlackOnly(t *testing.T) {
	for _, m := range All() {
		stream := string(m.PDF())
		for _, line := range strings.Split(stream, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			switch f[len(f)-1] {
			case "G", "g":
				if line != "0 G" && line != "0 g" {
					t.Errorf("%s: colour operator %q: the mat must be black (grey 0)", m.Name, line)
				}
			case "RG", "rg", "K", "k", "SC", "sc", "SCN", "scn":
				t.Errorf("%s: colour operator %q: the mat must be black (grey 0)", m.Name, line)
			}
		}
	}
}

func TestDeterministic(t *testing.T) {
	for _, m := range All() {
		if !bytes.Equal(m.PDF(), m.PDF()) {
			t.Errorf("%s: two calls gave different bytes", m.Name)
		}
	}
}

func TestPDFInfo(t *testing.T) {
	tool, err := exec.LookPath("pdfinfo")
	if err != nil {
		t.Skip("pdfinfo (poppler) not installed")
	}
	dir := t.TempDir()
	want := map[string]string{
		"A4":     "595.276 x 841.89 pts (A4)",
		"Letter": "612 x 792 pts (letter)",
	}
	for _, m := range All() {
		path := filepath.Join(dir, m.Name+".pdf")
		if err := os.WriteFile(path, m.PDF(), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(tool, path).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: pdfinfo: %v\n%s", m.Name, err, out)
		}
		info := string(out)
		if !regexp.MustCompile(`Pages:\s+1\n`).MatchString(info) {
			t.Errorf("%s: pdfinfo does not report one page:\n%s", m.Name, info)
		}
		if !regexp.MustCompile(`Page size:\s+` + regexp.QuoteMeta(want[m.Paper])).MatchString(info) {
			t.Errorf("%s: pdfinfo page size is not %s:\n%s", m.Name, want[m.Paper], info)
		}
	}
}
