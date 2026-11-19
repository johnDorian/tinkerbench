package plates

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
)

// ErrNoRoom is returned by Arrange when the objects do not fit on the bed.
var ErrNoRoom = errors.New("the objects do not fit on the bed")

const tolerance = 0.01 // mm, for "touching the edge" and "overlapping"

// PlaceObject returns the transform of an object and its placed box, from its model's triangles.
func PlaceObject(o Object, tris []geom.Triangle) (geom.Transform, geom.Box) {
	return geom.Place(tris, geom.Pose{Rotation: o.Rotation, Scale: o.Scale}, o.X, o.Y)
}

// Boxes returns the placed box of every object, by object id.
func Boxes(p Plate, store *models.Store) (map[string]geom.Box, error) {
	out := make(map[string]geom.Box, len(p.Objects))
	for _, o := range p.Objects {
		tris, _, err := store.Mesh(o.Model)
		if err != nil {
			return nil, fmt.Errorf("object %s: %w", o.ID, err)
		}
		_, box := PlaceObject(o, tris)
		out[o.ID] = box
	}
	return out, nil
}

// Footprint returns the size of a model after rotation and scale (its position does not matter).
func Footprint(tris []geom.Triangle, rotation [3]float64, scale float64) geom.Box {
	_, box := geom.Place(tris, geom.Pose{Rotation: rotation, Scale: scale}, 0, 0)
	return box
}

// AutoPlace finds a free spot for a footprint of w×d mm: the free position nearest the middle of
// the bed, so new objects land in the middle and fill outwards. Without room it returns the centre.
func AutoPlace(bed profiles.Bed, placed map[string]geom.Box, w, d float64) (float64, float64) {
	const step, gap = 5.0, 3.0
	cx, cy := bed.Width/2, bed.Depth/2
	type cand struct{ x, y, dist float64 }
	var cands []cand
	for x := w / 2; x <= bed.Width-w/2+tolerance; x += step {
		for y := d / 2; y <= bed.Depth-d/2+tolerance; y += step {
			cands = append(cands, cand{x, y, math.Hypot(x-cx, y-cy)})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	for _, c := range cands {
		if !overlapsAny(c.x, c.y, w, d, gap, placed) {
			return c.x, c.y
		}
	}
	return cx, cy
}

func overlapsAny(x, y, w, d, gap float64, placed map[string]geom.Box) bool {
	for _, b := range placed {
		ex, ey := (b.Min[0]+b.Max[0])/2, (b.Min[1]+b.Max[1])/2
		sz := b.Size()
		if math.Abs(x-ex) < (w+sz[0])/2+gap && math.Abs(y-ey) < (d+sz[1])/2+gap {
			return true
		}
	}
	return false
}

// Item is one object's footprint for Arrange.
type Item struct {
	ID   string
	W, D float64
}

// Arrange packs footprints onto the bed in shelves (deepest first), spaced by spacing mm and
// centred on the bed. It returns each object's centre by id, or ErrNoRoom.
func Arrange(bed profiles.Bed, items []Item, spacing float64) (map[string][2]float64, error) {
	if spacing < 0 {
		spacing = 0
	}
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].D != sorted[j].D {
			return sorted[i].D > sorted[j].D
		}
		return sorted[i].W > sorted[j].W
	})
	m := spacing
	out := map[string][2]float64{}
	x, y, rowH := m, m, 0.0
	maxRight, maxTop := 0.0, 0.0
	for _, it := range sorted {
		if it.W > bed.Width-2*m+tolerance || it.D > bed.Depth-2*m+tolerance {
			return nil, ErrNoRoom
		}
		if x > m && x+it.W > bed.Width-m+tolerance {
			x = m
			y += rowH + spacing
			rowH = 0
		}
		if y+it.D > bed.Depth-m+tolerance {
			return nil, ErrNoRoom
		}
		out[it.ID] = [2]float64{x + it.W/2, y + it.D/2}
		x += it.W + spacing
		rowH = math.Max(rowH, it.D)
		maxRight = math.Max(maxRight, x-spacing)
		maxTop = math.Max(maxTop, y+rowH)
	}
	// centre the block of shelves on the bed
	dx := (bed.Width-(maxRight-m))/2 - m
	dy := (bed.Depth-(maxTop-m))/2 - m
	for id, c := range out {
		out[id] = [2]float64{c[0] + dx, c[1] + dy}
	}
	return out, nil
}

// Check lists what stops or worries a slice. Objects off the bed or above the printable height
// are blocking: OrcaSlicer refuses to slice them. Overlapping objects are only a warning.
// Objects are reported in plate order.
func Check(bed profiles.Bed, objects []Object, boxes map[string]geom.Box) (warnings []string, blocking bool) {
	for _, o := range objects {
		b, ok := boxes[o.ID]
		if !ok {
			continue
		}
		if b.Min[0] < -tolerance || b.Min[1] < -tolerance || b.Max[0] > bed.Width+tolerance || b.Max[1] > bed.Depth+tolerance {
			warnings = append(warnings, fmt.Sprintf("object %s outside bed", o.ID))
			blocking = true
		}
		if b.Max[2] > bed.MaxHeight+tolerance {
			warnings = append(warnings, fmt.Sprintf("object %s taller than the printer (%g mm)", o.ID, bed.MaxHeight))
			blocking = true
		}
	}
	for i, a := range objects {
		for _, c := range objects[i+1:] {
			ba, okA := boxes[a.ID]
			bc, okC := boxes[c.ID]
			if !okA || !okC {
				continue
			}
			if math.Min(ba.Max[0], bc.Max[0])-math.Max(ba.Min[0], bc.Min[0]) > tolerance &&
				math.Min(ba.Max[1], bc.Max[1])-math.Max(ba.Min[1], bc.Min[1]) > tolerance {
				warnings = append(warnings, fmt.Sprintf("objects %s and %s overlap", a.ID, c.ID))
			}
		}
	}
	return warnings, blocking
}
