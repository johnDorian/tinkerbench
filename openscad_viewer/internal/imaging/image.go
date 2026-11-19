// Package imaging has the image processing measure needs: channel extraction, blur,
// Otsu thresholding, morphology, contour tracing, Canny edges and a Hough transform.
//
// It is a small stand-in for the handful of OpenCV calls the original Python tool used,
// written against the standard library only so the server stays a single static binary
// (see internal/measure).
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decoders for uploaded scans and photos
	_ "image/png"
	"io"
	"math"
	"os"
)

// MaxPixels caps decoded images. A part fills a small corner of the glass, so anything far
// bigger than this is a whole-bed scan that should be cropped first: measuring it would be
// slow and take hundreds of megabytes, without being any more accurate. A 1200 dpi scan of
// a 100 x 70 mm part is about 16 megapixels, so this leaves plenty of room.
const MaxPixels = 40 << 20

// Gray is an 8-bit single-channel image, one byte per pixel, no padding.
type Gray struct {
	W, H int
	Pix  []uint8
}

func NewGray(w, h int) *Gray { return &Gray{W: w, H: h, Pix: make([]uint8, w*h)} }

func (g *Gray) At(x, y int) uint8     { return g.Pix[y*g.W+x] }
func (g *Gray) Set(x, y int, v uint8) { g.Pix[y*g.W+x] = v }

// In reports whether (x, y) is inside the image.
func (g *Gray) In(x, y int) bool { return x >= 0 && y >= 0 && x < g.W && y < g.H }

// Clone returns a copy.
func (g *Gray) Clone() *Gray {
	c := &Gray{W: g.W, H: g.H, Pix: make([]uint8, len(g.Pix))}
	copy(c.Pix, g.Pix)
	return c
}

// RGB is an 8-bit colour image, three bytes per pixel.
type RGB struct {
	W, H int
	Pix  []uint8
}

func (c *RGB) At(x, y int) (r, g, b uint8) {
	i := (y*c.W + x) * 3
	return c.Pix[i], c.Pix[i+1], c.Pix[i+2]
}

// Load decodes a PNG or JPEG file into an RGB image and reports its stored resolution in
// dpi (0 when the file has none: scanners record it, phone cameras usually do not).
func Load(path string) (*RGB, float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, 0, fmt.Errorf("decode %s: %w", path, err)
	}
	c, err := fromImage(img)
	if err != nil {
		return nil, 0, err
	}
	dpi := 0.0
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		dpi, _ = DPI(f)
	}
	return c, dpi, nil
}

// Decode reads a PNG or JPEG from b.
func Decode(b []byte) (*RGB, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return fromImage(img)
}

func fromImage(img image.Image) (*RGB, error) {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty image")
	}
	if w*h > MaxPixels {
		return nil, fmt.Errorf("image is %dx%d (%.0f megapixels), over the %d MP limit: crop it to the part and try again (cropping does not cost accuracy, the scale comes from the dpi)",
			w, h, float64(w)*float64(h)/(1<<20), MaxPixels>>20)
	}
	out := &RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	// Fast paths for what the decoders actually produce, with a generic fallback.
	switch src := img.(type) {
	case *image.YCbCr: // baseline JPEG
		for y := range h {
			for x := range w {
				px, py := r.Min.X+x, r.Min.Y+y
				ci := src.COffset(px, py)
				cr, cg, cb := color.YCbCrToRGB(src.Y[src.YOffset(px, py)], src.Cb[ci], src.Cr[ci])
				i := (y*w + x) * 3
				out.Pix[i], out.Pix[i+1], out.Pix[i+2] = cr, cg, cb
			}
		}
	case *image.Gray: // greyscale PNG
		for y := range h {
			row := src.Pix[(y+r.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := range w {
				v := row[x+r.Min.X-src.Rect.Min.X]
				i := (y*w + x) * 3
				out.Pix[i], out.Pix[i+1], out.Pix[i+2] = v, v, v
			}
		}
	case *image.NRGBA:
		for y := range h {
			row := src.Pix[(y+r.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := range w {
				j := (x + r.Min.X - src.Rect.Min.X) * 4
				i := (y*w + x) * 3
				out.Pix[i], out.Pix[i+1], out.Pix[i+2] = row[j], row[j+1], row[j+2]
			}
		}
	default:
		for y := range h {
			for x := range w {
				cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
				i := (y*w + x) * 3
				out.Pix[i], out.Pix[i+1], out.Pix[i+2] = uint8(cr>>8), uint8(cg>>8), uint8(cb>>8)
			}
		}
	}
	return out, nil
}

// ---------- channels ----------

// Channel names accepted by Extract.
const (
	ChannelGray = "gray" // right for a scan
	ChannelA    = "a"    // Lab a*: separates a shadow from the part by colour, not brightness
	ChannelB    = "b"    // Lab b*
	ChannelSat  = "sat"  // HSV saturation
)

// Channels lists the channels Extract understands.
func Channels() []string { return []string{ChannelGray, ChannelA, ChannelB, ChannelSat} }

// Extract pulls one single-channel image out of a colour image, using the same definitions
// as OpenCV's cvtColor so thresholds behave the same way.
func Extract(c *RGB, name string) (*Gray, error) {
	g := NewGray(c.W, c.H)
	switch name {
	case "", ChannelGray:
		for i, n := 0, c.W*c.H; i < n; i++ {
			r, gg, b := int(c.Pix[i*3]), int(c.Pix[i*3+1]), int(c.Pix[i*3+2])
			g.Pix[i] = uint8((r*4899 + gg*9617 + b*1868 + 8192) >> 14) // 0.299/0.587/0.114
		}
	case ChannelA, ChannelB:
		wantA := name == ChannelA
		for i, n := 0, c.W*c.H; i < n; i++ {
			_, a, bb := labOf(c.Pix[i*3], c.Pix[i*3+1], c.Pix[i*3+2])
			v := bb
			if wantA {
				v = a
			}
			g.Pix[i] = clamp8(v + 128)
		}
	case ChannelSat:
		for i, n := 0, c.W*c.H; i < n; i++ {
			r, gg, b := int(c.Pix[i*3]), int(c.Pix[i*3+1]), int(c.Pix[i*3+2])
			hi, lo := max(r, gg, b), min(r, gg, b)
			if hi > 0 {
				g.Pix[i] = uint8((hi - lo) * 255 / hi)
			}
		}
	default:
		return nil, fmt.Errorf("unknown channel %q: use gray, a, b or sat", name)
	}
	return g, nil
}

// srgbLinear is the inverse sRGB transfer function, tabulated for the 256 input levels.
var srgbLinear = func() [256]float64 {
	var t [256]float64
	for i := range t {
		v := float64(i) / 255
		if v <= 0.04045 {
			t[i] = v / 12.92
		} else {
			t[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return t
}()

// labOf converts sRGB to CIE L*a*b* (D65), as OpenCV's COLOR_BGR2LAB does before it
// offsets a and b by 128.
func labOf(r8, g8, b8 uint8) (l, a, b float64) {
	r, g, bl := srgbLinear[r8], srgbLinear[g8], srgbLinear[b8]
	x := (0.412453*r + 0.357580*g + 0.180423*bl) / 0.950456
	y := 0.212671*r + 0.715160*g + 0.072169*bl
	z := (0.019334*r + 0.119193*g + 0.950227*bl) / 1.088754
	fx, fy, fz := labF(x), labF(y), labF(z)
	if y > 0.008856 {
		l = 116*fy - 16
	} else {
		l = 903.3 * y
	}
	return l, 500 * (fx - fy), 200 * (fy - fz)
}

func labF(t float64) float64 {
	if t > 0.008856 {
		return math.Cbrt(t)
	}
	return 7.787*t + 16.0/116.0
}

func clamp8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}

// ---------- stored resolution ----------

// DPI reads the resolution recorded in a PNG (pHYs) or JPEG (JFIF density, else Exif
// XResolution) and returns 0 when the file does not say.
func DPI(r io.Reader) (float64, error) {
	head := make([]byte, 1<<16)
	n, err := io.ReadFull(r, head)
	if n == 0 {
		return 0, err
	}
	head = head[:n]
	switch {
	case len(head) > 8 && string(head[1:4]) == "PNG":
		return pngDPI(head), nil
	case len(head) > 3 && head[0] == 0xff && head[1] == 0xd8:
		return jpegDPI(head), nil
	}
	return 0, nil
}

func pngDPI(b []byte) float64 {
	for p := 8; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		body := p + 8
		if n < 0 || body+n > len(b) {
			return 0
		}
		if typ == "pHYs" && n >= 9 && b[body+8] == 1 { // unit 1 = pixels per metre
			return float64(binary.BigEndian.Uint32(b[body:])) * 0.0254
		}
		if typ == "IDAT" {
			return 0 // past the metadata
		}
		p = body + n + 4
	}
	return 0
}

func jpegDPI(b []byte) float64 {
	for p := 2; p+4 <= len(b); {
		if b[p] != 0xff {
			return 0
		}
		marker := b[p+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			p += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[p+2:]))
		seg := b[p+4:]
		if n < 2 || p+2+n > len(b) {
			return 0
		}
		seg = seg[:n-2]
		switch {
		case marker == 0xe0 && len(seg) >= 12 && string(seg[:5]) == "JFIF\x00":
			x := float64(binary.BigEndian.Uint16(seg[8:]))
			switch seg[7] {
			case 1: // dots per inch
				return x
			case 2: // dots per cm
				return x * 2.54
			}
		case marker == 0xe1 && len(seg) >= 6 && string(seg[:4]) == "Exif":
			if d := exifDPI(seg[6:]); d > 0 {
				return d
			}
		case marker == 0xda: // start of scan: no metadata past here
			return 0
		}
		p += 2 + n
	}
	return 0
}

// exifDPI reads XResolution/ResolutionUnit out of a TIFF header.
func exifDPI(t []byte) float64 {
	if len(t) < 8 {
		return 0
	}
	var ord binary.ByteOrder = binary.BigEndian
	switch string(t[:2]) {
	case "II":
		ord = binary.LittleEndian
	case "MM":
	default:
		return 0
	}
	off := int(ord.Uint32(t[4:]))
	if off+2 > len(t) {
		return 0
	}
	count := int(ord.Uint16(t[off:]))
	res, unit := 0.0, 2
	for i := range count {
		e := off + 2 + i*12
		if e+12 > len(t) {
			break
		}
		tag := ord.Uint16(t[e:])
		v := int(ord.Uint32(t[e+8:]))
		switch tag {
		case 0x011a: // XResolution, a RATIONAL pointed at by the value field
			if v+8 <= len(t) {
				num, den := ord.Uint32(t[v:]), ord.Uint32(t[v+4:])
				if den != 0 {
					res = float64(num) / float64(den)
				}
			}
		case 0x0128: // ResolutionUnit: 2 = inch, 3 = cm
			unit = int(ord.Uint16(t[e+8:]))
		}
	}
	if unit == 3 {
		res *= 2.54
	}
	return res
}
