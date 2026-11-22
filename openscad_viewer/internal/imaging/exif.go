package imaging

import (
	"encoding/binary"
	"io"
	"strings"
)

// Meta is what a PNG or JPEG file says about itself, beyond its pixels.
type Meta struct {
	DPI         float64 // resolution the file records, 0 when it records none
	Orientation int     // EXIF orientation 1-8; 0 when the file has none (same as 1)
	Make, Model string  // camera maker and model, from EXIF IFD0
	Camera      bool    // Make or Model is present: a camera took this picture
}

// DPIUsable reports whether the recorded resolution is a measurement of the paper. A phone
// writes 72 or 300 dpi whatever it photographed, so a camera's dpi is never a scale; only a
// scanner (or software that exports a known resolution) earns that.
func (m Meta) DPIUsable() bool { return m.DPI > 1 && !m.Camera }

// CameraName is "Make Model" with the maker not repeated when the model already starts with
// it, or "" for a file with no camera in it.
func (m Meta) CameraName() string {
	if !m.Camera {
		return ""
	}
	switch {
	case m.Make == "":
		return m.Model
	case m.Model == "", strings.HasPrefix(strings.ToLower(m.Model), strings.ToLower(m.Make)):
		if m.Model == "" {
			return m.Make
		}
		return m.Model
	}
	return m.Make + " " + m.Model
}

// headerBytes is how much of a file HeadMeta looks at. The metadata of a phone photo sits
// in its first few kilobytes; the thumbnail is larger, but it comes after the fields we
// want.
const headerBytes = 1 << 18

// ReadMeta reads the metadata at the start of a PNG or JPEG stream. It reads at most the
// header, so the caller can seek back and decode the image afterwards.
func ReadMeta(r io.Reader) (Meta, error) {
	head := make([]byte, headerBytes)
	n, err := io.ReadFull(r, head)
	if n == 0 {
		return Meta{}, err
	}
	return HeadMeta(head[:n]), nil
}

// HeadMeta parses the metadata of a PNG or JPEG from the bytes at the start of the file.
// Anything it does not recognise is left at its zero value.
func HeadMeta(head []byte) Meta {
	if len(head) > 8 && string(head[1:4]) == "PNG" {
		return pngMeta(head)
	}
	if len(head) > 3 && head[0] == 0xff && head[1] == 0xd8 {
		return jpegMeta(head)
	}
	return Meta{}
}

// DPI reads the resolution recorded in a PNG (pHYs) or JPEG (JFIF density, else Exif
// XResolution) and returns 0 when the file does not say. It is ReadMeta's DPI on its own.
func DPI(r io.Reader) (float64, error) {
	m, err := ReadMeta(r)
	return m.DPI, err
}

func pngMeta(b []byte) Meta {
	var m Meta
	for p := 8; p+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[p:]))
		typ := string(b[p+4 : p+8])
		body := p + 8
		if n < 0 || body+n > len(b) {
			break
		}
		switch typ {
		case "pHYs":
			if n >= 9 && b[body+8] == 1 && m.DPI == 0 { // unit 1 = pixels per metre
				m.DPI = float64(binary.BigEndian.Uint32(b[body:])) * 0.0254
			}
		case "eXIf": // a raw TIFF structure, as in JPEG's APP1 minus the "Exif" prefix
			m.applyTIFF(b[body : body+n])
		case "IDAT":
			return m
		}
		p = body + n + 4
	}
	return m
}

func jpegMeta(b []byte) Meta {
	var m Meta
	for p := 2; p+4 <= len(b); {
		if b[p] != 0xff {
			break
		}
		marker := b[p+1]
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			p += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[p+2:]))
		if n < 2 || p+2+n > len(b) {
			break
		}
		seg := b[p+4 : p+2+n]
		switch {
		case marker == 0xe0 && len(seg) >= 12 && string(seg[:5]) == "JFIF\x00":
			if m.DPI == 0 {
				x := float64(binary.BigEndian.Uint16(seg[8:]))
				switch seg[7] {
				case 1: // dots per inch
					m.DPI = x
				case 2: // dots per cm
					m.DPI = x * 2.54
				}
			}
		case marker == 0xe1 && len(seg) >= 6 && string(seg[:4]) == "Exif":
			m.applyTIFF(seg[6:])
		case marker == 0xda: // start of scan: no metadata past here
			return m
		}
		p += 2 + n
	}
	return m
}

// EXIF tags read from IFD0.
const (
	tagMake        = 0x010f
	tagModel       = 0x0110
	tagOrientation = 0x0112
	tagXResolution = 0x011a
	tagResUnit     = 0x0128
)

// applyTIFF reads the IFD0 fields of a TIFF header into m. Fields the file already set
// are left alone, so JFIF density still wins over EXIF for the dpi, as it always has.
func (m *Meta) applyTIFF(t []byte) {
	f, ok := parseTIFF(t)
	if !ok {
		return
	}
	if f.orientation >= 1 && f.orientation <= 8 && m.Orientation == 0 {
		m.Orientation = f.orientation
	}
	if f.make != "" && m.Make == "" {
		m.Make = f.make
	}
	if f.model != "" && m.Model == "" {
		m.Model = f.model
	}
	m.Camera = m.Camera || m.Make != "" || m.Model != ""
	if f.xres > 0 && m.DPI == 0 {
		m.DPI = f.xres
	}
}

type tiffFields struct {
	orientation int
	make, model string
	xres        float64 // dots per inch, 0 when absent
}

// parseTIFF reads IFD0 of a TIFF structure: the orientation, the camera names and the
// resolution (XResolution scaled by ResolutionUnit, inch by default).
func parseTIFF(t []byte) (tiffFields, bool) {
	var f tiffFields
	if len(t) < 8 {
		return f, false
	}
	var ord binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		ord = binary.LittleEndian
	case "MM":
		ord = binary.BigEndian
	default:
		return f, false
	}
	off := int(ord.Uint32(t[4:]))
	if off < 0 || off+2 > len(t) {
		return f, false
	}
	count := int(ord.Uint16(t[off:]))

	// value returns the bytes of an entry's value: inline when they fit in the 4-byte
	// field, else at the offset that field holds.
	value := func(e int, size int) []byte {
		if size <= 4 {
			return t[e+8 : e+8+size]
		}
		v := int(ord.Uint32(t[e+8:]))
		if v < 0 || v+size > len(t) {
			return nil
		}
		return t[v : v+size]
	}

	var xres []byte
	unit := 2
	for i := range count {
		e := off + 2 + i*12
		if e < 0 || e+12 > len(t) {
			break
		}
		tag := ord.Uint16(t[e:])
		typ := ord.Uint16(t[e+2:])
		n := int(ord.Uint32(t[e+4:]))
		switch tag {
		case tagOrientation:
			if typ == 3 && n >= 1 {
				f.orientation = int(ord.Uint16(value(e, 2)))
			}
		case tagMake, tagModel:
			if typ == 2 && n > 0 && n < 1<<12 {
				s := strings.Trim(string(value(e, n)), "\x00 ")
				if tag == tagMake {
					f.make = s
				} else {
					f.model = s
				}
			}
		case tagXResolution:
			if typ == 5 && n == 1 {
				xres = value(e, 8)
			}
		case tagResUnit:
			if typ == 3 && n == 1 {
				unit = int(ord.Uint16(value(e, 2)))
			}
		}
	}
	if len(xres) == 8 {
		num, den := ord.Uint32(xres), ord.Uint32(xres[4:])
		if den != 0 {
			f.xres = float64(num) / float64(den)
			if unit == 3 { // centimetres
				f.xres *= 2.54
			}
		}
	}
	return f, true
}
