package measure

import (
	"fmt"
	"math"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// Overlay colours.
var (
	colOutline = imaging.Color{R: 230, G: 40, B: 40}   // the silhouette that was measured
	colStep    = imaging.Color{R: 40, G: 190, B: 230}  // boundaries between steps
	colTip     = imaging.Color{R: 255, G: 165, B: 20}  // the circle fitted to the round end
	colSpine   = imaging.Color{R: 60, G: 220, B: 90}   // the centreline of a bent strip
	colOther   = imaging.Color{R: 30, G: 90, B: 255}   // other objects in the photo, not measured
	colLabel   = imaging.Color{R: 255, G: 255, B: 255} // step numbers and the scale bar
	colShadow  = imaging.Color{R: 20, G: 20, B: 20}
)

// overlayMarginPx is the least margin kept round the part in a zoomed overlay, so the edge of
// the outline is not on the edge of the picture.
const overlayMarginPx = 24

// overlayView is the part of the photo the overlay draws: the measured part's box with a margin
// of a quarter of its larger side (at least overlayMarginPx), clamped to the image. The box is
// [left, top, right, bottom) in pixels of the image. It returns nil, which means the whole photo,
// when that view is more than half of the photo's longer side: a part that fills the frame gains
// nothing from a zoom.
func overlayView(box []int, w, h int) []int {
	l, t, r, b := box[0], box[1], box[2], box[3]
	m := max((max(r-l, b-t))/4, overlayMarginPx)
	view := []int{max(l-m, 0), max(t-m, 0), min(r+m, w), min(b+m, h)}
	if 2*max(view[2]-view[0], view[3]-view[1]) > max(w, h) {
		return nil
	}
	return view
}

// drawOverlay draws what was actually measured over the original image: the traced
// silhouette, the boundary between each step, the circle fitted to a rounded end, and a
// 10 mm scale bar. The other objects in the photo are outlined in blue and boxed.
//
// Unlike the Python tool it does not print the numbers into the image. They come back with
// the result, and both the web UI and the MCP response show them next to the picture, so
// the image is free to stay legible at a size worth sending to an agent.
//
// view, when not nil, is the part of img that is drawn (see overlayView), and it is enlarged
// to the overlay size so that a small part is legible. Everything is then drawn in the view's
// pixels, and the scale bar uses the view's scale. When the other objects are not in the view, a
// whole-photo inset shows where the view is (see drawInset).
func drawOverlay(img *imaging.RGB, outline points, fr frame, res *Result, o Options, view []int, others []imaging.Blob) ([]byte, error) {
	var small *imaging.RGB
	var f float64
	ox, oy := 0, 0
	if view != nil {
		ox, oy = view[0], view[1]
		small, f = zoomTo(crop(img, view), o.OverlayPx)
	} else {
		small, f = imaging.Scale(img, o.OverlayPx)
		if small == img { // Scale returned the original: do not draw over the caller's pixels
			small = &imaging.RGB{W: img.W, H: img.H, Pix: make([]uint8, len(img.Pix))}
			copy(small.Pix, img.Pix)
		}
	}
	c := imaging.Canvas{RGB: small}
	// Line weights are in final pixels, so the overlay reads the same at any scan
	// resolution.
	thin := max(small.W/500, 1)
	thick := max(small.W/320, 2)

	// px maps a point in image pixels to the overlay.
	px := func(x, y float64) (int, int) {
		return int(math.Round((x - float64(ox)) * f)), int(math.Round((y - float64(oy)) * f))
	}
	// at maps a point in the part's frame (pixels) to the overlay.
	at := func(x, y float64) (int, int) {
		ix, iy := fr.toImage(x, y)
		return px(ix, iy)
	}

	for _, b := range others {
		for _, q := range b.Outline {
			x, y := px(float64(q.X), float64(q.Y))
			c.Dot(x, y, thick/2, colOther)
		}
		bx := blobBox(b)
		l, t := px(float64(bx[0]), float64(bx[1]))
		r, bt := px(float64(bx[2]-1), float64(bx[3]-1))
		c.Line(l, t, r, t, thin, colOther)
		c.Line(l, bt, r, bt, thin, colOther)
		c.Line(l, t, l, bt, thin, colOther)
		c.Line(r, t, r, bt, thin, colOther)
	}

	for i := range outline.X {
		x, y := px(outline.X[i], outline.Y[i])
		c.Dot(x, y, thick/2, colOutline)
	}
	mmPx := res.ScaleMMPerPx
	if mmPx > 0 {
		halfW := res.OverallWidthMM / mmPx / 2 * 1.15 // reach a little past the widest point

		// A bent strip shows its centreline and knuckles instead of the steps, which follow
		// one straight axis and would only clutter it.
		if cl := res.Centreline; cl != nil {
			path := cl.PixelPath()
			for i := 1; i < len(path); i++ {
				x0, y0 := px(path[i-1][0], path[i-1][1])
				x1, y1 := px(path[i][0], path[i][1])
				c.Line(x0, y0, x1, y1, thin, colSpine)
			}
			for _, e := range cl.Ends {
				ex, ey, er := e.PixelCircle()
				x, y := px(ex, ey)
				c.Circle(float64(x), float64(y), er*f, thin, colTip)
				c.Dot(x, y, thick, colTip)
			}
		}
		// A line across the part at every step boundary, with the step number beside it.
		for i, s := range res.Steps {
			if res.Centreline != nil {
				break
			}
			for _, xmm := range []float64{s.XStartMM, s.XEndMM} {
				x0, y0 := at(xmm/mmPx, -halfW)
				x1, y1 := at(xmm/mmPx, halfW)
				c.Line(x0, y0, x1, y1, thin, colStep)
			}
			lx, ly := at((s.XStartMM+s.XEndMM)/2/mmPx, halfW)
			label(c, lx, ly, fmt.Sprint(i+1), thick)
		}

		// The circle fitted to the leading tip, and its centre on the axis.
		if r := res.RoundEnd; r != nil {
			cx, cy := at(r.CentreXMM/mmPx, 0)
			c.Circle(float64(cx), float64(cy), r.RadiusMM/mmPx*f, thin, colTip)
			c.Dot(cx, cy, thick, colTip)
		}

		// A 10 mm bar: the quickest check on the scale itself, which is the one error that
		// makes every dimension wrong by the same factor. f is this view's scale, so the bar
		// is right for a zoomed view too.
		scaleBar(c, f/mmPx, thin, thick)
	}

	// The inset goes last, so that nothing is drawn over it.
	if view != nil && len(others) > 0 {
		mb := outlineBox(outline)
		l, t := px(float64(mb[0]), float64(mb[1]))
		r, bt := px(float64(mb[2]), float64(mb[3]))
		drawInset(c, img, view, others, mb, []int{l, t, r, bt}, thin)
	}
	return imaging.EncodePNG(small)
}

// insetShare makes the whole-photo inset a quarter of the overlay's long edge.
const insetShare = 4

// drawInset draws the whole photo small in a corner of a zoomed overlay: the view in orange,
// the measured part and the other objects in their colours. The corner is the first of top
// right, top left and bottom right that the measured part does not reach (keepOut, in overlay
// pixels), so the inset never covers the part. mbox is the measured part, in image pixels.
func drawInset(c imaging.Canvas, img *imaging.RGB, view []int, others []imaging.Blob, mbox []int, keepOut []int, thin int) {
	thumb, f := imaging.Scale(img, max(c.W, c.H)/insetShare)
	t := &imaging.RGB{W: thumb.W, H: thumb.H, Pix: make([]uint8, len(thumb.Pix))}
	copy(t.Pix, thumb.Pix)
	tc := imaging.Canvas{RGB: t}
	tthin := max(t.W/150, 1)
	at := func(x, y int) (int, int) { // image pixels to inset pixels
		return int(math.Round(float64(x) * f)), int(math.Round(float64(y) * f))
	}
	box := func(col imaging.Color, b []int) {
		l, tp := at(b[0], b[1])
		r, bt := at(b[2], b[3])
		tc.Line(l, tp, r, tp, tthin, col)
		tc.Line(l, bt, r, bt, tthin, col)
		tc.Line(l, tp, l, bt, tthin, col)
		tc.Line(r, tp, r, bt, tthin, col)
	}
	for _, b := range others {
		box(colOther, blobBox(b))
	}
	box(colOutline, mbox)
	box(colTip, view)

	m := max(c.W/60, 8)
	corners := [][2]int{{c.W - t.W - m, m}, {m, m}, {c.W - t.W - m, c.H - t.H - m}}
	x0, y0 := corners[0][0], corners[0][1]
	for _, p := range corners {
		if !overlapsRect([]int{p[0], p[1], p[0] + t.W, p[1] + t.H}, keepOut) {
			x0, y0 = p[0], p[1]
			break
		}
	}
	c.Rect(x0-thin, y0-thin, x0+t.W+thin, y0+t.H+thin, colLabel)
	for y := range t.H {
		copy(c.Pix[((y0+y)*c.W+x0)*3:((y0+y)*c.W+x0+t.W)*3], t.Pix[y*t.W*3:(y+1)*t.W*3])
	}
}

// overlapsRect reports whether two [left, top, right, bottom) rectangles share a pixel.
func overlapsRect(a, b []int) bool {
	return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3]
}

// zoomTo fits img to maxEdge on its longer side, as imaging.Scale does, and also enlarges an
// image that is smaller than that: a zoomed part is drawn at the size the overlay is, rather
// than at the pixels of the photo. The enlargement repeats pixels (nearest neighbour), so no
// detail is invented. It returns the image and its scale factor.
func zoomTo(img *imaging.RGB, maxEdge int) (*imaging.RGB, float64) {
	long := max(img.W, img.H)
	if long >= maxEdge {
		return imaging.Scale(img, maxEdge)
	}
	f := float64(maxEdge) / float64(long)
	w, h := max(int(float64(img.W)*f+0.5), 1), max(int(float64(img.H)*f+0.5), 1)
	out := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for y := range h {
		sy := y * img.H / h
		for x := range w {
			sx := x * img.W / w
			copy(out.Pix[(y*w+x)*3:(y*w+x)*3+3], img.Pix[(sy*img.W+sx)*3:(sy*img.W+sx)*3+3])
		}
	}
	return out, float64(w) / float64(img.W)
}

// label draws a short string with a dark halo, so it reads over a pale scan and a dark
// part alike.
func label(c imaging.Canvas, x, y int, s string, scale int) {
	w, h := imaging.TextWidth(s, scale), 5*scale
	c.Rect(x-w/2-scale, y-scale, x+w/2+scale, y+h+scale, colShadow)
	imaging.Text(c, x-w/2, y, s, scale, colLabel)
}

// scaleBar draws a reference bar in the bottom-left corner: 10 mm, or the longest of 5, 2 and 1
// mm that fits when the overlay is zoomed on a small part and 10 mm is wider than the picture.
// pxPerMM is in overlay pixels. A bar too short to read is not drawn, since its label would lie.
func scaleBar(c imaging.Canvas, pxPerMM float64, thin, thick int) {
	mm, px := 0, 0
	for _, cand := range []int{10, 5, 2, 1} {
		if p := int(math.Round(float64(cand) * pxPerMM)); p <= c.W-2*thick-16 {
			mm, px = cand, p
			break
		}
	}
	if mm == 0 || px < 8 {
		return
	}
	pad := max(c.W/60, 8)
	y := c.H - pad - thick
	c.Rect(pad-thin, y-thin, pad+px+thin, y+thick+thin, colShadow)
	c.Rect(pad, y, pad+px, y+thick, colLabel)
	label(c, pad+px/2, y-8*thick, fmt.Sprintf("%d mm", mm), thick)
}
