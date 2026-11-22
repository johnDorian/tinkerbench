package measure

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// rectsImage is a light page with dark rectangles, each [left, top, right, bottom).
func rectsImage(w, h int, rects ...[4]int) *imaging.RGB {
	img := &imaging.RGB{W: w, H: h, Pix: make([]uint8, w*h*3)}
	for i := range img.Pix {
		img.Pix[i] = 235
	}
	for _, r := range rects {
		for y := r[1]; y < r[3]; y++ {
			for x := r[0]; x < r[2]; x++ {
				i := (y*w + x) * 3
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 25, 25, 25
			}
		}
	}
	return img
}

// decodeOverlay decodes the PNG a measurement drew.
func decodeOverlay(t *testing.T, b []byte) image.Image {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// countColour counts the pixels of an overlay that have exactly colour c. The overlay is drawn
// without anti-aliasing, so a mark keeps its colour.
func countColour(im image.Image, c imaging.Color) int {
	n := 0
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if rgbEq(im, x, y, c) {
				n++
			}
		}
	}
	return n
}

func rgbEq(im image.Image, x, y int, c imaging.Color) bool {
	r, g, bl, _ := im.At(x, y).RGBA()
	return uint8(r>>8) == c.R && uint8(g>>8) == c.G && uint8(bl>>8) == c.B
}

// longestRun is the longest horizontal run of colour c in any row of the overlay.
func longestRun(im image.Image, c imaging.Color) int {
	best := 0
	b := im.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		run := 0
		for x := b.Min.X; x < b.Max.X; x++ {
			if rgbEq(im, x, y, c) {
				run++
				best = max(best, run)
			} else {
				run = 0
			}
		}
	}
	return best
}

// TestTwoObjects measures the larger of two dark blobs and reports the other one.
func TestTwoObjects(t *testing.T) {
	// The part is 200x100 px, the other object 100x60 px (30% of it), both well inside the frame.
	img := rectsImage(600, 400, [4]int{50, 150, 250, 250}, [4]int{400, 200, 500, 260})
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Overlay: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ObjectsFound != 2 {
		t.Fatalf("objects_found = %d, want 2", res.ObjectsFound)
	}
	if len(res.OtherObjects) != 1 {
		t.Fatalf("other_objects = %+v, want one", res.OtherObjects)
	}
	if got := res.OtherObjects[0].BoxPx; len(got) != 4 || got[0] != 400 || got[1] != 200 || got[2] != 500 || got[3] != 260 {
		t.Errorf("other object box = %v, want [400 200 500 260]", got)
	}
	close(t, "measured length", res.OverallLengthMM, 200*25.4/600, 0.05)

	var note string
	for _, n := range res.Notes {
		if strings.Contains(n, "objects found") {
			note = n
		}
	}
	if !strings.HasPrefix(note, "2 objects found; measured the largest") {
		t.Errorf("note = %q, want the objects note", note)
	}

	if countColour(decodeOverlay(t, res.Overlay), colOther) == 0 {
		t.Error("the other object is not marked in blue on the overlay")
	}
}

// TestSpeckIsNotAnObject keeps a blob that is a small share of the part out of the count.
func TestSpeckIsNotAnObject(t *testing.T) {
	img := rectsImage(600, 400, [4]int{50, 150, 250, 250}, [4]int{400, 200, 420, 210})
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ObjectsFound != 1 || len(res.OtherObjects) != 0 {
		t.Errorf("objects_found = %d, other_objects = %+v; want 1 and none", res.ObjectsFound, res.OtherObjects)
	}
}

// TestSmallPartIsCropped zooms the overlay on a small part in a large photo.
func TestSmallPartIsCropped(t *testing.T) {
	img := rectsImage(2000, 1500, [4]int{1000, 700, 1100, 760})
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Overlay: true})
	if err != nil {
		t.Fatal(err)
	}
	var cropped bool
	for _, n := range res.Notes {
		cropped = cropped || strings.Contains(n, "overlay is cropped to the part")
	}
	if !cropped {
		t.Errorf("no note about the crop: %q", res.Notes)
	}
	// The part is 100x60 px, so the view is the part plus a 25 px margin: 150x110 px. At the
	// default 1200 px long edge that is 1200x880, where the whole photo would be 1200x900 and the
	// part a few dozen pixels.
	im := decodeOverlay(t, res.Overlay)
	if b := im.Bounds(); b.Dx() != 1200 || b.Dy() != 880 {
		t.Errorf("overlay is %dx%d, want 1200x880 (the cropped view)", b.Dx(), b.Dy())
	}
	if countColour(im, colOutline) == 0 {
		t.Error("the outline is not on the overlay")
	}
}

// TestScaleBarInCrop checks that the scale bar in the zoomed overlay is the length its label
// says. At this zoom 10 mm is wider than the overlay, so the bar falls back to 5 mm.
func TestScaleBarInCrop(t *testing.T) {
	img := rectsImage(2000, 1500, [4]int{1000, 700, 1100, 760})
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Overlay: true})
	if err != nil {
		t.Fatal(err)
	}
	// The view is 150 px wide and the overlay 1200 px, so the zoom is 8 overlay px per image px.
	pxPerMM := 8 / res.ScaleMMPerPx
	want := int(math.Round(5 * pxPerMM))
	if got := longestRun(decodeOverlay(t, res.Overlay), colLabel); got < want-2 || got > want+2 {
		t.Errorf("scale bar is %d px long, want %d px for 5 mm at %.1f px/mm", got, want, pxPerMM)
	}
}

// TestZoomWithFarObject zooms on a small part when another object lies far away: the other
// object is in the corner inset, not in the zoomed view, and the zoom still happens.
func TestZoomWithFarObject(t *testing.T) {
	img := rectsImage(2000, 1500, [4]int{200, 200, 300, 260}, [4]int{1600, 1100, 1700, 1160})
	res, err := Measure(img, imaging.Meta{DPI: 600}, Options{Overlay: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ObjectsFound != 2 {
		t.Fatalf("objects_found = %d, want 2", res.ObjectsFound)
	}
	var cropped bool
	for _, n := range res.Notes {
		cropped = cropped || strings.Contains(n, "overlay is cropped to the part")
	}
	if !cropped {
		t.Errorf("the overlay was not zoomed on the part: %q", res.Notes)
	}
	im := decodeOverlay(t, res.Overlay)
	if b := im.Bounds(); b.Dx() != 1200 || b.Dy() != 880 {
		t.Errorf("overlay is %dx%d, want 1200x880 (the zoomed view)", b.Dx(), b.Dy())
	}
	if countColour(im, colOther) == 0 {
		t.Error("the other object is not marked on the overlay (in the inset)")
	}
	if countColour(im, colTip) == 0 {
		t.Error("the inset does not show the crop in orange")
	}
}

// TestOverlayView covers the crop window: its margin, clamping at the edge, and the whole photo
// when the part fills it.
func TestOverlayView(t *testing.T) {
	cases := []struct {
		name string
		box  []int
		w, h int
		want []int // nil for the whole photo
	}{
		{"small part, margin", []int{1000, 700, 1100, 760}, 2000, 1500, []int{975, 675, 1125, 785}},
		{"clamped at the corner", []int{5, 5, 50, 30}, 2000, 1500, []int{0, 0, 74, 54}},
		{"part fills the photo", []int{100, 100, 900, 700}, 1000, 800, nil},
	}
	for _, c := range cases {
		got := overlayView(c.box, c.w, c.h)
		if len(got) != len(c.want) {
			t.Errorf("%s: view = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: view = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
