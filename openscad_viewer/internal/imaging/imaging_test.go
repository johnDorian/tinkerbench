package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
)

func TestEllipseSE(t *testing.T) {
	// The same element OpenCV's getStructuringElement(MORPH_ELLIPSE, (7,7)) builds.
	want := []int{0, 2, 3, 3, 3, 2, 0}
	se := EllipseSE(7)
	if len(se) != 7 {
		t.Fatalf("got %d rows, want 7", len(se))
	}
	for i, r := range se {
		if r.DY != i-3 || r.HalfW != want[i] {
			t.Errorf("row %d = {dy %d, half %d}, want {dy %d, half %d}", i, r.DY, r.HalfW, i-3, want[i])
		}
	}
}

func TestOtsuSplitsTwoPeaks(t *testing.T) {
	g := NewGray(100, 100)
	for i := range g.Pix {
		if i%2 == 0 {
			g.Pix[i] = 40
		} else {
			g.Pix[i] = 200
		}
	}
	if got := Otsu(g); got < 40 || got >= 200 {
		t.Errorf("threshold %d does not lie between the two peaks (40, 200)", got)
	}
	m := Threshold(g, Otsu(g), true)
	var set int
	for _, v := range m.Pix {
		if v == 255 {
			set++
		}
	}
	if set != len(g.Pix)/2 {
		t.Errorf("%d pixels above the threshold, want half of %d", set, len(g.Pix))
	}
}

func TestBlur5KeepsFlatImage(t *testing.T) {
	g := NewGray(32, 32)
	for i := range g.Pix {
		g.Pix[i] = 123
	}
	for i, v := range Blur5(g).Pix {
		if v != 123 { // including at the border, where reflection must not darken it
			t.Fatalf("pixel %d = %d after blurring a flat image, want 123", i, v)
		}
	}
}

// rect fills a rectangle in a mask.
func rect(g *Gray, x0, y0, x1, y1 int, v uint8) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			g.Pix[y*g.W+x] = v
		}
	}
}

func TestBlobsTracesOuterBorder(t *testing.T) {
	g := NewGray(60, 40)
	rect(g, 10, 8, 29, 27, 255) // 20x20
	rect(g, 15, 13, 24, 22, 0)  // a hole in the middle
	rect(g, 45, 30, 49, 34, 255)

	blobs := Blobs(g)
	if len(blobs) != 2 {
		t.Fatalf("found %d blobs, want 2", len(blobs))
	}
	big := blobs[0]
	if want := 20*20 - 10*10; big.Area != want {
		t.Errorf("area = %d, want %d (the ring, not the hole)", big.Area, want)
	}
	// The outer border is traced once round the outside: a 20x20 square has 76 edge
	// pixels, and the hole must not appear in it.
	if len(big.Outline) != 76 {
		t.Errorf("outline has %d points, want 76", len(big.Outline))
	}
	for _, p := range big.Outline {
		if p.X >= 15 && p.X <= 24 && p.Y >= 13 && p.Y <= 22 {
			t.Fatalf("the hole's border leaked into the outer outline at %+v", p)
		}
	}
	if got, want := Area(big.Outline), 19.0*19.0; math.Abs(got-want) > 1 {
		t.Errorf("polygon area = %.0f, want about %.0f", got, want)
	}
	if blobs[1].Area != 25 {
		t.Errorf("second blob area = %d, want 25 (blobs should be largest first)", blobs[1].Area)
	}
}

func TestBlobsSinglePixel(t *testing.T) {
	g := NewGray(10, 10)
	g.Pix[5*10+5] = 255
	blobs := Blobs(g)
	if len(blobs) != 1 || blobs[0].Area != 1 || len(blobs[0].Outline) != 1 {
		t.Fatalf("a lone pixel gave %+v", blobs)
	}
}

func TestMorphologyRemovesSpeckleAndFillsGaps(t *testing.T) {
	g := NewGray(60, 60)
	rect(g, 20, 20, 39, 39, 255)
	g.Pix[5*60+5] = 255        // speckle
	rect(g, 28, 20, 31, 39, 0) // a thin gap across the square
	se := EllipseSE(7)

	closed := Close(g, se)
	if closed.At(29, 30) != 255 {
		t.Error("closing should have bridged the gap")
	}
	opened := Open(closed, se)
	if opened.At(5, 5) != 0 {
		t.Error("opening should have removed the lone speckle")
	}
	if opened.At(30, 30) != 255 {
		t.Error("opening removed the square itself")
	}
}

func TestCannyFindsAStraightEdge(t *testing.T) {
	g := NewGray(80, 80)
	rect(g, 40, 0, 79, 79, 255) // a vertical step down the middle
	e := Canny(g, 40, 120)
	var on, off int
	for y := 5; y < 75; y++ {
		for x := 1; x < 79; x++ {
			if e.At(x, y) == 0 {
				continue
			}
			if x >= 38 && x <= 41 {
				on++
			} else {
				off++
			}
		}
	}
	if on < 60 {
		t.Errorf("found the edge in only %d of 70 rows", on)
	}
	if off > 0 {
		t.Errorf("%d edge pixels away from the step: non-maximum suppression failed", off)
	}
}

func TestHoughFindsTheAngleOfALine(t *testing.T) {
	g := NewGray(200, 200)
	for x := range 200 { // a line at 30 degrees
		y := int(float64(x)*math.Tan(30*math.Pi/180)) + 10
		if y >= 0 && y < 200 {
			g.Pix[y*200+x] = 255
		}
	}
	lines := HoughLines(g, 1, math.Pi/720, 100, 10)
	if len(lines) == 0 {
		t.Fatal("no lines found")
	}
	// A line at 30 degrees has a normal at 120 degrees.
	got := lines[0].Theta * 180 / math.Pi
	if math.Abs(got-120) > 1.5 {
		t.Errorf("strongest line normal at %.1f degrees, want about 120", got)
	}
}

func TestRotateNearest(t *testing.T) {
	g := NewGray(21, 21)
	rect(g, 8, 2, 12, 18, 255) // a tall bar
	r := RotateNearest(g, 90)
	if r.At(10, 10) != 255 {
		t.Error("the centre should stay set after rotating")
	}
	if r.At(10, 3) != 0 || r.At(3, 10) != 255 {
		t.Error("the bar should have turned from vertical to horizontal")
	}
}

func TestScaleShrinks(t *testing.T) {
	c := &RGB{W: 400, H: 200, Pix: make([]uint8, 400*200*3)}
	for i := range c.Pix {
		c.Pix[i] = 100
	}
	small, f := Scale(c, 100)
	if small.W != 100 || small.H != 50 {
		t.Errorf("scaled to %dx%d, want 100x50", small.W, small.H)
	}
	if math.Abs(f-0.25) > 1e-9 {
		t.Errorf("factor %g, want 0.25", f)
	}
	if small.Pix[0] != 100 {
		t.Errorf("box filter changed a flat colour to %d", small.Pix[0])
	}
	if same, f := Scale(c, 1000); same != c || f != 1 {
		t.Error("an image that already fits should come back untouched")
	}
}

// pngWithDPI encodes a PNG and splices in the pHYs chunk that records a resolution, the
// way a scanner writes one.
func pngWithDPI(dpi float64) []byte {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4)))
	b := buf.Bytes()

	body := make([]byte, 9)
	ppm := uint32(math.Round(dpi / 0.0254))
	binary.BigEndian.PutUint32(body[0:], ppm)
	binary.BigEndian.PutUint32(body[4:], ppm)
	body[8] = 1 // unit: pixels per metre

	chunk := make([]byte, 0, 21)
	chunk = binary.BigEndian.AppendUint32(chunk, 9)
	chunk = append(chunk, "pHYs"...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))

	const afterIHDR = 8 + 25 // signature + IHDR chunk
	return append(append(append([]byte{}, b[:afterIHDR]...), chunk...), b[afterIHDR:]...)
}

func TestDPIFromPNG(t *testing.T) {
	got, err := DPI(bytes.NewReader(pngWithDPI(1200)))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1200) > 0.01 {
		t.Errorf("dpi = %g, want about 1200", got)
	}
	// A file that records nothing says so, rather than guessing.
	var plain bytes.Buffer
	png.Encode(&plain, image.NewGray(image.Rect(0, 0, 4, 4)))
	if got, _ := DPI(bytes.NewReader(plain.Bytes())); got != 0 {
		t.Errorf("dpi = %g for a PNG with no pHYs, want 0", got)
	}
}

func TestDPIFromJPEG(t *testing.T) {
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil)
	b := buf.Bytes()
	// Go writes a JFIF APP0 with "no units"; set it to dots per inch at 600.
	i := bytes.Index(b, []byte("JFIF\x00"))
	if i < 0 {
		t.Skip("no JFIF header to patch")
	}
	b[i+7] = 1 // units: dpi
	binary.BigEndian.PutUint16(b[i+8:], 600)
	binary.BigEndian.PutUint16(b[i+10:], 600)
	if got, _ := DPI(bytes.NewReader(b)); got != 600 {
		t.Errorf("dpi = %g, want 600", got)
	}
}

func TestDecodeRejectsRubbish(t *testing.T) {
	if _, err := Decode([]byte("this is not an image")); err == nil {
		t.Fatal("want an error")
	}
}

func TestExtractChannels(t *testing.T) {
	c := &RGB{W: 1, H: 1, Pix: []uint8{200, 50, 50}} // a saturated red
	for _, name := range Channels() {
		if _, err := Extract(c, name); err != nil {
			t.Errorf("channel %s: %v", name, err)
		}
	}
	if _, err := Extract(c, "nope"); err == nil {
		t.Error("want an error for an unknown channel")
	}
	g, _ := Extract(c, ChannelGray)
	if want := uint8((200*4899 + 50*9617 + 50*1868 + 8192) >> 14); g.Pix[0] != want {
		t.Errorf("grey = %d, want %d", g.Pix[0], want)
	}
	a, _ := Extract(c, ChannelA) // red is strongly positive on a*
	if a.Pix[0] <= 128 {
		t.Errorf("a* = %d for red, want well above 128", a.Pix[0])
	}
	s, _ := Extract(c, ChannelSat)
	if want := uint8((200 - 50) * 255 / 200); s.Pix[0] != want {
		t.Errorf("saturation = %d, want %d", s.Pix[0], want)
	}
}

func TestEncodePNGRoundTrip(t *testing.T) {
	c := &RGB{W: 3, H: 2, Pix: make([]uint8, 3*2*3)}
	Canvas{RGB: c}.Rect(0, 0, 2, 1, Color{R: 10, G: 20, B: 30})
	b, err := EncodePNG(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if back.W != 3 || back.H != 2 || !bytes.Equal(back.Pix, c.Pix) {
		t.Error("the image did not survive a PNG round trip")
	}
}

func TestTextWidth(t *testing.T) {
	if got := TextWidth("10", 2); got != 14 { // two glyphs of 3, a gap of 1, times 2
		t.Errorf("width = %d, want 14", got)
	}
	if TextWidth("", 2) != 0 {
		t.Error("an empty string should take no width")
	}
}

// bruteMorph is the obvious, slow definition of a min/max filter, used to check the
// sliding-window one against.
func bruteMorph(g *Gray, se SE, dilate bool) *Gray {
	out := NewGray(g.W, g.H)
	for y := range g.H {
		for x := range g.W {
			var v uint8 = 255
			if dilate {
				v = 0
			}
			for _, r := range se {
				yy := y + r.DY
				if yy < 0 || yy >= g.H {
					continue
				}
				for xx := x - r.HalfW; xx <= x+r.HalfW; xx++ {
					if xx < 0 || xx >= g.W {
						continue
					}
					p := g.Pix[yy*g.W+xx]
					if dilate {
						v = max(v, p)
					} else {
						v = min(v, p)
					}
				}
			}
			out.Pix[y*g.W+x] = v
		}
	}
	return out
}

// TestMorphMatchesBruteForce pins the sliding-window implementation to the plain
// definition, including at the borders and for window sizes that do not divide the width.
func TestMorphMatchesBruteForce(t *testing.T) {
	seed := uint32(12345)
	rnd := func() uint8 { // xorshift: a repeatable pattern without a dependency
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return uint8(seed)
	}
	for _, size := range []int{3, 5, 7, 9} {
		se := EllipseSE(size)
		for _, dim := range [][2]int{{1, 1}, {4, 3}, {17, 5}, {23, 19}, {64, 8}} {
			g := NewGray(dim[0], dim[1])
			for i := range g.Pix {
				g.Pix[i] = rnd()
			}
			for _, dilate := range []bool{true, false} {
				want := bruteMorph(g, se, dilate)
				got := morph(g, se, dilate)
				for i := range want.Pix {
					if got.Pix[i] != want.Pix[i] {
						t.Fatalf("size %d, %dx%d, dilate=%v: pixel %d (x %d, y %d) = %d, want %d",
							size, dim[0], dim[1], dilate, i, i%g.W, i/g.W, got.Pix[i], want.Pix[i])
					}
				}
			}
		}
	}
}
