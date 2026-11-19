package imaging

import (
	"bytes"
	"image"
	"image/png"
	"math"
)

// Color is an 8-bit opaque colour.
type Color struct{ R, G, B uint8 }

// Canvas draws annotations onto an RGB image.
type Canvas struct{ *RGB }

func (c Canvas) set(x, y int, col Color) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	i := (y*c.W + x) * 3
	c.Pix[i], c.Pix[i+1], c.Pix[i+2] = col.R, col.G, col.B
}

// Dot draws a filled square of side 2*r+1.
func (c Canvas) Dot(x, y, r int, col Color) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			c.set(x+dx, y+dy, col)
		}
	}
}

// Line draws a straight line of the given thickness (Bresenham, thickened by drawing a
// square at each step).
func (c Canvas) Line(x0, y0, x1, y1, thick int, col Color) {
	r := max(thick/2, 0)
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.Dot(x0, y0, r, col)
		if x0 == x1 && y0 == y1 {
			return
		}
		if e2 := 2 * err; e2 >= dy {
			err += dy
			x0 += sx
		} else {
			err += dx
			y0 += sy
		}
	}
}

// Circle draws a circle outline by stepping round it in roughly one-pixel increments.
func (c Canvas) Circle(cx, cy, radius float64, thick int, col Color) {
	if radius <= 0 {
		return
	}
	steps := max(int(2*math.Pi*radius), 24)
	r := max(thick/2, 0)
	for i := range steps {
		a := 2 * math.Pi * float64(i) / float64(steps)
		c.Dot(int(math.Round(cx+radius*math.Cos(a))), int(math.Round(cy+radius*math.Sin(a))), r, col)
	}
}

// Rect fills a rectangle.
func (c Canvas) Rect(x0, y0, x1, y1 int, col Color) {
	for y := min(y0, y1); y <= max(y0, y1); y++ {
		for x := min(x0, x1); x <= max(x0, x1); x++ {
			c.set(x, y, col)
		}
	}
}

// glyphs is a 3x5 bitmap font, one byte per row, low three bits left to right. It covers
// what the overlay labels need: step numbers, a scale bar and its unit.
var glyphs = map[rune][5]uint8{
	'0': {0b111, 0b101, 0b101, 0b101, 0b111},
	'1': {0b010, 0b110, 0b010, 0b010, 0b111},
	'2': {0b111, 0b001, 0b111, 0b100, 0b111},
	'3': {0b111, 0b001, 0b111, 0b001, 0b111},
	'4': {0b101, 0b101, 0b111, 0b001, 0b001},
	'5': {0b111, 0b100, 0b111, 0b001, 0b111},
	'6': {0b111, 0b100, 0b111, 0b101, 0b111},
	'7': {0b111, 0b001, 0b001, 0b001, 0b001},
	'8': {0b111, 0b101, 0b111, 0b101, 0b111},
	'9': {0b111, 0b101, 0b111, 0b001, 0b111},
	'.': {0b000, 0b000, 0b000, 0b000, 0b010},
	'-': {0b000, 0b000, 0b111, 0b000, 0b000},
	'm': {0b000, 0b000, 0b111, 0b111, 0b101},
	' ': {0, 0, 0, 0, 0},
}

// TextWidth is the width in pixels Text will take at the given scale.
func TextWidth(s string, scale int) int {
	if len(s) == 0 {
		return 0
	}
	return len([]rune(s))*4*scale - scale
}

// Text draws a short label with its top-left corner at (x, y), each glyph pixel becoming a
// scale x scale block. Unknown runes are skipped.
func Text(c Canvas, x, y int, s string, scale int, col Color) {
	for _, ch := range s {
		g, ok := glyphs[ch]
		if !ok {
			continue
		}
		for row := range 5 {
			for bit := range 3 {
				if g[row]&(1<<(2-bit)) != 0 {
					c.Rect(x+bit*scale, y+row*scale, x+(bit+1)*scale-1, y+(row+1)*scale-1, col)
				}
			}
		}
		x += 4 * scale
	}
}

// EncodePNG encodes an RGB image.
func EncodePNG(c *RGB) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
	for y := range c.H {
		for x := range c.W {
			i := (y*c.W + x) * 3
			j := y*img.Stride + x*4
			img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = c.Pix[i], c.Pix[i+1], c.Pix[i+2], 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
