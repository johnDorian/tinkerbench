package render

import (
	_ "embed"
	"os"
	"path/filepath"
)

// workbenchSCAD is the helper library, see library/workbench.scad.
//
//go:embed library/workbench.scad
var workbenchSCAD []byte

// InstallLibrary writes the OpenSCAD helper library into dir, which Renderer.LibDir puts on
// OPENSCADPATH so that `use <workbench.scad>` resolves in every run. It runs at start-up, so
// the library always matches the binary.
func InstallLibrary(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "workbench.scad"), workbenchSCAD, 0o644)
}
