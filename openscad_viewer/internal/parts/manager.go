package parts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// Manager renders the parts of a model in the background, one job per project at a time.
// A new render cancels the running job (Cancel), and a finished job commits its listing and
// STL files only if it was not cancelled.
type Manager struct {
	r      *render.Renderer
	onDone func(project string, ok bool) // called after a job commits, or after a listing with no parts
	mu     sync.Mutex
	jobs   map[string]*job // by project dir
	seq    int
}

type job struct {
	id     int
	cancel context.CancelFunc
}

// NewManager makes a manager. onDone runs without the manager's lock held.
func NewManager(r *render.Renderer, onDone func(project string, ok bool)) *Manager {
	return &Manager{r: r, onDone: onDone, jobs: map[string]*job{}}
}

// Active reports whether part renders are running for a project dir.
func (m *Manager) Active(dir string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[dir]
	return ok
}

// Cancel stops the part renders of dir that are still running. Their parts are marked as not
// rendered, so the listing doesn't pass for a finished one.
func (m *Manager) Cancel(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelLocked(dir)
}

func (m *Manager) cancelLocked(dir string) {
	j, ok := m.jobs[dir]
	if !ok {
		return
	}
	j.cancel()
	delete(m.jobs, dir)
	if l, err := Read(dir); err == nil && l != nil {
		for i := range l.Parts {
			l.Parts[i].OK, l.Parts[i].BBox = false, nil
			l.Parts[i].Error = "cancelled by a newer render"
		}
		l.Pending = false
		write(dir, *l)
	}
}

// Start records the labels of a successful render and renders the pieces in the background.
// The listing is written before Start returns, with its parts not rendered yet.
func (m *Manager) Start(projectName, dir string, res render.Result) {
	mt := res.STLTime
	lst := Listing{ModelTime: &mt, Pending: true}
	for _, name := range res.Labels.Parts {
		file, line := Locate(dir, "part", name)
		lst.Parts = append(lst.Parts, Part{Name: name, File: file, Line: line, Error: ""})
	}
	for _, f := range res.Labels.Features {
		file, line := Locate(dir, "feature", f.Name)
		lst.Features = append(lst.Features, Feature{Name: f.Name, At: f.At, File: file, Line: line})
	}
	if len(lst.Parts) == 0 {
		lst.Pending = false
		m.mu.Lock()
		m.cancelLocked(dir)
		err := write(dir, lst)
		m.mu.Unlock()
		if err != nil {
			m.onDone(projectName, false)
			return
		}
		m.onDone(projectName, true)
		return
	}

	m.mu.Lock()
	m.cancelLocked(dir)
	m.seq++
	id := m.seq
	work := filepath.Join(render.OutDir(dir), "parts-work-"+strconv.Itoa(id))
	m.mu.Unlock()

	names := make([]string, len(lst.Parts))
	for i, p := range lst.Parts {
		names[i] = p.Name
	}
	if err := prepareWork(dir, work, names); err != nil {
		m.mu.Lock()
		for i := range lst.Parts {
			lst.Parts[i].Error = "cannot prepare the part renders: " + err.Error()
		}
		lst.Pending = false
		write(dir, lst)
		m.mu.Unlock()
		m.onDone(projectName, false)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	j := &job{id: id, cancel: cancel}
	m.mu.Lock()
	m.jobs[dir] = j
	err := write(dir, lst)
	m.mu.Unlock()
	if err != nil {
		m.mu.Lock()
		if m.jobs[dir] == j {
			delete(m.jobs, dir)
		}
		m.mu.Unlock()
		cancel()
		os.RemoveAll(work)
		m.onDone(projectName, false)
		return
	}
	go m.run(ctx, j, projectName, dir, work, lst, res.Params)
}

// prepareWork copies the project's .scad files into work and writes one wrapper per piece.
// A wrapper sets $only_part and includes main.scad, so the same source renders every piece;
// the rest wrapper (__rest__) renders what is outside every part.
func prepareWork(dir, work string, names []string) error {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	for _, f := range scadFiles(dir) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(work, f), b, 0o644); err != nil {
			return err
		}
	}
	for i, name := range names {
		if err := writeWrapper(work, wrapperName(i), name); err != nil {
			return err
		}
	}
	return writeWrapper(work, restWrapper, "__rest__")
}

// restWrapper renders the geometry outside every part.
const restWrapper = "wb_rest.scad"

func wrapperName(i int) string { return "wb_" + strconv.Itoa(i) + ".scad" }

var scadEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func writeWrapper(work, file, only string) error {
	src := fmt.Sprintf("$only_part = \"%s\";\ninclude <%s>\n", scadEscaper.Replace(only), project.MainFile)
	return os.WriteFile(filepath.Join(work, file), []byte(src), 0o644)
}

// run renders every piece (and the rest) in parallel, then commits.
func (m *Manager) run(ctx context.Context, j *job, projectName, dir, work string, lst Listing, params map[string]string) {
	defer os.RemoveAll(work)
	outDir := filepath.Join(render.OutDir(dir), "parts")
	os.MkdirAll(outDir, 0o755)
	n := len(lst.Parts)
	results := make([]Part, n+1) // the last one is the rest
	tmps := make([]string, n+1)
	var wg sync.WaitGroup
	for i := range n + 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name, file, line, wrap := Unlabelled, "", 0, restWrapper
			if i < n {
				name, file, line, wrap = lst.Parts[i].Name, lst.Parts[i].File, lst.Parts[i].Line, wrapperName(i)
			}
			p := Part{Name: name, File: file, Line: line}
			tmp := filepath.Join(outDir, fmt.Sprintf(".%d-%d.stl", j.id, i))
			empty, err := m.r.Export(ctx, work, wrap, tmp, params)
			switch {
			case err != nil:
				p.Error = err.Error()
				os.Remove(tmp)
			case empty:
				p.OK = true
				p.BBox = &Box{}
				os.Remove(tmp)
			default:
				st, err := render.ReadSTLFile(tmp)
				if err != nil {
					p.Error = err.Error()
					os.Remove(tmp)
					break
				}
				p.OK = true
				p.BBox = &Box{Min: st.Min, Max: st.Max}
				p.Triangles = st.Triangles
				tmps[i] = tmp
			}
			results[i] = p
		}()
	}
	wg.Wait()

	m.mu.Lock()
	if ctx.Err() != nil || m.jobs[dir] != j {
		// cancelled by a newer render, which owns the listing now
		for _, t := range tmps {
			if t != "" {
				os.Remove(t)
			}
		}
		m.mu.Unlock()
		return
	}
	delete(m.jobs, dir)

	// The remainder is listed only when it has geometry (or failed to render).
	final := append([]Part{}, results[:n]...)
	keep := append([]string{}, tmps[:n]...)
	if restEmpty := results[n].OK && tmps[n] == ""; !restEmpty {
		final = append(final, results[n])
		keep = append(keep, tmps[n])
	}
	for i, t := range keep {
		dst := PartPath(dir, i)
		if t == "" {
			os.Remove(dst)
			continue
		}
		os.Rename(t, dst)
	}
	pruneSTL(outDir, len(final))
	mt := *lst.ModelTime
	out := Listing{ModelTime: &mt, Pending: false, Parts: final, Features: lst.Features}
	ok := true
	for _, p := range final {
		ok = ok && p.OK
	}
	err := write(dir, out)
	m.mu.Unlock()
	m.onDone(projectName, ok && err == nil)
}

// pruneSTL removes index files at or above count, left by an earlier listing.
func pruneSTL(outDir string, count int) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := stlIndex.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if i, err := strconv.Atoi(m[1]); err == nil && i >= count {
			os.Remove(filepath.Join(outDir, e.Name()))
		}
	}
}

var stlIndex = regexp.MustCompile(`^(\d+)\.stl$`)
