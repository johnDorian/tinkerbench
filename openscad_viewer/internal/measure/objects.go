package measure

import (
	"fmt"
	"math"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
)

// A blob other than the largest counts as another object when it is a real share of the
// measured one and of the photo. The share of the largest is what makes a speck of paper or a
// fleck of dust fall out; the floor on the photo is the same one the measured part has to
// clear, so both are judged by one standard.
const (
	minOtherShare = 0.15  // of the largest blob's area
	minPartShare  = 0.001 // of the image's area
)

// Object is another part-sized blob in the photo that was not measured. The box is in
// full-image pixels, so a crop can be taken round it.
type Object struct {
	BoxPx   []int   `json:"box_px"`   // [left, top, right, bottom], exclusive on the right and bottom
	AreaPx  int     `json:"area_px"`  // set pixels
	AreaMM2 float64 `json:"area_mm2"` // at the photo's scale, where the part is measured
}

// plausibleOthers keeps the blobs after the largest that could be parts of their own. rest
// is in the order Blobs returns them (largest first); largest is the measured blob's area and
// pixels is the area of the image the blobs were found in.
func plausibleOthers(rest []imaging.Blob, largest, pixels int) []imaging.Blob {
	var out []imaging.Blob
	for _, b := range rest {
		if float64(b.Area) < minOtherShare*float64(largest) || float64(b.Area) < minPartShare*float64(pixels) {
			continue
		}
		if len(b.Outline) < 12 {
			continue
		}
		out = append(out, b)
	}
	return out
}

// otherObjects reports the blobs that were not measured. ox and oy are the crop's origin, so
// the boxes are in full-image pixels; mmPerPx is the photo's scale, for the areas.
func otherObjects(others []imaging.Blob, ox, oy int, mmPerPx float64) []Object {
	out := make([]Object, 0, len(others))
	for _, b := range others {
		out = append(out, Object{
			BoxPx:   shiftBox(blobBox(b), ox, oy),
			AreaPx:  b.Area,
			AreaMM2: round2(float64(b.Area) * mmPerPx * mmPerPx),
		})
	}
	return out
}

// objectsNote says what was found and what was measured, in words an agent can act on.
func objectsNote(n int, measured []int) string {
	return fmt.Sprintf("%d objects found; measured the largest (box [%d, %d, %d, %d]). The others are marked in blue on the overlay; pass crop=[l,t,r,b] to measure one of them.",
		n, measured[0], measured[1], measured[2], measured[3])
}

// outlineBox is the bounding box [left, top, right, bottom) of a traced outline, in the
// pixels it was traced in.
func outlineBox(p points) []int {
	return []int{
		int(minOf(p.X)), int(minOf(p.Y)),
		int(maxOf(p.X)) + 1, int(maxOf(p.Y)) + 1,
	}
}

// blobBox is the bounding box [left, top, right, bottom) of a blob's outline, in the pixels
// it was found in.
func blobBox(b imaging.Blob) []int {
	l, t, r, bt := math.MaxInt, math.MaxInt, math.MinInt, math.MinInt
	for _, q := range b.Outline {
		l, t = min(l, q.X), min(t, q.Y)
		r, bt = max(r, q.X+1), max(bt, q.Y+1)
	}
	return []int{l, t, r, bt}
}

// shiftBox moves a box by a crop's origin, to full-image pixels.
func shiftBox(b []int, dx, dy int) []int {
	return []int{b[0] + dx, b[1] + dy, b[2] + dx, b[3] + dy}
}
