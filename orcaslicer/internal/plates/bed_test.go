package plates

import "testing"

func TestEffectiveBed(t *testing.T) {
	single := map[string]any{"support_multi_bed_types": "0"}
	multi := map[string]any{"support_multi_bed_types": "1"}
	for _, c := range []struct {
		machine map[string]any
		bed     string
		want    string
	}{
		{single, "", SingleBed},
		{single, "Cool Plate", SingleBed},           // the printer has one bed: the plate's choice is ignored
		{map[string]any{}, "Cool Plate", SingleBed}, // key missing counts as off
		{multi, "Textured PEI Plate", "Textured PEI Plate"},
		{multi, "", SingleBed},
		{multi, "Glass", SingleBed},
	} {
		if got := EffectiveBed(c.machine, c.bed); got != c.want {
			t.Errorf("EffectiveBed(%v, %q) = %q, want %q", c.machine, c.bed, got, c.want)
		}
	}
}
