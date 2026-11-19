package profiles

import (
	"errors"
	"slices"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func TestFlattenMergesInheritance(t *testing.T) {
	lib := Open(testkit.WriteProfiles(t))
	proc, err := lib.Flatten(Process, testkit.Process)
	if err != nil {
		t.Fatal(err)
	}
	// own key wins; inherited key comes through; inherits/from/instantiation are dropped
	if Str(proc, "layer_height") != "0.2" || Str(proc, "sparse_infill_density") != "15%" || Str(proc, "wall_loops") != "2" {
		t.Fatalf("unexpected process settings: %v", proc)
	}
	for _, k := range []string{"inherits", "from", "instantiation"} {
		if Has(proc, k) {
			t.Fatalf("%s should be dropped", k)
		}
	}
	fil, err := lib.Flatten(Filament, testkit.Filament)
	if err != nil {
		t.Fatal(err)
	}
	// two levels deep: filament -> Generic PETG -> fdm_filament_common
	if Str(fil, "nozzle_temperature") != "240" || Str(fil, "filament_diameter") != "1.75" || Str(fil, "filament_type") != "PETG" {
		t.Fatalf("unexpected filament settings: %v", fil)
	}
}

func TestFlattenUnknown(t *testing.T) {
	lib := Open(testkit.WriteProfiles(t))
	if _, err := lib.Flatten(Process, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListCompatible(t *testing.T) {
	lib := Open(testkit.WriteProfiles(t))
	l, err := lib.List(testkit.Machine)
	if err != nil {
		t.Fatal(err)
	}
	var machines []string
	for _, m := range l.Machines {
		machines = append(machines, m.Name)
	}
	if !slices.Contains(machines, testkit.Machine) || !slices.Contains(machines, "Anycubic Kobra 3 Max 0.4 nozzle") {
		t.Fatalf("machines = %v", machines)
	}
	if len(l.Processes) != 1 || l.Processes[0].Name != testkit.Process || l.Processes[0].LayerHeight != 0.2 {
		t.Fatalf("processes = %+v", l.Processes)
	}
	names := []string{}
	for _, f := range l.Filaments {
		names = append(names, f.Name)
	}
	// Generic PLA lists the machine; Elsewhere PLA does not; the Kobra PETG does
	if !slices.Contains(names, testkit.Filament) || !slices.Contains(names, "Generic PLA") || slices.Contains(names, "Elsewhere PLA") {
		t.Fatalf("filaments = %v", names)
	}
}

func TestBed(t *testing.T) {
	lib := Open(testkit.WriteProfiles(t))
	bed, err := lib.Bed(testkit.Machine)
	if err != nil {
		t.Fatal(err)
	}
	if bed.Width != 255 || bed.Depth != 255 || bed.MaxHeight != 260 || len(bed.Shape) != 4 {
		t.Fatalf("bed = %+v", bed)
	}
}

func TestSetKeepsArrayShape(t *testing.T) {
	m := map[string]any{"a": []any{"1"}, "b": "x"}
	Set(m, "a", "2")
	Set(m, "b", "y")
	if !slices.Equal(Strings(m, "a"), []string{"2"}) || Str(m, "b") != "y" {
		t.Fatalf("m = %v", m)
	}
}

func TestRealProfilesWhenAvailable(t *testing.T) {
	_, dir := testkit.OrcaProfiles()
	if dir == "" {
		t.Skip("set ORCA_TEST_BIN to an OrcaSlicer binary to run this")
	}
	lib := Open(dir)
	bed, err := lib.Bed("Anycubic Kobra 3 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	if bed.Width != 255 || bed.MaxHeight != 260 {
		t.Fatalf("Kobra 3 bed = %+v", bed)
	}
	l, err := lib.List("Anycubic Kobra 3 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range l.Processes {
		found = found || p.Name == "0.20mm Standard @Anycubic Kobra 3 0.4 nozzle"
	}
	if !found || len(l.Filaments) == 0 {
		t.Fatalf("compatible presets missing: %d processes, %d filaments", len(l.Processes), len(l.Filaments))
	}
	if _, err := lib.Flatten(Filament, "Anycubic PETG @Anycubic Kobra 3 0.4 nozzle"); err != nil {
		t.Fatal(err)
	}
}
