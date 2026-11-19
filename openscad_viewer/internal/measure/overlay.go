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
	colLabel   = imaging.Color{R: 255, G: 255, B: 255} // step numbers and the scale bar
	colShadow  = imaging.Color{R: 20, G: 20, B: 20}
)

// drawOverlay draws what was actually measured over the original image: the traced
// silhouette, the boundary between each step, the circle fitted to a rounded end, and a
// 10 mm scale bar.
//
// Unlike the Python tool it does not print the numbers into the image. They come back with
// the result, and both the web UI and the MCP response show them next to the picture, so
// the image is free to stay legible at a size worth sending to an agent.
func drawOverlay(img *imaging.RGB, outline points, fr frame, res *Result, o Options) ([]byte, error) {
	small, f := imaging.Scale(img, o.OverlayPx)
	if small == img { // Scale returned the original: do not draw over the caller's pixels
		small = &imaging.RGB{W: img.W, H: img.H, Pix: make([]uint8, len(img.Pix))}
		copy(small.Pix, img.Pix)
	}
	c := imaging.Canvas{RGB: small}
	// Line weights are in final pixels, so the overlay reads the same at any scan
	// resolution.
	thin := max(small.W/500, 1)
	thick := max(small.W/320, 2)

	// at maps a point in the part's frame (pixels) to the overlay.
	at := func(x, y float64) (int, int) {
		ix, iy := fr.toImage(x, y)
		return int(math.Round(ix * f)), int(math.Round(iy * f))
	}

	for i := range outline.X {
		c.Dot(int(math.Round(outline.X[i]*f)), int(math.Round(outline.Y[i]*f)), thick/2, colOutline)
	}

	mmPx := res.ScaleMMPerPx
	if mmPx > 0 {
		halfW := res.OverallWidthMM / mmPx / 2 * 1.15 // reach a little past the widest point

		// A line across the part at every step boundary, with the step number beside it.
		for i, s := range res.Steps {
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
		// makes every dimension wrong by the same factor.
		scaleBar(c, f/mmPx, thin, thick)
	}
	return imaging.EncodePNG(small)
}

// label draws a short string with a dark halo, so it reads over a pale scan and a dark
// part alike.
func label(c imaging.Canvas, x, y int, s string, scale int) {
	w, h := imaging.TextWidth(s, scale), 5*scale
	c.Rect(x-w/2-scale, y-scale, x+w/2+scale, y+h+scale, colShadow)
	imaging.Text(c, x-w/2, y, s, scale, colLabel)
}

// scaleBar draws a 10 mm reference bar in the bottom-left corner. pxPerMM is in overlay
// pixels.
func scaleBar(c imaging.Canvas, pxPerMM float64, thin, thick int) {
	const mm = 10
	px := int(math.Round(mm * pxPerMM))
	if px < 8 || px > c.W-2*thick-16 {
		return // too small to read, or wider than the image: the label would lie
	}
	pad := max(c.W/60, 8)
	y := c.H - pad - thick
	c.Rect(pad-thin, y-thin, pad+px+thin, y+thick+thin, colShadow)
	c.Rect(pad, y, pad+px, y+thick, colLabel)
	label(c, pad+px/2, y-8*thick, "10 mm", thick)
}
