// Package measure turns a scan (or a photo) of a flat part into millimetre dimensions for
// OpenSCAD: the overall size, every step in the outline, and a circle fit for a rounded
// end.
//
// The intended workflow is a flatbed scanner: lay the part flat face down, scan at
// 600-1200 dpi, measure. The scan resolution is the scale, so there is no perspective and
// no parallax to correct. For a phone photo on 1 mm graph paper the scale can instead be
// recovered from the grid, or anchored to one known dimension.
//
// Nothing here measures Z. Thickness, boss height, bore depth and wall thickness still
// need a caliper.
//
// This is a port of an earlier Python tool (measure_part.py) that used OpenCV and numpy; the
// image processing it needed is in internal/imaging.
package measure

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// Defaults for Options.
const (
	DefaultTolMM     = 0.08 // width spread allowed inside one step
	DefaultMinStepMM = 0.5  // shortest step reported
	DefaultOverlayPx = 1200 // long edge of the overlay image
)

// Options control a measurement. The three scale sources are tried in the order
// ScaleRefMM, GridMM, DPI; at least one has to resolve or Measure fails, because without a
// scale every dimension is meaningless.
type Options struct {
	DPI         float64 `json:"dpi"`          // scan resolution; 0 = read it from the file
	GridMM      float64 `json:"grid_mm"`      // graph paper pitch, for photos
	ScaleRefMM  float64 `json:"scale_ref_mm"` // known overall length, to anchor the scale
	ThicknessMM float64 `json:"thickness_mm"` // with CameraMM, corrects photo parallax
	CameraMM    float64 `json:"camera_mm"`    // lens-to-paper distance
	Invert      bool    `json:"invert"`       // the part is lighter than the background
	Channel     string  `json:"channel"`      // gray (a scan), or a / b / sat on a photo
	TolMM       float64 `json:"tol_mm"`       // width tolerance within one step
	MinStepMM   float64 `json:"min_step_mm"`  // shortest step to report
	Flip        bool    `json:"flip"`         // force the other end to x = 0
	Overlay     bool    `json:"overlay"`      // also draw the annotated check image
	OverlayPx   int     `json:"overlay_px"`   // long edge of that image
}

func (o *Options) defaults() {
	if o.TolMM <= 0 {
		o.TolMM = DefaultTolMM
	}
	if o.MinStepMM <= 0 {
		o.MinStepMM = DefaultMinStepMM
	}
	if o.Channel == "" {
		o.Channel = imaging.ChannelGray
	}
	if o.OverlayPx <= 0 {
		o.OverlayPx = DefaultOverlayPx
	}
	o.OverlayPx = min(o.OverlayPx, 3000)
}

// Step is one run of constant width along the part.
type Step struct {
	XStartMM float64 `json:"x_start_mm"`
	XEndMM   float64 `json:"x_end_mm"`
	LengthMM float64 `json:"length_mm"`
	WidthMM  float64 `json:"width_mm"`
	Centred  bool    `json:"centred"` // the step is centred on the part's axis
}

// RoundEnd is a circle fitted to the leading tip, when one fits well.
type RoundEnd struct {
	RadiusMM   float64 `json:"radius_mm"`
	DiameterMM float64 `json:"diameter_mm"`
	CentreXMM  float64 `json:"centre_x_mm"` // where the straight flank ends
	FitRMSMM   float64 `json:"fit_rms_mm"`
}

// Result is a finished measurement. Everything is in millimetres, along the part's own
// axes: x runs the length from the leading end, y across the width.
type Result struct {
	ScaleMMPerPx float64 `json:"scale_mm_per_px"`
	ScaleFrom    string  `json:"scale_from"`
	RotationDeg  float64 `json:"rotation_deg"` // how the part lay in the image
	Flipped      bool    `json:"flipped"`      // the far end was taken as x = 0

	OverallLengthMM float64 `json:"overall_length_mm"`
	OverallWidthMM  float64 `json:"overall_width_mm"`
	Steps           []Step  `json:"steps"`

	// RoundEnd is set when a circle fits the leading tip to well under a tenth of a
	// millimetre. TipFitRMSMM is the fit error either way, so a near miss is visible.
	RoundEnd    *RoundEnd `json:"round_end,omitempty"`
	TipFitRMSMM float64   `json:"tip_fit_rms_mm"`

	// OpenSCAD is a variable block to paste at the top of main.scad.
	OpenSCAD string   `json:"openscad"`
	Notes    []string `json:"notes,omitempty"`

	// Overlay is the annotated check image (PNG), when Options.Overlay was set. It shows
	// what was actually measured and is the only way to catch a silhouette that picked up
	// a shadow or a caliper jaw.
	Overlay []byte `json:"-"`
}

// Measure measures the part in img. fileDPI is the resolution stored in the image file, or
// 0; it is used when Options gives no scale of its own.
func Measure(img *imaging.RGB, fileDPI float64, o Options) (*Result, error) {
	o.defaults()

	outline, err := silhouette(img, o)
	if err != nil {
		return nil, err
	}
	pts, angle, fr := toAxes(outline)
	spanPx := maxOf(pts.X)
	if spanPx <= 0 {
		return nil, errors.New("the part has no extent along its own axis; check the overlay")
	}

	res := &Result{RotationDeg: round2(angle)}
	if spansImage(outline, img.W, img.H) {
		res.Notes = append(res.Notes, "The shape measured reaches every edge of the image, which usually means the background was picked instead of the part: try the invert option, another channel, or crop closer.")
	}
	mmPx, how, err := scale(img, spanPx, fileDPI, o, res)
	if err != nil {
		return nil, err
	}

	// A photo images the top face of the part larger than its footprint, because the face
	// is closer to the lens; a scan does not.
	if o.ThicknessMM > 0 && o.CameraMM > 0 {
		k := (o.CameraMM - o.ThicknessMM) / o.CameraMM
		if k <= 0 {
			return nil, fmt.Errorf("thickness %g mm is not less than the camera distance %g mm", o.ThicknessMM, o.CameraMM)
		}
		mmPx *= k
		how += fmt.Sprintf(", parallax -%.2f%%", (1-k)*100)
	}
	res.ScaleMMPerPx, res.ScaleFrom = round6(mmPx), how

	// Which end leads? The rounder one, unless told otherwise. A half-round tip is an arc
	// of about half the local width, so the points within one width of each tip are the
	// candidates for a circle fit, and the better fit wins.
	bins := max(200, int(spanPx/2))
	xs, lo, hi := widthProfile(pts, bins)
	wmm := scaled(sub(hi, lo), mmPx)
	edge := max(3, len(wmm)/20)
	near := fitTip(pts, math.Max(median(wmm[:edge]), 1.0)/mmPx, false)
	far := fitTip(pts, math.Max(median(wmm[len(wmm)-edge:]), 1.0)/mmPx, true)

	tip := near
	if res.Flipped = o.Flip || far.RMS < near.RMS; res.Flipped {
		fr.flipped = true
		for i := range pts.X {
			pts.X[i] = spanPx - pts.X[i]
		}
		xs, lo, hi = widthProfile(pts, bins)
		wmm = scaled(sub(hi, lo), mmPx)
		tip = far
	}

	res.OverallLengthMM = round3(spanPx * mmPx)
	res.OverallWidthMM = round3(maxOf(wmm))
	res.TipFitRMSMM = round4(tip.RMS * mmPx)
	res.Steps = findSteps(scaled(xs, mmPx), wmm, scaled(mid(hi, lo), mmPx), o.TolMM, o.MinStepMM)

	// A tip counts as round when a circle fits it to well under a tenth of a millimetre.
	if tip.RMS*mmPx < 0.05 && !math.IsInf(tip.R, 0) && !math.IsNaN(tip.R) {
		res.RoundEnd = &RoundEnd{
			RadiusMM:   round3(tip.R * mmPx),
			DiameterMM: round3(2 * tip.R * mmPx),
			CentreXMM:  round3(tip.CX * mmPx),
			FitRMSMM:   round4(tip.RMS * mmPx),
		}
	}
	res.OpenSCAD = scadBlock(res)
	res.Notes = append(res.Notes,
		"Z is not measured: thickness, boss height, bore depth and wall thickness need a caliper.",
		"Features that are not in the silhouette (a raised boss, a recess, a blind hole) are not measured either.",
		"Check the overlay before trusting these numbers: a shadow or a caliper jaw merged into the outline is the usual way this goes wrong.")

	if o.Overlay {
		png, err := drawOverlay(img, outline, fr, res, o)
		if err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
		res.Overlay = png
	}
	return res, nil
}

// silhouette thresholds the image and returns the outline of the part, in image pixels.
func silhouette(img *imaging.RGB, o Options) (points, error) {
	ch, err := imaging.Extract(img, o.Channel)
	if err != nil {
		return points{}, err
	}
	blur := imaging.Blur5(ch)
	mask := imaging.Threshold(blur, imaging.Otsu(blur), o.Invert)
	se := imaging.EllipseSE(7)
	mask = imaging.Close(mask, se) // bridge the graph-paper lines crossing the part
	mask = imaging.Open(mask, se)  // drop speckle

	blobs := imaging.Blobs(mask)
	if len(blobs) == 0 {
		return points{}, errors.New("no part found: try invert, another channel, or crop the image closer")
	}
	b := blobs[0]
	if float64(b.Area) < 0.001*float64(len(mask.Pix)) {
		return points{}, fmt.Errorf("the largest blob covers only %.3f%% of the image: try invert, another channel, or crop the image closer",
			100*float64(b.Area)/float64(len(mask.Pix)))
	}
	if len(b.Outline) < 12 {
		return points{}, errors.New("the part outline is too small to measure")
	}
	p := points{X: make([]float64, len(b.Outline)), Y: make([]float64, len(b.Outline))}
	for i, q := range b.Outline {
		p.X[i], p.Y[i] = float64(q.X), float64(q.Y)
	}
	return p, nil
}

// spansImage reports whether an outline touches all four edges of the image. The part
// should sit inside the frame, so a shape that does not is almost always the background,
// selected because invert was set the wrong way round.
func spansImage(p points, w, h int) bool {
	var l, r, t, b bool
	for i := range p.X {
		x, y := p.X[i], p.Y[i]
		l, r = l || x <= 0, r || x >= float64(w-1)
		t, b = t || y <= 0, b || y >= float64(h-1)
	}
	return l && r && t && b
}

// scale works out millimetres per pixel.
func scale(img *imaging.RGB, spanPx, fileDPI float64, o Options, res *Result) (float64, string, error) {
	switch {
	case o.ScaleRefMM > 0:
		return o.ScaleRefMM / spanPx, fmt.Sprintf("anchored to %g mm", o.ScaleRefMM), nil
	case o.GridMM > 0:
		gray, err := imaging.Extract(img, imaging.ChannelGray)
		if err != nil {
			return 0, "", err
		}
		mmPx, note, err := scaleFromGrid(gray, o.GridMM)
		if err != nil {
			return 0, "", err
		}
		res.Notes = append(res.Notes, note)
		return mmPx, fmt.Sprintf("%g mm grid (Hough)", o.GridMM), nil
	}
	dpi := o.DPI
	if dpi <= 0 {
		dpi = fileDPI
	}
	if dpi <= 1 {
		return 0, "", errors.New("no scale: the image does not record a dpi, so pass dpi, grid_mm or scale_ref_mm")
	}
	return 25.4 / dpi, fmt.Sprintf("%g dpi", dpi), nil
}

// scaleFromGrid recovers millimetres per pixel from graph paper: a Hough transform for the
// paper's angle, the spectrum of the edge profile for its pitch. Only needed for photos; a
// scan has its dpi.
func scaleFromGrid(gray *imaging.Gray, gridMM float64) (float64, string, error) {
	edges := imaging.Canny(gray, 40, 120)
	ang, err := gridAngle(edges)
	if err != nil {
		return 0, "", err
	}
	rot := imaging.RotateNearest(edges, ang)

	var pitches []float64
	for _, p := range []float64{pitchByFFT(imaging.ColSums(rot), 4), pitchByFFT(imaging.RowSums(rot), 4)} {
		if p > 0 {
			pitches = append(pitches, p)
		}
	}
	if len(pitches) == 0 {
		return 0, "", errors.New("grid: could not find a repeating pitch; pass dpi or scale_ref_mm instead")
	}
	// Square paper, so the two axes have to agree. If they do not, the scale is a guess
	// and every dimension would be wrong by that one factor, so refuse rather than return
	// a plausible-looking number.
	if len(pitches) == 2 && math.Abs(pitches[0]-pitches[1]) > 0.05*math.Max(pitches[0], pitches[1]) {
		return 0, "", fmt.Errorf("grid: the two axes disagree (%.1f px vs %.1f px), so the scale would be unreliable; pass dpi or scale_ref_mm instead",
			pitches[0], pitches[1])
	}
	pitch := median(pitches)
	return gridMM / pitch, fmt.Sprintf("grid: %+.2f deg, %.2f px per %g mm cell", ang, pitch, gridMM), nil
}

// gridAngle is the rotation of the graph paper in degrees, from a Hough transform.
//
// A square grid has two line families 90 degrees apart, so the angle is only defined
// modulo 90. Multiplying by 4 maps that onto a full circle, where the peak can be found
// without a wraparound seam: folding onto [0, 90) instead would split an axis-aligned grid
// between 0 and 89 and let a spurious direction win.
//
// Only the angle comes from here. The pitch does not, because Canny finds two edges per
// printed line and the line-to-line spacing then collapses to the width of a single line.
func gridAngle(edges *imaging.Gray) (float64, error) {
	// A grid line runs the width of the image, so it earns votes in proportion to the
	// image size. A low fixed threshold instead returns tens of thousands of lines, and
	// the diagonals through the lattice intersections drown out the real grid.
	threshold := int(0.25 * float64(max(edges.W, edges.H)))
	lines := imaging.HoughLines(edges, 1, math.Pi/720, threshold, 300)
	if len(lines) < 8 {
		return 0, fmt.Errorf("grid: only %d strong lines found, need 8; pass dpi or scale_ref_mm instead", len(lines))
	}

	quad := make([]float64, len(lines))
	for i, l := range lines {
		quad[i] = math.Mod(4*l.Theta, 2*math.Pi) // 90 degrees -> a full circle
	}
	const bins = 360
	var hist [bins]int
	for _, q := range quad {
		b := min(int(q/(2*math.Pi)*bins), bins-1)
		hist[b]++
	}
	peak := 0
	for i, c := range hist {
		if c > hist[peak] {
			peak = i
		}
	}
	centre := (float64(peak) + 0.5) * 2 * math.Pi / bins

	// circular mean of the lines within 8 degrees of the peak
	var sre, sim float64
	n := 0
	for _, q := range quad {
		if math.Abs(wrapPi(q-centre)) < 8*math.Pi/180 {
			sre += math.Cos(q)
			sim += math.Sin(q)
			n++
		}
	}
	if n == 0 {
		for _, q := range quad {
			sre += math.Cos(q)
			sim += math.Sin(q)
		}
	}
	return math.Atan2(sim, sre) / 4 * 180 / math.Pi, nil
}

// wrapPi folds an angle onto (-pi, pi].
func wrapPi(a float64) float64 { return math.Atan2(math.Sin(a), math.Cos(a)) }

// widthProfile measures the width of the outline in each of bins columns and returns the
// column positions with the lower and upper edge in each, skipping empty columns.
func widthProfile(p points, bins int) (xs, lo, hi []float64) {
	maxX := maxOf(p.X)
	blo := make([]float64, bins)
	bhi := make([]float64, bins)
	seen := make([]bool, bins)
	for i := range p.X {
		b := min(max(int(p.X[i]/maxX*float64(bins-1)), 0), bins-1)
		y := p.Y[i]
		if !seen[b] {
			blo[b], bhi[b], seen[b] = y, y, true
			continue
		}
		blo[b], bhi[b] = math.Min(blo[b], y), math.Max(bhi[b], y)
	}
	for b := range bins {
		if !seen[b] {
			continue
		}
		xs = append(xs, maxX*float64(b)/float64(bins-1))
		lo = append(lo, blo[b])
		hi = append(hi, bhi[b])
	}
	return xs, lo, hi
}

// fitTip fits a circle to one tip of the part. width is the part's width just inside that
// tip, in pixels; the tip of a half-round end is an arc of about width/2, so the points
// within one width of the tip are the candidates.
func fitTip(p points, width float64, far bool) circle {
	maxX := maxOf(p.X)
	sel := points{}
	for i := range p.X {
		x := p.X[i]
		if far {
			x = maxX - x
		}
		if x < width {
			sel.X = append(sel.X, p.X[i])
			sel.Y = append(sel.Y, p.Y[i])
		}
	}
	if sel.len() < 12 {
		return circle{R: math.NaN(), RMS: math.Inf(1), CX: math.NaN()}
	}
	c := fitCircle(sel)
	if far {
		c.CX = maxX - c.CX
	}
	return c
}

// findSteps splits the width profile into runs of constant width.
//
// The columns where the width changes belong to neither neighbour, so each boundary is
// placed at the midpoint of the transition. That way the steps tile the part and their
// lengths add up to the overall length, instead of each coming up short.
func findSteps(xs, w, cx []float64, tolMM, minLenMM float64) []Step {
	if len(w) == 0 {
		return []Step{}
	}
	type run struct{ a, b int }
	var runs []run
	start := 0
	for i := 1; i <= len(w); i++ {
		if i < len(w) {
			seg := w[start:i]
			if maxOf(seg)-minOf(seg) <= tolMM && math.Abs(w[i]-median(seg)) <= tolMM {
				continue
			}
		}
		runs = append(runs, run{start, i - 1})
		start = i
	}

	var keep []run
	for _, r := range runs {
		if xs[r.b]-xs[r.a] >= minLenMM && r.b-r.a > 2 {
			keep = append(keep, r)
		}
	}
	if len(keep) == 0 {
		return []Step{}
	}

	// Resampling the rotated outline jitters the width by a fraction of a pixel, which can
	// break one long step into several. Neighbours of the same width are one step.
	merged := []run{keep[0]}
	for _, r := range keep[1:] {
		p := merged[len(merged)-1]
		if math.Abs(median(w[r.a:r.b+1])-median(w[p.a:p.b+1])) <= tolMM {
			merged[len(merged)-1] = run{p.a, r.b}
		} else {
			merged = append(merged, r)
		}
	}

	steps := make([]Step, 0, len(merged))
	for n, r := range merged {
		x0, x1 := xs[0], xs[len(xs)-1]
		if n > 0 {
			x0 = (xs[merged[n-1].b] + xs[r.a]) / 2
		}
		if n < len(merged)-1 {
			x1 = (xs[r.b] + xs[merged[n+1].a]) / 2
		}
		steps = append(steps, Step{
			XStartMM: round3(x0),
			XEndMM:   round3(x1),
			LengthMM: round3(x1 - x0),
			WidthMM:  round3(median(w[r.a : r.b+1])),
			Centred:  math.Abs(median(cx[r.a:r.b+1])) < tolMM,
		})
	}
	return steps
}

// scadBlock is a variable block to paste at the top of main.scad.
func scadBlock(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// measured: %.2f x %.2f mm overall (%s)\n", r.OverallLengthMM, r.OverallWidthMM, r.ScaleFrom)
	for i, s := range r.Steps {
		fmt.Fprintf(&b, "seg%d_length = %.2f;  seg%d_width = %.2f;\n", i+1, s.LengthMM, i+1, s.WidthMM)
	}
	if r.RoundEnd != nil {
		fmt.Fprintf(&b, "tip_radius = %.2f;  // arc centre at x = %.2f\n", r.RoundEnd.RadiusMM, r.RoundEnd.CentreXMM)
	}
	b.WriteString("thickness = 0;  // Z is not measured: use a caliper\n")
	return b.String()
}

// ---------- small slice helpers ----------

func maxOf(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		m = math.Max(m, x)
	}
	return m
}

func minOf(v []float64) float64 {
	m := math.Inf(1)
	for _, x := range v {
		m = math.Min(m, x)
	}
	return m
}

func sub(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out
}

func mid(a, b []float64) []float64 {
	out := make([]float64, len(a))
	for i := range a {
		out[i] = (a[i] + b[i]) / 2
	}
	return out
}

func scaled(v []float64, k float64) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		out[i] = v[i] * k
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
