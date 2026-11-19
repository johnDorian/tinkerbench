// Package render runs the openscad CLI: main.scad -> out/model.stl, PNG snapshots, and the
// per-part exports of a model's part() calls.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxLog = 20000 // bytes of OpenSCAD output kept in a result

// Result of the last render of a project, stored in out/render.json.
type Result struct {
	OK         bool              `json:"ok"`
	Started    time.Time         `json:"started"`
	DurationMS int64             `json:"duration_ms"`
	Params     map[string]string `json:"params,omitempty"`
	Errors     []string          `json:"errors,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	Echo       []string          `json:"echo,omitempty"`
	Log        string            `json:"log"`
	// Stats of out/model.stl. On a failed render the previous model is kept, so these
	// then describe the last successful render (see STLTime).
	Stats   *Stats    `json:"stats,omitempty"`
	STLTime time.Time `json:"stl_time,omitzero"`
	// Labels are the part() and feature() echoes of this render. They feed the parts
	// listing and are not part of the stored or returned result.
	Labels Labels `json:"-"`
}

// Renderer runs openscad with a concurrency limit and a timeout.
type Renderer struct {
	Bin     string        // openscad binary
	Timeout time.Duration // per openscad run
	// LibDir is prepended to OPENSCADPATH for every run, so `use <workbench.scad>` resolves
	// (see InstallLibrary). Empty leaves OPENSCADPATH alone.
	LibDir string
	// ExtraArgs (OPENSCAD_ARGS) are put in front of every run's arguments, e.g.
	// --backend=manifold on a build that has it.
	ExtraArgs []string
	xvfb      string // xvfb-run path, used for PNG export when there's no display

	sem   chan struct{}
	mu    sync.Mutex
	locks map[string]*sync.Mutex // one render per project dir at a time
}

// New creates a Renderer allowing `concurrent` openscad processes.
func New(bin string, timeout time.Duration, concurrent int) *Renderer {
	if concurrent < 1 {
		concurrent = 1
	}
	r := &Renderer{Bin: bin, Timeout: timeout, sem: make(chan struct{}, concurrent), locks: map[string]*sync.Mutex{}}
	if os.Getenv("DISPLAY") == "" {
		r.xvfb, _ = exec.LookPath("xvfb-run")
	}
	return r
}

// Version returns the openscad version string.
func (r *Renderer) Version() string {
	out, err := exec.Command(r.Bin, "--version").CombinedOutput()
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return strings.TrimSpace(string(out))
}

func (r *Renderer) lock(dir string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.locks[dir]
	if !ok {
		l = &sync.Mutex{}
		r.locks[dir] = l
	}
	return l
}

// environ is the environment of an openscad run: the library dir first on OPENSCADPATH.
func (r *Renderer) environ() []string {
	if r.LibDir == "" {
		return nil // inherit
	}
	path := r.LibDir
	old := os.Getenv("OPENSCADPATH")
	if old != "" {
		path += string(os.PathListSeparator) + old
	}
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "OPENSCADPATH=") {
			env = append(env, kv)
		}
	}
	return append(env, "OPENSCADPATH="+path)
}

func (r *Renderer) run(ctx context.Context, dir string, png bool, args ...string) (string, error) {
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-r.sem }()
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	args = append(append([]string{}, r.ExtraArgs...), args...)
	bin := r.Bin
	if png && r.xvfb != "" {
		args = append([]string{"-a", "-s", "-screen 0 1920x1080x24", bin}, args...)
		bin = r.xvfb
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = r.environ()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("openscad timed out after %s", r.Timeout)
	}
	return out.String(), err
}

// OutDir is where render output of a project dir goes.
func OutDir(dir string) string { return filepath.Join(dir, "out") }

// STLPath is the last successfully rendered model of a project dir.
func STLPath(dir string) string { return filepath.Join(OutDir(dir), "model.stl") }

// Last returns the stored result of the last render, or nil.
func Last(dir string) *Result {
	b, err := os.ReadFile(filepath.Join(OutDir(dir), "render.json"))
	if err != nil {
		return nil
	}
	var res Result
	if json.Unmarshal(b, &res) != nil {
		return nil
	}
	return &res
}

var identRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_]*$`)

// ValidParamName reports whether k can be overridden with -D.
func ValidParamName(k string) bool { return identRe.MatchString(k) }

// defineArgs turns params into -D arguments, checking the names.
func defineArgs(params map[string]string) ([]string, error) {
	var args []string
	for k, v := range params {
		if !identRe.MatchString(k) {
			return nil, fmt.Errorf("invalid parameter name %q", k)
		}
		args = append(args, "-D", k+"="+v)
	}
	return args, nil
}

// Render renders main.scad in dir to out/model.stl with -D overrides and stores the result.
// The previous model is only replaced when the render succeeds.
func (r *Renderer) Render(ctx context.Context, dir, main string, params map[string]string) (Result, error) {
	l := r.lock(dir)
	l.Lock()
	defer l.Unlock()

	out := OutDir(dir)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return Result{}, err
	}
	res := Result{Started: time.Now(), Params: params}
	tmp := filepath.Join(out, "model.tmp.stl")
	os.Remove(tmp)
	args := []string{"-o", tmp, "--export-format", "binstl"}
	defs, err := defineArgs(params)
	if err != nil {
		return Result{}, err
	}
	args = append(args, defs...)
	args = append(args, main)

	log, err := r.run(ctx, dir, false, args...)
	if ctx.Err() == context.Canceled {
		return Result{}, ctx.Err()
	}
	res.DurationMS = time.Since(res.Started).Milliseconds()
	res.Log = trimLog(log)
	res.Errors, res.Warnings, res.Echo = parseLog(log, dir)
	res.Labels, _ = parseLabels(log)
	if err != nil && len(res.Errors) == 0 {
		msg := err.Error()
		if lines := strings.Split(strings.TrimSpace(log), "\n"); lines[len(lines)-1] != "" {
			msg += ": " + lines[len(lines)-1]
		}
		res.Errors = []string{msg}
	}
	if err == nil && len(res.Errors) == 0 {
		if st, serr := os.Stat(tmp); serr != nil || st.Size() == 0 {
			res.Errors = append(res.Errors, "openscad produced no geometry (empty top-level object?)")
		}
	}
	res.OK = len(res.Errors) == 0
	if res.OK {
		if err := os.Rename(tmp, STLPath(dir)); err != nil {
			return Result{}, err
		}
	} else {
		os.Remove(tmp)
	}
	if st, err := os.Stat(STLPath(dir)); err == nil {
		res.STLTime = st.ModTime()
		if s, err := ReadSTLFile(STLPath(dir)); err == nil {
			res.Stats = &s
		}
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "render.json"), b, 0o644); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Export renders file main in dir to the binary STL out, with -D params. It is for the
// per-part renders: nothing is stored and out/ is untouched. empty reports a file whose top
// level has no geometry, which OpenSCAD reports as an error and writes no file for; it is
// not an error here.
func (r *Renderer) Export(ctx context.Context, dir, main, out string, params map[string]string) (empty bool, err error) {
	defs, err := defineArgs(params)
	if err != nil {
		return false, err
	}
	args := append([]string{"-o", out, "--export-format", "binstl"}, defs...)
	args = append(args, main)
	log, err := r.run(ctx, dir, false, args...)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err == nil {
		return false, nil
	}
	if strings.Contains(log, "Current top level object is empty") && len(parseErrors(log)) == 0 {
		return true, nil
	}
	msg := err.Error()
	if e := parseErrors(log); len(e) > 0 {
		msg = e[0]
	}
	return false, errors.New(msg)
}

func parseErrors(log string) []string {
	var errs []string
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ERROR:") || strings.HasPrefix(line, "Parser error") {
			errs = append(errs, line)
		}
	}
	return errs
}

func trimLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxLog {
		s = "…\n" + s[len(s)-maxLog:]
	}
	return s
}

// echoContent returns the text of an `ECHO: "..."` line (OpenSCAD quotes string echoes and
// does not escape quotes inside them).
func echoContent(line string) (string, bool) {
	const prefix = `ECHO: "`
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `"`) || len(line) < len(prefix)+1 {
		return "", false
	}
	return line[len(prefix) : len(line)-1], true
}

// isLabelEcho reports whether a line is one of the workbench's own echoes (part, feature
// or warning), which are not shown as ordinary echo output.
func isLabelEcho(line string) bool {
	c, ok := echoContent(strings.TrimSpace(line))
	return ok && (strings.HasPrefix(c, "PART:") || strings.HasPrefix(c, "FEATURE:") || strings.HasPrefix(c, "WORKBENCH WARNING:"))
}

// parseLog picks ERROR/WARNING/ECHO lines out of OpenSCAD's output, with the project dir
// stripped from file paths.
func parseLog(log, dir string) (errs, warns, echo []string) {
	for _, line := range strings.Split(log, "\n") {
		if isLabelEcho(line) {
			continue
		}
		line = strings.TrimSpace(strings.ReplaceAll(line, dir+string(filepath.Separator), ""))
		switch {
		case strings.HasPrefix(line, "ERROR:"), strings.HasPrefix(line, "Parser error"):
			errs = append(errs, line)
		case strings.HasPrefix(line, "WARNING:"):
			warns = append(warns, line)
		case strings.HasPrefix(line, "ECHO:"):
			if len(echo) < 200 {
				echo = append(echo, line)
			}
		}
	}
	return
}

// Label is a part() or feature() call found in a render's echo output.
type Label struct {
	Kind string // "part" or "feature"
	Name string
	At   [3]float64 // features only
}

// Labels is what one render reported: the part names in order of first appearance, and the
// features (name and absolute position). Warnings are the workbench's own (nested parts,
// reserved names, malformed features).
type Labels struct {
	Parts    []string  `json:"parts"`
	Features []Feature `json:"features"`
}

// Feature is a named point of the model, in absolute model mm.
type Feature struct {
	Name string     `json:"name"`
	At   [3]float64 `json:"at"`
}

// parseLabels reads the PART:, FEATURE: and WORKBENCH WARNING echoes of an OpenSCAD log.
// Names are kept as written; the same part name is listed once (a part in a loop, or the
// same part called twice, is one part).
func parseLabels(log string) (Labels, []string) {
	var lbl Labels
	var warns []string
	seen := map[string]bool{}
	for _, line := range strings.Split(log, "\n") {
		c, ok := echoContent(strings.TrimSpace(line))
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(c, "PART:"):
			name := strings.TrimPrefix(c, "PART:")
			if name != "" && !seen[name] {
				seen[name] = true
				lbl.Parts = append(lbl.Parts, name)
			}
		case strings.HasPrefix(c, "FEATURE:"):
			rest := strings.TrimPrefix(c, "FEATURE:")
			i := strings.LastIndex(rest, ":[")
			if i < 0 || !strings.HasSuffix(rest, "]") {
				warns = append(warns, "WARNING: unreadable feature "+strconv.Quote(rest)+"; pass [x, y, z]")
				continue
			}
			at, err := parseVec3(rest[i+2 : len(rest)-1])
			if err != nil {
				warns = append(warns, "WARNING: feature "+strconv.Quote(rest[:i])+": "+err.Error()+"; pass [x, y, z]")
				continue
			}
			lbl.Features = append(lbl.Features, Feature{Name: rest[:i], At: at})
		case strings.HasPrefix(c, "WORKBENCH WARNING:"):
			warns = append(warns, "WARNING:"+strings.TrimPrefix(c, "WORKBENCH WARNING:"))
		}
	}
	return lbl, warns
}

func parseVec3(s string) ([3]float64, error) {
	var v [3]float64
	f := strings.Split(s, ",")
	if len(f) != 3 {
		return v, fmt.Errorf("want 3 numbers, got %q", s)
	}
	for i := range 3 {
		x, err := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
		if err != nil {
			return v, fmt.Errorf("%q is not a number", strings.TrimSpace(f[i]))
		}
		v[i] = x
	}
	return v, nil
}

// Views are camera rotations (rot_x, rot_y, rot_z in degrees, OpenSCAD --camera) for snapshots.
var Views = map[string][3]float64{
	"iso":    {55, 0, 25},
	"top":    {0, 0, 0},
	"bottom": {180, 0, 0},
	"front":  {90, 0, 0},
	"back":   {90, 0, 180},
	"left":   {90, 0, 270},
	"right":  {90, 0, 90},
}

// SnapshotOptions configure a PNG snapshot.
type SnapshotOptions struct {
	View   string
	Width  int
	Height int
	// FromSource renders a preview of main.scad (shows colors and #/% debug modifiers)
	// instead of the rendered STL.
	FromSource bool
	Params     map[string]string
}

// Snapshot renders a PNG of the project's model and returns it.
func (r *Renderer) Snapshot(ctx context.Context, dir, main string, o SnapshotOptions) ([]byte, error) {
	rot, ok := Views[o.View]
	if !ok {
		return nil, fmt.Errorf("unknown view %q", o.View)
	}
	if o.Width <= 0 || o.Height <= 0 {
		o.Width, o.Height = 800, 600
	}
	o.Width, o.Height = min(o.Width, 1600), min(o.Height, 1600)
	out := OutDir(dir)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(out, "snap-*.png")
	if err != nil {
		return nil, err
	}
	png := f.Name()
	f.Close()
	defer os.Remove(png)

	src := main
	if !o.FromSource {
		if _, err := os.Stat(STLPath(dir)); err != nil {
			return nil, errors.New("no rendered model yet; render first")
		}
		src = filepath.Join(out, "snapshot.scad")
		if err := os.WriteFile(src, []byte(`color("#e8a33d") import("model.stl");`+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
	args := []string{"-o", png,
		fmt.Sprintf("--imgsize=%d,%d", o.Width, o.Height),
		fmt.Sprintf("--camera=0,0,0,%g,%g,%g,0", rot[0], rot[1], rot[2]),
		"--viewall", "--autocenter", "--colorscheme=Tomorrow", "--view=axes,scales",
	}
	if o.View != "iso" {
		args = append(args, "--projection=o")
	}
	if o.FromSource {
		for k, v := range o.Params {
			if identRe.MatchString(k) {
				args = append(args, "-D", k+"="+v)
			}
		}
	}
	args = append(args, src)
	log, err := r.run(ctx, dir, true, args...)
	if err != nil {
		return nil, fmt.Errorf("snapshot failed: %v\n%s", err, trimLog(log))
	}
	b, err := os.ReadFile(png)
	if err == nil && len(b) == 0 {
		err = fmt.Errorf("snapshot failed: empty image\n%s", trimLog(log))
	}
	return b, err
}
