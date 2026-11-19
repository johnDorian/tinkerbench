// Package testkit has fixtures shared by the tests: a small OrcaSlicer-shaped profile tree and
// STL generators. Nothing here touches the network or a real printer.
package testkit

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Machine is the name of the synthetic machine preset (a Kobra 3 0.4 look-alike).
const (
	Machine  = "Anycubic Kobra 3 0.4 nozzle"
	Process  = "0.20mm Standard @Anycubic Kobra 3 0.4 nozzle"
	Filament = "Anycubic PETG @Anycubic Kobra 3 0.4 nozzle"
)

// WriteProfiles writes a synthetic resources/profiles tree with inheritance chains like OrcaSlicer's
// and returns its root.
func WriteProfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(vendor, kind, name string, v map[string]any) {
		dir := filepath.Join(root, vendor, kind)
		os.MkdirAll(dir, 0o755)
		v["name"] = name
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Anycubic", "machine", "fdm_machine_common", map[string]any{"type": "machine"})
	write("Anycubic", "machine", Machine, map[string]any{
		"type": "machine", "inherits": "fdm_machine_common", "from": "system", "instantiation": "true",
		"printer_model": "Anycubic Kobra 3", "printable_area": []string{"0x0", "255x0", "255x255", "0x255"},
		"printable_height": "260", "nozzle_diameter": []string{"0.4"},
	})
	write("Anycubic", "machine", "Anycubic Kobra 3 Max 0.4 nozzle", map[string]any{
		"type": "machine", "instantiation": "true", "printer_model": "Anycubic Kobra 3 Max",
		"printable_area": []string{"0x0", "300x0", "300x300", "0x300"}, "printable_height": "300",
	})
	write("Anycubic", "process", "fdm_process_common", map[string]any{"type": "process", "layer_height": "0.2", "sparse_infill_density": "15%", "wall_loops": "2"})
	write("Anycubic", "process", Process, map[string]any{
		"type": "process", "inherits": "fdm_process_common", "from": "system", "instantiation": "true",
		"compatible_printers": []string{Machine}, "layer_height": "0.2", "brim_type": "auto_brim",
		"enable_support": "0", "support_type": "normal(auto)",
	})
	write("Anycubic", "process", "0.20mm Standard @Anycubic Kobra 3 Max 0.4 nozzle", map[string]any{
		"type": "process", "inherits": "fdm_process_common", "instantiation": "true",
		"compatible_printers": []string{"Anycubic Kobra 3 Max 0.4 nozzle"},
	})
	write("Anycubic", "filament", "fdm_filament_common", map[string]any{"type": "filament", "filament_diameter": []string{"1.75"}})
	write("Anycubic", "filament", "Anycubic Generic PETG", map[string]any{
		"type": "filament", "inherits": "fdm_filament_common", "filament_type": []string{"PETG"}, "nozzle_temperature": []string{"240"},
		"hot_plate_temp": []string{"70"},
	})
	write("Anycubic", "filament", Filament, map[string]any{
		"type": "filament", "inherits": "Anycubic Generic PETG", "from": "system", "instantiation": "true",
		"filament_type": []string{"PETG"}, "filament_vendor": []string{"Anycubic"},
		"compatible_printers": []string{Machine},
	})
	write("OrcaFilamentLibrary", "filament", "Generic PLA", map[string]any{
		"type": "filament", "instantiation": "true", "filament_type": []string{"PLA"}, "filament_vendor": []string{"Generic"},
		"compatible_printers": []string{Machine, "Some Other Printer"},
	})
	write("OrcaFilamentLibrary", "filament", "Elsewhere PLA", map[string]any{
		"type": "filament", "instantiation": "true", "filament_type": []string{"PLA"}, "compatible_printers": []string{"Other"},
	})
	return root
}

// Cube returns a binary STL of an axis-aligned cube whose lowest corner is at min.
func Cube(min [3]float64, size float64) []byte {
	var tris [][3][3]float32
	x0, y0, z0 := min[0], min[1], min[2]
	x1, y1, z1 := x0+size, y0+size, z0+size
	p := func(x, y, z float64) [3]float32 { return [3]float32{float32(x), float32(y), float32(z)} }
	// six faces, two triangles each
	faces := [][4][3]float32{
		{p(x0, y0, z0), p(x1, y0, z0), p(x1, y1, z0), p(x0, y1, z0)},
		{p(x0, y0, z1), p(x1, y0, z1), p(x1, y1, z1), p(x0, y1, z1)},
		{p(x0, y0, z0), p(x1, y0, z0), p(x1, y0, z1), p(x0, y0, z1)},
		{p(x0, y1, z0), p(x1, y1, z0), p(x1, y1, z1), p(x0, y1, z1)},
		{p(x0, y0, z0), p(x0, y1, z0), p(x0, y1, z1), p(x0, y0, z1)},
		{p(x1, y0, z0), p(x1, y1, z0), p(x1, y1, z1), p(x1, y0, z1)},
	}
	for _, f := range faces {
		tris = append(tris, [3][3]float32{f[0], f[1], f[2]}, [3][3]float32{f[0], f[2], f[3]})
	}
	var buf bytes.Buffer
	buf.Write(make([]byte, 80))
	binary.Write(&buf, binary.LittleEndian, uint32(len(tris)))
	for _, t := range tris {
		binary.Write(&buf, binary.LittleEndian, [3]float32{})
		for _, v := range t {
			binary.Write(&buf, binary.LittleEndian, v)
		}
		binary.Write(&buf, binary.LittleEndian, uint16(0))
	}
	return buf.Bytes()
}

// ASCIICube is the same cube as an ASCII STL.
func ASCIICube(min [3]float64, size float64) []byte {
	bin := Cube(min, size)
	n := int(binary.LittleEndian.Uint32(bin[80:84]))
	var buf bytes.Buffer
	buf.WriteString("solid cube\n")
	for i := 0; i < n; i++ {
		off := 84 + 50*i + 12
		buf.WriteString("  facet normal 0 0 0\n    outer loop\n")
		for v := 0; v < 3; v++ {
			x := math.Float32frombits(binary.LittleEndian.Uint32(bin[off:]))
			y := math.Float32frombits(binary.LittleEndian.Uint32(bin[off+4:]))
			z := math.Float32frombits(binary.LittleEndian.Uint32(bin[off+8:]))
			fmt.Fprintf(&buf, "      vertex %g %g %g\n", x, y, z)
			off += 12
		}
		buf.WriteString("    endloop\n  endfacet\n")
	}
	buf.WriteString("endsolid cube\n")
	return buf.Bytes()
}

// OrcaProfiles returns the real OrcaSlicer resources/profiles dir next to the binary named by
// ORCA_TEST_BIN, or "" if that isn't set.
func OrcaProfiles() (bin, profiles string) {
	bin = os.Getenv("ORCA_TEST_BIN")
	if bin == "" {
		return "", ""
	}
	dir := filepath.Dir(bin)
	for _, c := range []string{filepath.Join(dir, "resources", "profiles"), filepath.Join(dir, "..", "resources", "profiles")} {
		if _, err := os.Stat(c); err == nil {
			return bin, c
		}
	}
	return bin, ""
}
