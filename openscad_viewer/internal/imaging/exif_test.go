package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// tiffEntry is one IFD0 entry: its tag, type and the value bytes, which are stored inline when
// they fit in four bytes and otherwise after the directory.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	value    []byte
}

// buildTIFF lays out a big-endian TIFF structure with the given IFD0 entries, the way an
// EXIF APP1 segment carries them (the "Exif\0\0" prefix is added by the caller).
func buildTIFF(entries []tiffEntry) []byte {
	const ifdOff = 8
	dirLen := 2 + 12*len(entries) + 4
	data := []byte{}
	out := make([]byte, ifdOff+dirLen)
	copy(out, "MM")
	binary.BigEndian.PutUint16(out[2:], 42)
	binary.BigEndian.PutUint32(out[4:], ifdOff)
	binary.BigEndian.PutUint16(out[ifdOff:], uint16(len(entries)))
	for i, e := range entries {
		at := ifdOff + 2 + 12*i
		binary.BigEndian.PutUint16(out[at:], e.tag)
		binary.BigEndian.PutUint16(out[at+2:], e.typ)
		binary.BigEndian.PutUint32(out[at+4:], e.count)
		if len(e.value) <= 4 {
			copy(out[at+8:at+12], e.value)
			continue
		}
		off := len(out) + len(data)
		binary.BigEndian.PutUint32(out[at+8:], uint32(off))
		data = append(data, e.value...)
	}
	return append(out, data...)
}

// exifSegment is an APP1 segment carrying the TIFF structure.
func exifSegment(tiff []byte) []byte {
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// asciiEntry is an ASCII tag with its terminating NUL.
func asciiEntry(tag uint16, s string) tiffEntry {
	v := append([]byte(s), 0)
	return tiffEntry{tag: tag, typ: 2, count: uint32(len(v)), value: v}
}

// phoneEntries is what a phone writes to IFD0: orientation, camera names and a 72 dpi
// resolution in inches.
func phoneEntries(orientation uint16) []tiffEntry {
	orient := make([]byte, 4)
	binary.BigEndian.PutUint16(orient, orientation)
	res := make([]byte, 8)
	binary.BigEndian.PutUint32(res[0:], 72)
	binary.BigEndian.PutUint32(res[4:], 1)
	unit := make([]byte, 4)
	binary.BigEndian.PutUint16(unit, 2) // inches
	return []tiffEntry{
		{tag: tagOrientation, typ: 3, count: 1, value: orient},
		asciiEntry(tagMake, "Apple"),
		asciiEntry(tagModel, "iPhone 16e"),
		{tag: tagXResolution, typ: 5, count: 1, value: res},
		{tag: tagResUnit, typ: 3, count: 1, value: unit},
	}
}

// withSegment inserts an APP segment right after the JPEG's start-of-image marker.
func withSegment(jpg, seg []byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// encodeTwoTone is a w x h JPEG, red on the left half and blue on the right.
func encodeTwoTone(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 230, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 230, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestOrientationAndCamera checks the EXIF fields of a phone photo: the orientation turns the
// pixels upright (6 is a quarter turn clockwise, so a 40 x 20 file comes out 20 x 40), and the
// camera's 72 dpi is recorded but is not a usable scale.
func TestOrientationAndCamera(t *testing.T) {
	raw := encodeTwoTone(t, 40, 20)
	b := withSegment(raw, exifSegment(buildTIFF(phoneEntries(6))))

	meta, err := ReadMeta(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Orientation != 6 {
		t.Errorf("orientation = %d, want 6", meta.Orientation)
	}
	if meta.Make != "Apple" || meta.Model != "iPhone 16e" || !meta.Camera {
		t.Errorf("camera = %q %q camera=%v", meta.Make, meta.Model, meta.Camera)
	}
	if meta.CameraName() != "Apple iPhone 16e" {
		t.Errorf("CameraName = %q, want %q", meta.CameraName(), "Apple iPhone 16e")
	}
	if meta.DPI != 72 {
		t.Errorf("dpi = %v, want 72", meta.DPI)
	}
	if meta.DPIUsable() {
		t.Error("a camera's dpi must not be usable as a scale")
	}

	img, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if img.W != 20 || img.H != 40 {
		t.Fatalf("upright size %dx%d, want 20x40", img.W, img.H)
	}
	// The red half of the source (x < 20) is the top of the upright image after the quarter
	// turn: source (5, 10) lands at (19-10, 5) = (9, 5), and blue (30, 10) at (9, 30).
	if r, _, bl := img.At(9, 5); r < 150 || bl > 100 {
		t.Errorf("pixel (9,5) = red %d blue %d, want red", r, bl)
	}
	if r, _, bl := img.At(9, 30); bl < 150 || r > 100 {
		t.Errorf("pixel (9,30) = red %d blue %d, want blue", r, bl)
	}
}

// TestScannerDPIIsUsable is a scanner's JFIF density with no camera in the file.
func TestScannerDPIIsUsable(t *testing.T) {
	raw := encodeTwoTone(t, 16, 16)
	// JFIF APP0: "JFIF\0", version 1.01, units 1 (dots per inch), density 600 x 600, no thumbnail.
	app0 := []byte{0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 1, 0x02, 0x58, 0x02, 0x58, 0, 0}
	b := withSegment(raw, app0)
	meta, err := ReadMeta(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if meta.DPI != 600 || meta.Camera || !meta.DPIUsable() {
		t.Errorf("scanner meta = %+v, want 600 dpi usable", meta)
	}
	if meta.CameraName() != "" {
		t.Errorf("CameraName = %q, want empty", meta.CameraName())
	}
}

// TestOrientationsAreBijections checks that each of the eight orientations maps the source
// onto every pixel of the upright image exactly once, with the swapped size for the quarter
// turns.
func TestOrientationsAreBijections(t *testing.T) {
	const w, h = 7, 4
	for o := 1; o <= 8; o++ {
		W, H := orientedSize(w, h, o)
		seen := make(map[[2]int]bool)
		for y := range h {
			for x := range w {
				dx, dy := orientedIndex(o, w, h, x, y)
				if dx < 0 || dy < 0 || dx >= W || dy >= H {
					t.Fatalf("orientation %d: (%d,%d) maps outside %dx%d to (%d,%d)", o, x, y, W, H, dx, dy)
				}
				seen[[2]int{dx, dy}] = true
			}
		}
		if len(seen) != w*h {
			t.Errorf("orientation %d: %d distinct pixels, want %d", o, len(seen), w*h)
		}
	}
}

// TestPNGExifOrientation reads the same metadata from a PNG eXIf chunk.
func TestPNGExifOrientation(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}
	png := buf.Bytes()
	// Insert an eXIf chunk after the IHDR (8 byte signature, 25 byte IHDR chunk).
	tiff := buildTIFF(phoneEntries(8))
	chunk := make([]byte, 8+len(tiff)+4)
	binary.BigEndian.PutUint32(chunk, uint32(len(tiff)))
	copy(chunk[4:], "eXIf")
	copy(chunk[8:], tiff)
	out := append(append(append([]byte{}, png[:33]...), chunk...), png[33:]...)
	meta := HeadMeta(out)
	if meta.Orientation != 8 || !meta.Camera {
		t.Errorf("PNG eXIf meta = %+v, want orientation 8 and a camera", meta)
	}
}
