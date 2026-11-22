package app

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// phonePhoto is a w x h JPEG with an EXIF APP1 segment that says it was taken by a phone, held
// with orientation 6 (a quarter turn clockwise) and recording 72 dpi.
func phonePhoto(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	// TIFF, big-endian: IFD0 at 8 with Orientation (inline), Make, Model and XResolution.
	make_ := []byte("Apple\x00")
	model := []byte("iPhone 16e\x00")
	res := []byte{0, 0, 0, 72, 0, 0, 0, 1}
	const n = 4
	dataAt := 8 + 2 + 12*n + 4
	tiff := make([]byte, dataAt)
	copy(tiff, "MM")
	binary.BigEndian.PutUint16(tiff[2:], 42)
	binary.BigEndian.PutUint32(tiff[4:], 8)
	binary.BigEndian.PutUint16(tiff[8:], n)
	entry := func(i int, tag, typ uint16, count uint32, inline []byte, offset int) {
		at := 10 + 12*i
		binary.BigEndian.PutUint16(tiff[at:], tag)
		binary.BigEndian.PutUint16(tiff[at+2:], typ)
		binary.BigEndian.PutUint32(tiff[at+4:], count)
		if inline != nil {
			copy(tiff[at+8:], inline)
		} else {
			binary.BigEndian.PutUint32(tiff[at+8:], uint32(offset))
		}
	}
	entry(0, 0x0112, 3, 1, []byte{0, 6}, 0)
	entry(1, 0x010f, 2, uint32(len(make_)), nil, dataAt)
	tiff = append(tiff, make_...)
	entry(2, 0x0110, 2, uint32(len(model)), nil, dataAt+len(make_))
	tiff = append(tiff, model...)
	entry(3, 0x011a, 5, 1, nil, len(tiff))
	tiff = append(tiff, res...)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	j := buf.Bytes()
	out := append(append(append([]byte{}, j[:2]...), seg...), payload...)
	return append(out, j[2:]...)
}

// TestCameraPhotoListing: a phone's photo is listed upright (the quarter turn swaps the sides),
// with its camera named and its 72 dpi marked as not usable as a scale.
func TestCameraPhotoListing(t *testing.T) {
	store, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("part", "", "cube(1);\n", "test"); err != nil {
		t.Fatal(err)
	}
	a := New(store, render.New("openscad-not-used", 0, 1), events.NewHub())

	stored, err := a.UploadImage("part", "photo.jpg", phonePhoto(t, 40, 20), "test")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Width != 20 || stored.Height != 40 {
		t.Errorf("upload size %dx%d, want the upright 20x40", stored.Width, stored.Height)
	}
	if stored.Camera != "Apple iPhone 16e" || stored.DPIUsable || stored.DPI != 72 {
		t.Errorf("upload = camera %q usable %v dpi %v", stored.Camera, stored.DPIUsable, stored.DPI)
	}

	list, err := a.Images("part")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Width != 20 || list[0].Height != 40 || list[0].Camera != "Apple iPhone 16e" || list[0].DPIUsable {
		t.Errorf("listing = %+v", list)
	}
}
