// Package measure turns a scan (or a photo) of a flat part into millimetre dimensions for
// OpenSCAD: the overall size, every step in the outline, and a circle fit for a rounded
// end.
//
// The intended workflow is a flatbed scanner: lay the part flat face down, scan at
// 600-1200 dpi, measure. The scan resolution is the scale, so there is no perspective and
// no parallax to correct. A phone photo on graph paper gets its scale from the grid (solid
// lines, or the dots between them), or from one known dimension. A camera's recorded dpi is
// a default, never a scale.
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

// Options control a measurement. The scale comes from the first of ScaleRefMM, GridMM and
// the dpi (explicit, else the file's, which a camera's does not count as) that applies; at
// least one has to resolve or Measure fails, because without a scale every dimension is
// meaningless.
type Options struct {
	DPI         float64 `json:"dpi"`          // scan resolution; 0 = the file's, if it is a scanner's
	GridMM      float64 `json:"grid_mm"`      // graph paper pitch, for photos
	ScaleRefMM  float64 `json:"scale_ref_mm"` // known overall length, to anchor the scale
	ThicknessMM float64 `json:"thickness_mm"` // height of the part above the paper, with CameraMM corrects parallax
	CameraMM    float64 `json:"camera_mm"`    // lens-to-paper distance
	Object      string  `json:"object"`       // auto (default), dark, light or split: how the part is found
	Threshold   int     `json:"threshold"`    // grey level that separates the part; 0 = automatic
	Crop        []int   `json:"crop"`         // [left, top, right, bottom] in pixels of the upright image; measure inside it
	Invert      bool    `json:"invert"`       // the part is lighter than the background
	Channel     string  `json:"channel"`      // gray (a scan), or a / b / sat on a photo
	TolMM       float64 `json:"tol_mm"`       // width tolerance within one step
	MinStepMM   float64 `json:"min_step_mm"`  // shortest step to report
	Flip        bool    `json:"flip"`         // force the other end to x = 0
	Overlay     bool    `json:"overlay"`      // also draw the annotated check image
	OverlayPx   int     `json:"overlay_px"`   // long edge of that image
}

func (o *Options) defaults() {
	if o.Object == "" {
		o.Object = ObjectAuto
	}
	if len(o.Crop) == 0 {
		o.Crop = nil
	}
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

// check reports an option that cannot be used on an image of size w x h.
func (o Options) check(w, h int) error {
	if !validObject(o.Object) {
		return fmt.Errorf("object %q: use auto, dark, light or split", o.Object)
	}
	if o.Threshold < 0 || o.Threshold > 255 {
		return fmt.Errorf("threshold %d: use a grey level from 1 to 255, or 0 for automatic", o.Threshold)
	}
	if o.Crop != nil {
		if len(o.Crop) != 4 {
			return fmt.Errorf("crop %v: give [left, top, right, bottom] in pixels", o.Crop)
		}
		l, t, r, b := o.Crop[0], o.Crop[1], o.Crop[2], o.Crop[3]
		if l < 0 || t < 0 || r > w || b > h || r-l < 16 || b-t < 16 {
			return fmt.Errorf("crop %v is outside the %dx%d image, or smaller than 16 px", o.Crop, w, h)
		}
	}
	return nil
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
	Object       string  `json:"object"`       // how the part was found: split, "split, inverted", dark or light
	Crop         []int   `json:"crop,omitempty"`

	// Mat is "white" or "black" when the photo was taken on a measuring mat; only its square,
	// MatBoxPx in full-image pixels, was measured.
	Mat      string `json:"mat,omitempty"`
	MatBoxPx []int  `json:"mat_box_px,omitempty"`

	// ObjectsFound counts the part-sized objects in the photo, the measured one included. The
	// others are not measured; OtherObjects says where they are, and a note says how to measure
	// one of them.
	ObjectsFound int      `json:"objects_found"`
	OtherObjects []Object `json:"other_objects,omitempty"`

	OverallLengthMM float64 `json:"overall_length_mm"`
	OverallWidthMM  float64 `json:"overall_width_mm"`
	Steps           []Step  `json:"steps"`

	// RoundEnd is set when a circle fits the leading tip to well under a tenth of a
	// millimetre. TipFitRMSMM is the fit error either way, so a near miss is visible.
	RoundEnd    *RoundEnd `json:"round_end,omitempty"`
	TipFitRMSMM float64   `json:"tip_fit_rms_mm"`

	// OpenSCAD is a variable block to paste at the top of main.scad.
	GridSpreadPct  float64 `json:"grid_spread_pct,omitempty"`    // how much the grid pitch varies across the photo, in per cent
	GridPxPerMMMin float64 `json:"grid_px_per_mm_min,omitempty"` // the least pixels per millimetre across the photo
	GridPxPerMMMax float64 `json:"grid_px_per_mm_max,omitempty"` // the most
	// GridBlocks is how many patches of dotted paper agreed on the pitch. Under 3, the scale
	// rests on one or two readings that nothing could contradict; a note says so.
	GridBlocks int `json:"grid_blocks,omitempty"`

	// Centreline is set for a long thin part whose axis bends (a spring strip, a hook seen
	// edge-on): its spine as a polyline in mm, with the local thickness and end circles.
	Centreline *Centreline `json:"centreline,omitempty"`

	OpenSCAD string   `json:"openscad"`
	Notes    []string `json:"notes,omitempty"`

	// Overlay is the annotated check image (PNG), when Options.Overlay was set. It shows
	// what was actually measured and is the only way to catch a silhouette that picked up
	// a shadow or a caliper jaw.
	Overlay []byte `json:"-"`
}

// Measure measures the part in img, which is the image as a photo viewer shows it (EXIF
// orientation already applied). meta is what the file says about itself: its recorded
// resolution is a scale only when it is a scanner's, not a camera's.
func Measure(img *imaging.RGB, meta imaging.Meta, o Options) (*Result, error) {
	o.defaults()
	if err := o.check(img.W, img.H); err != nil {
		return nil, err
	}
	res := &Result{}
	work := img
	if o.Crop != nil {
		work = crop(img, o.Crop)
		res.Crop = append([]int(nil), o.Crop...)
		res.Notes = append(res.Notes, fmt.Sprintf("Measured inside the crop [%d, %d, %d, %d] of the %dx%d image.",
			o.Crop[0], o.Crop[1], o.Crop[2], o.Crop[3], img.W, img.H))
	}

	// Where the scale does not need the outline (a grid, or the file's dpi), it comes first,
	// so that segmentation can work in millimetres. The grid is read from the whole image,
	// because the paper is all over it even when the crop is not.
	sc, err := preScale(img, meta, o, res)
	if err != nil {
		return nil, err
	}
	ppm := 0.0
	if sc.mmPerPx > 0 {
		ppm = 1 / sc.mmPerPx
	}

	// org is where work starts in the full image: the crop's corner, or the mat square's.
	org := [2]int{}
	if o.Crop != nil {
		org = [2]int{o.Crop[0], o.Crop[1]}
	}

	p, auto := choosePlan(o, meta.Camera)

	// On a measuring mat only the square counts: the part is looked for inside it, and its
	// colour says whether the part is the dark or the light thing in it. An explicit crop wins.
	var mat *matSquare
	if o.Crop == nil && o.GridMM > 0 {
		mat = findMatSquare(img, ppm)
	}
	if mat != nil {
		work = mat.crop(img)
		org = [2]int{mat.box[0], mat.box[1]}
		res.MatBoxPx = append([]int(nil), mat.box...)
		res.Mat = "white"
		if mat.black {
			res.Mat = "black"
		}
		res.Notes = append(res.Notes, mat.note())
		if auto && !o.Invert {
			p = plan{light: mat.black}
		}
		if mat.empty(work) {
			return nil, fmt.Errorf("the measuring mat's %s square (box [%d, %d, %d, %d] of the photo) is empty: the part has to lie inside the square, which is all that is measured. Lay it there and take the photo again, or pass crop=[l,t,r,b] round the part to measure it where it is",
				res.Mat, mat.box[0], mat.box[1], mat.box[2], mat.box[3])
		}
	}

	outline, p, others, err := segmentAll(work, p, auto, ppm, o, res)
	if err != nil {
		if mat != nil {
			return nil, fmt.Errorf("inside the measuring mat's square (box [%d, %d, %d, %d]): %w", mat.box[0], mat.box[1], mat.box[2], mat.box[3], err)
		}
		return nil, err
	}
	if mat != nil && mat.touchesEdge(outline) {
		res.Notes = append(res.Notes, "The part reaches the edge of the mat's square: anything of it outside the square was not measured. Move it fully inside the square and take the photo again.")
	}
	res.Object = p.name()
	res.ObjectsFound = 1 + len(others)

	pts, angle, fr := toAxes(outline)
	spanPx := maxOf(pts.X)
	if spanPx <= 0 {
		return nil, errors.New("the part has no extent along its own axis; check the overlay")
	}
	res.RotationDeg = round2(angle)

	mmPx, how := sc.mmPerPx, sc.how
	if mmPx == 0 { // scale_ref_mm: the one scale that needs the outline
		mmPx = o.ScaleRefMM / spanPx
		how = fmt.Sprintf("anchored to %g mm", o.ScaleRefMM)
	}
	if sc.at != nil {
		// Graph paper's pitch changes across a photo, so the scale is the one where the part is,
		// not the photo's typical one. The outline's centre, in full-image pixels.
		var cx, cy float64
		for i := range outline.X {
			cx += outline.X[i]
			cy += outline.Y[i]
		}
		cx, cy = cx/float64(outline.len()), cy/float64(outline.len())
		cx, cy = cx+float64(org[0]), cy+float64(org[1])
		local := sc.at(cx, cy)
		if math.Abs(local/sc.mmPerPx-1) > 0.005 {
			res.Notes = append(res.Notes, fmt.Sprintf("The scale is taken where the part is, %.4g mm per pixel there, which is %+.1f%% from the photo's typical grid pitch.", local, 100*(local/sc.mmPerPx-1)))
		}
		mmPx = local
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
	if sc.grid {
		res.GridSpreadPct = round2(sc.spread.SpreadPct)
		res.GridPxPerMMMin = round2(sc.spread.PxPerMMMin)
		res.GridPxPerMMMax = round2(sc.spread.PxPerMMMax)
		res.GridBlocks = sc.spread.Blocks
		if sc.spread.SpreadPct > 2 {
			res.Notes = append(res.Notes, fmt.Sprintf("the photo has perspective: the scale varies by %.1f%% across it (%.2f to %.2f px per mm); the scale near the part was used. For better accuracy anchor with scale_ref_mm, or shoot from further away and straight down.", sc.spread.SpreadPct, sc.spread.PxPerMMMin, sc.spread.PxPerMMMax))
		}
	}
	if o.CameraMM <= 0 && (sc.grid || meta.Camera) {
		res.Notes = append(res.Notes, "Features standing above the paper read large in a photo: their top is nearer the lens than the paper, so it images bigger. Pass thickness_mm (the height of the measured feature above the paper) and camera_mm (lens to paper, e.g. 300) to correct for it; without them the numbers are the paper's scale.")
	}

	// The other objects are reported with their boxes in full-image pixels; the note says
	// which one was measured, so the person can take a crop round another.
	ox, oy := org[0], org[1]
	if len(others) > 0 {
		res.OtherObjects = otherObjects(others, ox, oy, res.ScaleMMPerPx)
		res.Notes = append(res.Notes, objectsNote(res.ObjectsFound, shiftBox(outlineBox(outline), ox, oy)))
	}

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
	// A long thin strip whose axis bends has no meaningful widths along one straight axis, so
	// it also gets a centreline: the polyline, the thickness along it and the knuckles at its
	// ends, which is what a model of a bent strip is built from.
	if cl, ok := centreline(fillOutline(outline, work.W, work.H), mmPx); ok {
		res.Centreline = cl
		res.Notes = append(res.Notes, fmt.Sprintf("The part is a bent strip (%.1f mm along its centreline, %.2f mm thick, bowing %.1f mm off its chord), so the steps along one straight axis mean little: use centreline (points_mm, thickness_mm, ends) instead. The centreline is drawn in green on the overlay.", cl.LengthMM, cl.MedianThicknessMM, cl.SagMM))
	}
	res.OpenSCAD = scadBlock(res)
	res.Notes = append(res.Notes,
		"Z is not measured: thickness, boss height, bore depth and wall thickness need a caliper.",
		"Features that are not in the silhouette (a raised boss, a recess, a blind hole) are not measured either.",
		"Check the overlay before trusting these numbers: a shadow or a caliper jaw merged into the outline is the usual way this goes wrong.")

	if o.Overlay {
		view := overlayView(outlineBox(outline), work.W, work.H)
		if view != nil {
			full := shiftBox(view, ox, oy)
			note := fmt.Sprintf("The overlay is cropped to the part, [%d, %d, %d, %d] of the %dx%d photo, so that it can be read; the rest of the photo is not drawn.",
				full[0], full[1], full[2], full[3], img.W, img.H)
			if len(others) > 0 {
				note += " The whole photo is in a corner inset, with the crop in orange and the other objects in blue."
			}
			res.Notes = append(res.Notes, note)
		}
		png, err := drawOverlay(work, outline, fr, res, o, view, others)
		if err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
		res.Overlay = png
	}
	return res, nil
}

// segmentAll finds the outline with plan p. An automatic choice whose outline reaches every
// edge of the image (the background, not the part) is tried the other way round once; the
// result says so when that is what was used.
//
// The other objects in the photo come back with the outline, for the result to report.
func segmentAll(img *imaging.RGB, p plan, auto bool, ppm float64, o Options, res *Result) (points, plan, []imaging.Blob, error) {
	outline, others, frames, err := silhouette(img, p, ppm, o)
	if err != nil {
		return points{}, p, nil, err
	}
	if frames > 0 {
		res.Notes = append(res.Notes, "A frame printed around the part (a hollow outline with the part inside it) was left out; the part inside it was measured.")
	}
	if !spansImage(outline, img.W, img.H) {
		return outline, p, others, nil
	}
	if auto {
		alt := p.other()
		if second, altOthers, _, err := silhouette(img, alt, ppm, o); err == nil && !spansImage(second, img.W, img.H) {
			res.Notes = append(res.Notes, fmt.Sprintf("The %s method reached every edge of the image, so the part was found with the %s method instead.", p.name(), alt.name()))
			return second, alt, altOthers, nil
		}
	}
	res.Notes = append(res.Notes, "The shape measured reaches every edge of the image, which usually means the background was picked instead of the part: try the invert option, another channel, object, or crop closer.")
	return outline, p, others, nil
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

// scaleSource is a scale that does not need the outline.
type scaleSource struct {
	mmPerPx float64                    // 0 when the scale needs the outline (scale_ref_mm)
	at      func(x, y float64) float64 // graph paper: the scale at a point, full-image pixels; nil otherwise
	how     string                     // what it came from, for ScaleFrom
	grid    bool                       // it came from graph paper
	spread  gridScale                  // the grid's spread and range, when it came from graph paper
}

// preScale works out the scale from the grid or from the dpi, or defers to scale_ref_mm.
func preScale(img *imaging.RGB, meta imaging.Meta, o Options, res *Result) (scaleSource, error) {
	switch {
	case o.ScaleRefMM > 0:
		return scaleSource{}, nil
	case o.GridMM > 0:
		gray, err := imaging.Extract(img, imaging.ChannelGray)
		if err != nil {
			return scaleSource{}, err
		}
		gs, err := scaleFromGrid(gray, o.GridMM)
		if err != nil {
			return scaleSource{}, err
		}
		res.Notes = append(res.Notes, gs.Note)
		return scaleSource{mmPerPx: gs.MMPerPx, at: gs.MMPerPxAt, how: fmt.Sprintf("%g mm grid, %s", o.GridMM, gs.Method), grid: true, spread: gs}, nil
	}
	dpi := o.DPI
	if dpi <= 0 {
		if meta.Camera {
			if meta.DPI > 1 {
				return scaleSource{}, fmt.Errorf("this is a camera photo; its %g dpi is a default, not a measurement: pass grid_mm, scale_ref_mm or dpi", meta.DPI)
			}
			return scaleSource{}, errors.New("this is a camera photo, which records no scale: pass grid_mm, scale_ref_mm or dpi")
		}
		dpi = meta.DPI
	}
	if dpi <= 1 {
		return scaleSource{}, errors.New("no scale: the image does not record a dpi, so pass dpi, grid_mm or scale_ref_mm")
	}
	return scaleSource{mmPerPx: 25.4 / dpi, how: fmt.Sprintf("%g dpi", dpi)}, nil
}

// crop copies the rectangle [l, t, r, b) out of img.
func crop(img *imaging.RGB, c []int) *imaging.RGB {
	l, t, r, b := c[0], c[1], c[2], c[3]
	w, h := r-l, b-t
	out := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for y := range h {
		src := ((t+y)*img.W + l) * 3
		copy(out.Pix[y*w*3:(y+1)*w*3], img.Pix[src:src+w*3])
	}
	return out
}

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
	if r.Centreline != nil {
		b.WriteString(r.Centreline.OpenSCAD)
		if !strings.HasSuffix(r.Centreline.OpenSCAD, "\n") {
			b.WriteString("\n")
		}
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
