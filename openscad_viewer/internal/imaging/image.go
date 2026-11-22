// Package imaging has the image processing measure needs: channel extraction, blur,
// Otsu thresholding, morphology, contour tracing, Canny edges and a Hough transform.
//
// It is a small stand-in for the handful of OpenCV calls the original Python tool used,
// written against the standard library only so the server stays a single static binary
// (see internal/measure).
package imaging

import (
	"bytes"
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

// Load decodes a PNG or JPEG file into an RGB image, turned upright by its EXIF orientation
// so that it matches what a photo viewer shows. It also returns what the file says about
// itself (resolution, camera).
func Load(path string) (*RGB, Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Meta{}, err
	}
	defer f.Close()
	meta, err := ReadMeta(f)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("read %s: %w", path, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, Meta{}, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("decode %s: %w", path, err)
	}
	c, err := fromImage(img, meta.Orientation)
	if err != nil {
		return nil, Meta{}, err
	}
	return c, meta, nil
}

// Decode reads a PNG or JPEG from b, upright as Load does.
func Decode(b []byte) (*RGB, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	head := b
	if len(head) > headerBytes {
		head = head[:headerBytes]
	}
	return fromImage(img, HeadMeta(head).Orientation)
}

// orientedSize is the size of an image after EXIF orientation o (1-8) is applied: the
// quarter turns (5 to 8) swap the width and the height.
func orientedSize(w, h, o int) (int, int) {
	if o >= 5 && o <= 8 {
		return h, w
	}
	return w, h
}

// orientedIndex maps a source pixel (x, y) of a w x h image to its position in the upright
// image, for EXIF orientation o. The eight cases are the mirrors and turns of the EXIF
// specification: 2 mirror, 3 half turn, 4 mirror vertically, 5 transpose, 6 quarter turn
// clockwise, 7 transverse, 8 quarter turn anticlockwise.
func orientedIndex(o, w, h, x, y int) (int, int) {
	switch o {
	case 2:
		return w - 1 - x, y
	case 3:
		return w - 1 - x, h - 1 - y
	case 4:
		return x, h - 1 - y
	case 5:
		return y, x
	case 6:
		return h - 1 - y, x
	case 7:
		return h - 1 - y, w - 1 - x
	case 8:
		return y, w - 1 - x
	}
	return x, y
}

func fromImage(img image.Image, orientation int) (*RGB, error) {
	r := img.Bounds()
	sw, sh := r.Dx(), r.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, errors.New("empty image")
	}
	if sw*sh > MaxPixels {
		return nil, fmt.Errorf("image is %dx%d (%.0f megapixels), over the %d MP limit: crop it to the part and try again (cropping does not cost accuracy, the scale comes from the dpi)",
			sw, sh, float64(sw)*float64(sh)/(1<<20), MaxPixels>>20)
	}
	w, h := orientedSize(sw, sh, orientation)
	out := &RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	// put writes source pixel (x, y), 0-based, to its upright place.
	put := func(x, y int, r, g, b uint8) {
		dx, dy := orientedIndex(orientation, sw, sh, x, y)
		i := (dy*w + dx) * 3
		out.Pix[i], out.Pix[i+1], out.Pix[i+2] = r, g, b
	}
	// Fast paths for what the decoders actually produce, with a generic fallback.
	switch src := img.(type) {
	case *image.YCbCr: // baseline JPEG
		for y := range sh {
			for x := range sw {
				px, py := r.Min.X+x, r.Min.Y+y
				ci := src.COffset(px, py)
				cr, cg, cb := color.YCbCrToRGB(src.Y[src.YOffset(px, py)], src.Cb[ci], src.Cr[ci])
				put(x, y, cr, cg, cb)
			}
		}
	case *image.Gray: // greyscale PNG
		for y := range sh {
			row := src.Pix[(y+r.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := range sw {
				v := row[x+r.Min.X-src.Rect.Min.X]
				put(x, y, v, v, v)
			}
		}
	case *image.NRGBA:
		for y := range sh {
			row := src.Pix[(y+r.Min.Y-src.Rect.Min.Y)*src.Stride:]
			for x := range sw {
				j := (x + r.Min.X - src.Rect.Min.X) * 4
				put(x, y, row[j], row[j+1], row[j+2])
			}
		}
	default:
		for y := range sh {
			for x := range sw {
				cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
				put(x, y, uint8(cr>>8), uint8(cg>>8), uint8(cb>>8))
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
