// Package runner turns a project into SVGs by running Fritzing as a subprocess:
//
//	Fritzing -svg <folder>
//
// loads every .fzz in the folder and writes <name>_breadboard.svg, <name>_schematic.svg and
// <name>_pcb.svg next to it. Fritzing is a Qt GUI program, so it runs with the offscreen platform
// plugin (no X server) and a private HOME that holds its parts library and settings.
package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
)

// Runner runs Fritzing and rsvg-convert. Only one Fritzing runs at a time: it keeps its settings,
// autosave backups and a part cache in HOME, and an old Mac mini has no CPU to spare for two Qt apps.
type Runner struct {
	Bin     string // the Fritzing executable (not Debian's wrapper script, which drops its arguments)
	Home    string // HOME for Fritzing, with .local/lib/fritzing/fritzing-parts below it
	Rsvg    string // rsvg-convert, for PNGs
	Timeout time.Duration

	mu sync.Mutex
}

// Result is the outcome of one render.
type Result struct {
	OK       bool      `json:"ok"`
	Views    []string  `json:"views"` // the views that were written to out/
	Errors   []string  `json:"errors"`
	Warnings []string  `json:"warnings"`
	Duration float64   `json:"duration_s"`
	Log      string    `json:"log,omitempty"` // tail of Fritzing's output, when it failed
	Time     time.Time `json:"time"`
}

// OutDir is where a project's renders are kept.
func OutDir(projectDir string) string { return filepath.Join(projectDir, "out") }

// SVGPath is the rendered SVG of a view.
func SVGPath(projectDir, view string) string { return filepath.Join(OutDir(projectDir), view+".svg") }

// FZZ packs the project's files into an .fzz (a zip): sketch.fz plus any custom parts.
func FZZ(files []project.File) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.Content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Render runs Fritzing on the files and stores the SVGs below projectDir/out. A failed render
// keeps the previous SVGs, so the viewer shows the last good state.
func (r *Runner) Render(ctx context.Context, projectDir string, files []project.File, cat *parts.Catalog) (Result, error) {
	res := Result{Errors: []string{}, Warnings: []string{}, Views: []string{}}
	// Cheap checks first: a missing part would otherwise open a dialog nobody can click.
	if msgs := checkParts(files, cat); len(msgs) > 0 {
		res.Errors = msgs
		res.Time = time.Now()
		return res, r.save(projectDir, res)
	}
	fzz, err := FZZ(files)
	if err != nil {
		return res, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	start := time.Now()
	work, err := os.MkdirTemp("", "fritzing-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(work)
	if err := os.WriteFile(filepath.Join(work, "sketch.fzz"), fzz, 0o644); err != nil {
		return res, err
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, r.Bin, "-svg", work)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "HOME="+r.Home, "QT_QPA_PLATFORM=offscreen", "XDG_RUNTIME_DIR="+work)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	res.Duration = time.Since(start).Seconds()
	res.Time = time.Now()
	log := out.String()

	for _, v := range parts.Views {
		b, err := os.ReadFile(filepath.Join(work, "sketch_"+v+".svg"))
		if err != nil || !bytes.Contains(b, []byte("<svg")) {
			continue
		}
		if err := os.MkdirAll(OutDir(projectDir), 0o755); err != nil {
			return res, err
		}
		if err := writeAtomic(SVGPath(projectDir, v), b); err != nil {
			return res, err
		}
		res.Views = append(res.Views, v)
	}
	res.Errors, res.Warnings = scanLog(log, sketchModules(files))
	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		res.Errors = append(res.Errors, fmt.Sprintf("Fritzing did not finish within %s", timeout))
	case runErr != nil:
		res.Errors = append(res.Errors, "Fritzing failed: "+runErr.Error())
	case len(res.Views) == 0:
		res.Errors = append(res.Errors, "Fritzing wrote no SVGs; is sketch.fz a valid sketch?")
	}
	res.OK = len(res.Errors) == 0
	if !res.OK {
		res.Log = tail(log, 40)
	}
	return res, r.save(projectDir, res)
}

// checkParts reports instances whose part is neither in the library nor in the project.
func checkParts(files []project.File, cat *parts.Catalog) []string {
	if cat == nil {
		return nil
	}
	own := map[string]bool{}
	var fz string
	for _, f := range files {
		switch {
		case strings.HasSuffix(f.Name, ".fzp"):
			if p, err := parts.ParseFZP([]byte(f.Content)); err == nil {
				own[p.ModuleID] = true
			}
		case f.Name == project.MainFile:
			fz = f.Content
		}
	}
	var msgs []string
	for _, id := range moduleRefs(fz) {
		if own[id] || builtin(id) {
			continue
		}
		if _, err := cat.Get(id); err != nil {
			msgs = append(msgs, fmt.Sprintf("unknown part %q: not in the parts library or in this project (search_parts to find the right module id)", id))
		}
	}
	return msgs
}

// Fritzing generates some parts itself instead of reading them from the library.
var builtins = map[string]bool{
	"WireModuleID": true, "LogoTextModuleID": true, "LogoImageModuleID": true, "newLogoImageModuleID": true,
	"Copper0LogoTextModuleID": true, "Copper1LogoTextModuleID": true, "Copper0LogoImageModuleID": true,
	"Copper1LogoImageModuleID": true, "NoteModuleID": true, "SchematicFrameModuleID": true,
	"GroundPlaneModuleID": true, "Copper1GroundPlaneModuleID": true, "JumperModuleID": true,
	"ViaModuleID": true, "HoleModuleID": true, "PadModuleID": true, "CopperBlockerModuleID": true,
	"Copper1BlockerModuleID": true, "TwoLayerRectanglePCBModuleID": true, "RectanglePCBModuleID": true,
	"PerfboardModuleID": true, "StripboardModuleID": true, "Stripboard2ModuleID": true,
}

func builtin(id string) bool { return builtins[id] }

// moduleRefs returns the moduleIdRef of every <instance> without parsing the whole sketch again.
func moduleRefs(fz string) []string {
	var out []string
	seen := map[string]bool{}
	for rest := fz; ; {
		i := strings.Index(rest, "<instance ")
		if i < 0 {
			return out
		}
		rest = rest[i+len("<instance "):]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return out
		}
		tag := rest[:end]
		if j := strings.Index(tag, `moduleIdRef="`); j >= 0 {
			v := tag[j+len(`moduleIdRef="`):]
			if k := strings.IndexByte(v, '"'); k >= 0 && !seen[v[:k]] {
				seen[v[:k]] = true
				out = append(out, v[:k])
			}
		}
	}
}

// scanLog pulls problems out of Fritzing's chatter. Most of what it prints (svgidlayer fail, adding
// layer kin, libpng iCCP warnings) is normal, so only lines that read like real trouble count.
func scanLog(log string, modules []string) (errs, warns []string) {
	errs, warns = []string{}, []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(log, "\n") {
		l := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), `"`))
		low := strings.ToLower(l)
		if l == "" || seen[l] || strings.Contains(low, "svgidlayer") || strings.Contains(low, "libpng") ||
			strings.Contains(low, "adding layer") || strings.Contains(low, "xdg_runtime_dir") {
			continue
		}
		if strings.Contains(low, "module id") && !mentions(l, modules) {
			continue // Fritzing grumbles about library parts it has no database row for; not our sketch
		}
		switch {
		case strings.Contains(low, "missing part"), strings.Contains(low, "could not load"), strings.Contains(low, "cannot load"),
			strings.Contains(low, "unable to"), strings.Contains(low, "unknown module"), strings.Contains(low, "not found"),
			strings.Contains(low, "missing expected terminal"): // an imported part whose artwork lacks a pin's terminal; Fritzing may leave the part out of that view
			seen[l] = true
			warns = append(warns, l)
		}
	}
	return errs, warns
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (r *Runner) save(projectDir string, res Result) error {
	if err := os.MkdirAll(OutDir(projectDir), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	return writeAtomic(filepath.Join(OutDir(projectDir), "result.json"), b)
}

// Last is the stored result of the last render, or nil.
func Last(projectDir string) *Result {
	b, err := os.ReadFile(filepath.Join(OutDir(projectDir), "result.json"))
	if err != nil {
		return nil
	}
	var res Result
	if json.Unmarshal(b, &res) != nil {
		return nil
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	if res.Views == nil {
		res.Views = []string{}
	}
	return &res
}

// PNG rasterises an SVG with rsvg-convert, width px wide on a white background.
func (r *Runner) PNG(ctx context.Context, svg []byte, width int) ([]byte, error) {
	if r.Rsvg == "" {
		return nil, errors.New("rsvg-convert is not available")
	}
	if width <= 0 {
		width = 1200
	}
	width = min(width, 4000)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Rsvg, "-f", "png", "-w", fmt.Sprint(width), "-b", "white")
	cmd.Stdin = bytes.NewReader(svg)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rsvg-convert: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// ViewSVG reads the rendered SVG of a view.
func ViewSVG(projectDir, view string) ([]byte, error) {
	if !parts.ValidView(view) {
		return nil, fmt.Errorf("unknown view %q (breadboard, schematic, pcb)", view)
	}
	b, err := os.ReadFile(SVGPath(projectDir, view))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no %s render yet: %w", view, project.ErrNotFound)
	}
	return b, err
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sketchModules are the module ids the project's sketch uses.
func sketchModules(files []project.File) []string {
	for _, f := range files {
		if f.Name == project.MainFile {
			return moduleRefs(f.Content)
		}
	}
	return nil
}

func mentions(line string, modules []string) bool {
	for _, m := range modules {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}
