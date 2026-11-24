// Package mat makes the measuring mats: printable sheets a part is laid on before it is
// photographed for measure_part. A mat is a white square with a 1 mm grid around it, so
// the photo has a scale in the picture itself. The black variant fills the square with solid
// black (inside a thin white gap), for a white or light part that would not stand out on paper.
//
// The PDF is written by hand. It is PDF 1.4 with one page, one content stream of path and
// text operators, and the standard Helvetica fonts, which every PDF reader has, so nothing
// is embedded and no library is needed. Nothing in the output depends on the time or the
// machine, so the same Mat always gives the same bytes and the server can cache or ETag it.
package mat

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Mat is one variant of the mat.
type Mat struct {
	Name    string  // file stem and URL key, e.g. "a4-100"
	Title   string  // printed on the sheet, e.g. "Measuring mat, 100 mm"
	Paper   string  // "A4" or "Letter"
	InnerMM float64 // side of the square inside the grid in mm
	Black   bool    // the square is filled black, for a light part
}

// paperSizes are in mm, portrait.
var paperSizes = map[string][2]float64{
	"A4":     {210, 297},
	"Letter": {215.9, 279.4},
}

// All lists the variants: A4 and US Letter, each with a 100 mm and a 150 mm square, and each
// of those white (for a dark part) and black (for a light part).
func All() []Mat {
	var out []Mat
	for _, paper := range []string{"A4", "Letter"} {
		for _, inner := range []float64{100, 150} {
			for _, black := range []bool{false, true} {
				out = append(out, newMat(paper, inner, black))
			}
		}
	}
	return out
}

// ByName finds a variant such as "a4-100", "letter-150" or "a4-100-black".
func ByName(name string) (Mat, bool) {
	for _, m := range All() {
		if m.Name == name {
			return m, true
		}
	}
	return Mat{}, false
}

func newMat(paper string, inner float64, black bool) Mat {
	m := Mat{
		Name:    strings.ToLower(paper) + "-" + strconv.Itoa(int(inner)),
		Title:   fmt.Sprintf("Measuring mat, %g mm", inner),
		Paper:   paper,
		InnerMM: inner,
		Black:   black,
	}
	if black {
		m.Name += "-black"
		m.Title += ", black"
	}
	return m
}

// Layout in mm. The frame's top edge is at frameTopMM from the top of the page, which
// leaves room for the orientation mark above the corner and keeps everything at least
// 10 mm from the edges on both papers.
const (
	bandMM      = 10.0 // width of the grid band around the white square
	frameTopMM  = 22.0 // top edge of the outer square, from the top of the page
	markLegMM   = 6.0  // legs of the orientation triangle
	markGapMM   = 2.0  // gap between the triangle and the corner it marks
	marginMM    = 15.0 // left edge of the text
	checkMM     = 50.0 // length of the check bar
	footerMM    = 11.0 // footer baseline, from the bottom of the page
	textWrapLen = 90   // characters per line of the instructions

	gridLineMM  = 0.12 // every 1 mm
	majorLineMM = 0.2  // every 5 mm, and the check bar and its ticks
	edgeLineMM  = 0.4  // the two edges of the band

	// BlackGapMM is the white gap between the inner edge of the band and the black fill of a
	// black mat. It keeps the grid band a hollow frame of its own, apart from the black square,
	// which is how measure_part finds the square on either kind of mat.
	BlackGapMM = 2.0
)

// PDF returns the mat as a one-page PDF. A Mat with an unknown paper gives nil.
func (m Mat) PDF() []byte {
	size, ok := paperSizes[m.Paper]
	if !ok {
		return nil
	}
	w, h := size[0], size[1]
	content := m.content(w, h)

	// Object 1 to 7; the page's contents are object 4 and Info is object 7.
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Contents 4 0 R /Resources << /Font << /F1 5 0 R /F2 6 0 R >> >> >>",
			num(w*ptPerMM), num(h*ptPerMM)),
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>",
		fmt.Sprintf("<< /Title (%s) /Producer (tinkerbench openscad workbench) >>", pdfString(m.Title)),
	}

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects))
	for i, body := range objects {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	// Each xref entry is exactly 20 bytes; the offsets are where "N 0 obj" starts.
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 7 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

// content draws the page. Coordinates are mm from the top-left corner, and the stream
// converts them to PDF points with y up.
func (m Mat) content(w, h float64) string {
	s := &stream{h: h}
	// Pure black (grey 0) for every stroke and fill. A printer turns a grey line into a
	// halftone of dots, which is why the grid is not grey.
	s.b.WriteString("0 G\n0 g\n0 J\n")

	outer := m.InnerMM + 2*bandMM
	ox := (w - outer) / 2
	oy := frameTopMM
	grid(s, ox, oy, m.InnerMM)
	if m.Black {
		g := bandMM + BlackGapMM
		s.rect(ox+g, oy+g, m.InnerMM-2*BlackGapMM, m.InnerMM-2*BlackGapMM)
	}

	// The orientation mark is a right triangle outside the top-left corner, off the grid,
	// so the frame's way up is never in doubt in a photo.
	c := ox - markGapMM
	d := oy - markGapMM
	s.fill(c, d, c, d-markLegMM, c-markLegMM, d)

	// Check bar, centred under the frame. A caliper on the printed bar shows whether the
	// printer scaled the sheet.
	yb := oy + outer + 14
	x0 := (w - checkMM) / 2
	s.line(x0, yb, x0+checkMM, yb, majorLineMM)
	for k := 0; k <= int(checkMM/10); k++ {
		x := x0 + float64(k)*10
		s.line(x, yb, x, yb+4, majorLineMM)
	}
	s.textCentred(x0, yb+8, 8, false, "0")
	s.textCentred(x0+checkMM, yb+8, 8, false, "50 mm")

	s.text(marginMM, yb+16, 12, true, m.Title)
	y := yb + 24
	paras := []string{
		fmt.Sprintf("Print at 100%% / Actual size (turn off Fit to page). Check with a caliper: the white square must be %.1f mm across; if not, tell the agent the real size.", m.InnerMM),
		"Lay the part flat in the white square, all of it inside the square: only what is in the square is measured. Shoot from straight above, from far enough that the whole frame is in the picture, in even light.",
		"Upload the photo to the project and measure with grid_mm = 1.",
	}
	if m.Black {
		paras[0] = fmt.Sprintf("Print at 100%% / Actual size (turn off Fit to page). Check with a caliper: the square inside the grid, edge line to edge line, must be %.1f mm across; if not, tell the agent the real size.", m.InnerMM)
		paras[1] = "For a white or light part. Lay it flat on the black, all of it inside: only what is in the square is measured. Shoot from straight above, from far enough that the whole frame is in the picture, in even light without flash (a glossy black reflects it)."
	}
	for i, p := range paras {
		if i > 0 {
			y += 2
		}
		for _, line := range wrap(p, textWrapLen) {
			s.text(marginMM, y, 9, false, line)
			y += 5
		}
	}

	s.text(marginMM, h-footerMM, 7, false, "tinkerbench openscad workbench")
	return s.b.String()
}

// grid draws the band of 1 mm grid around the white square at (ox, oy) with side inner.
// A line sits at every whole mm counted from the outer edge, so the lines meet the frame's
// edges and run through the corners. Across the white square itself the lines are cut off
// at its edges, so it has no marks.
func grid(s *stream, ox, oy, inner float64) {
	const band = int(bandMM)
	ki := int(inner)
	n := band + ki + band // outer side in mm
	outer := float64(n)
	ix, iy := ox+bandMM, oy+bandMM
	ex, ey := ix+inner, iy+inner

	width := func(k int) float64 {
		switch {
		case k == 0 || k == n || k == band || k == band+ki:
			return edgeLineMM
		case k%5 == 0:
			return majorLineMM
		default:
			return gridLineMM
		}
	}
	for k := 0; k <= n; k++ {
		x := ox + float64(k)
		w := width(k)
		if k > band && k < band+ki {
			// Inside the white square's x range: only the top and bottom bands get the line.
			s.line(x, oy, x, iy, w)
			s.line(x, ey, x, oy+outer, w)
		} else {
			s.line(x, oy, x, oy+outer, w)
		}
	}
	for k := 0; k <= n; k++ {
		y := oy + float64(k)
		w := width(k)
		if k > band && k < band+ki {
			s.line(ox, y, ix, y, w)
			s.line(ex, y, ox+outer, y, w)
		} else {
			s.line(ox, y, ox+outer, y, w)
		}
	}
}

// stream builds the content stream. It remembers the line width it last set so that the
// width operator is only written when it changes.
type stream struct {
	b   strings.Builder
	h   float64 // page height in mm
	wmm float64 // line width last set, in mm
}

// xy converts a point in mm from the top-left to PDF points with y up.
func (s *stream) xy(x, y float64) string {
	return num(x*ptPerMM) + " " + num((s.h-y)*ptPerMM)
}

// line strokes a straight segment; both ends are in mm.
func (s *stream) line(x1, y1, x2, y2, widthMM float64) {
	if widthMM != s.wmm {
		fmt.Fprintf(&s.b, "%s w\n", num(widthMM*ptPerMM))
		s.wmm = widthMM
	}
	fmt.Fprintf(&s.b, "%s m %s l S\n", s.xy(x1, y1), s.xy(x2, y2))
}

// fill paints a closed triangle with the current fill colour.
func (s *stream) fill(x1, y1, x2, y2, x3, y3 float64) {
	fmt.Fprintf(&s.b, "%s m %s l %s l h f\n", s.xy(x1, y1), s.xy(x2, y2), s.xy(x3, y3))
}

// rect fills a rectangle with the current fill colour; the corner and size are in mm.
func (s *stream) rect(x, y, w, h float64) {
	fmt.Fprintf(&s.b, "%s %s %s re f\n", s.xy(x, y+h), num(w*ptPerMM), num(h*ptPerMM))
}

// text sets a line of text with its baseline at (x, y) in mm.
func (s *stream) text(x, y, size float64, bold bool, str string) {
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(&s.b, "BT /%s %s Tf %s Td (%s) Tj ET\n", font, num(size), s.xy(x, y), pdfString(str))
}

// textCentred centres a short label on x. The width is estimated (Helvetica is
// proportional, but for a few digits and "mm" the estimate is within a fraction of a mm).
func (s *stream) textCentred(x, y, size float64, bold bool, str string) {
	wmm := float64(len(str)) * 0.55 * size / ptPerMM
	s.text(x-wmm/2, y, size, bold, str)
}

// wrap breaks s at spaces into lines of at most n characters, unless a single word is longer.
func wrap(s string, n int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case len(cur)+1+len(word) > n:
			lines = append(lines, cur)
			cur = word
		default:
			cur += " " + word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

const ptPerMM = 72 / 25.4

// num formats a number for the PDF with at most three decimals and no trailing zeros.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// pdfString escapes a string for a PDF literal string. The text here is plain ASCII.
func pdfString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}
