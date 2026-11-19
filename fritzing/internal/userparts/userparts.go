// Package userparts is a second parts library next to Fritzing's own: parts imported one at a time
// from .fzpz bundles (what the Fritzing forum, Adafruit, SparkFun and others publish). It is kept
// apart from parts.db, so the core library stays untouched, but searches see both.
//
// Each part lives in its own folder below the library directory with the files of its bundle
// (the .fzp plus svg.<view>.<name>.svg artwork) and a .meta.json. When a sketch uses a part, the app
// copies its files into the .fzz Fritzing renders, which is how Fritzing itself carries custom parts.
package userparts

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

const (
	maxEntries   = 60
	maxBundle    = 20 << 20 // uncompressed
	metaFile     = ".meta.json"
	maxModuleLen = 200
)

// Meta is kept next to a part's files.
type Meta struct {
	Source     string    `json:"source"` // URL or "upload"
	ImportedAt time.Time `json:"imported_at"`
}

type entry struct {
	dir  string // folder name below the library directory
	fzp  *parts.FZP
	file string // name of the .fzp inside the folder
	meta Meta
}

// Library is the set of imported parts.
type Library struct {
	root string
	mu   sync.RWMutex
	by   map[string]*entry // by module id
}

// Open loads the library from dir (created if missing). Folders that do not hold a readable part
// are skipped with no error, so one broken import cannot take the server down.
func Open(dir string) (*Library, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Library{root: dir, by: map[string]*entry{}}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, d := range ents {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		if e, err := l.load(d.Name()); err == nil {
			l.by[e.fzp.ModuleID] = e
		}
	}
	return l, nil
}

func (l *Library) load(dirName string) (*entry, error) {
	d := filepath.Join(l.root, dirName)
	files, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".fzp") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(d, f.Name()))
		if err != nil {
			return nil, err
		}
		fzp, err := parts.ParseFZP(b)
		if err != nil {
			return nil, err
		}
		e := &entry{dir: dirName, fzp: fzp, file: f.Name()}
		if mb, err := os.ReadFile(filepath.Join(d, metaFile)); err == nil {
			json.Unmarshal(mb, &e.meta)
		}
		return e, nil
	}
	return nil, errors.New("no .fzp")
}

// Count is the number of imported parts.
func (l *Library) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.by)
}

// Has reports whether a module id is an imported part.
func (l *Library) Has(moduleID string) bool {
	if l == nil {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.by[moduleID]
	return ok
}

// Imported describes the outcome of an import.
type Imported struct {
	ModuleID   string   `json:"module_id"`
	Title      string   `json:"title"`
	Connectors int      `json:"connectors"`
	Views      []string `json:"views"`
	Replaced   bool     `json:"replaced,omitempty"`
	// Warnings are things that will work but not perfectly (a pin that has no spot in the
	// artwork, a view the part lacks).
	Warnings []string `json:"warnings"`
}

// Import validates a .fzpz bundle and adds the part. taken says whether a module id already exists
// in Fritzing's own library: those are never replaced. An imported part with the same id is only
// replaced when overwrite is set.
func (l *Library) Import(data []byte, source string, overwrite bool, taken func(moduleID string) bool) (*Imported, error) {
	files, err := readBundle(data)
	if err != nil {
		return nil, err
	}
	var fzpName string
	for _, n := range sortedNames(files) {
		if strings.HasSuffix(strings.ToLower(n), ".fzp") {
			if fzpName != "" {
				return nil, fmt.Errorf("the bundle holds more than one part (%s, %s): import one part at a time", fzpName, n)
			}
			fzpName = n
		}
	}
	if fzpName == "" {
		return nil, errors.New("no .fzp part definition in the bundle (is this a .fzpz?)")
	}
	fzp, err := parts.ParseFZP(files[fzpName])
	if err != nil {
		return nil, err
	}
	id := fzp.ModuleID
	if len(id) > maxModuleLen || strings.ContainsAny(id, "\"<>&\x00\r\n") {
		return nil, fmt.Errorf("unusable module id %q", id)
	}
	if taken != nil && taken(id) {
		return nil, fmt.Errorf("module id %q is already in Fritzing's own library; the bundle would clash with it", id)
	}
	out := &Imported{ModuleID: id, Title: fzp.Title, Views: []string{}, Warnings: []string{}}
	if out.Title == "" {
		out.Title = id
		out.Warnings = append(out.Warnings, "the part has no title; its module id is used")
	}
	out.Connectors = len(fzp.Connectors)
	if len(fzp.Connectors) == 0 {
		return nil, errors.New("the part has no connectors")
	}
	for _, v := range parts.Views {
		img := fzp.Image(v)
		if img == "" {
			out.Warnings = append(out.Warnings, fmt.Sprintf("no %s view: the part will not appear there", v))
			continue
		}
		svg, ok := findArtwork(files, v, img)
		if !ok {
			return nil, fmt.Errorf("the %s view names %s but the bundle has no such file (looked for svg.%s.%s)", v, img, v, base(img))
		}
		if bytes.Contains(bytes.ToLower(svg), []byte("<script")) {
			return nil, fmt.Errorf("%s artwork contains a <script> element", v)
		}
		info, ok := sketch.ParseSVG(svg)
		if !ok {
			return nil, fmt.Errorf("%s artwork has no usable width/height (or viewBox)", v)
		}
		out.Views = append(out.Views, v)
		missing := 0
		first := ""
		for i := range fzp.Connectors {
			c := &fzp.Connectors[i]
			ps := c.Pins(v)
			if len(ps) == 0 {
				missing++
				continue
			}
			if _, _, ok := info.Pin(ps[0].SvgID, ps[0].TerminalID, ps[0].LegID); !ok {
				if first == "" {
					first = c.ID
				}
				missing++
			}
		}
		if missing > 0 {
			msg := fmt.Sprintf("%s view: %d of %d connectors have no matching element in the artwork", v, missing, len(fzp.Connectors))
			if first != "" {
				msg += " (first: " + first + ")"
			}
			out.Warnings = append(out.Warnings, msg+"; wires to them will be misplaced")
		}
	}
	if len(out.Views) == 0 {
		return nil, errors.New("the part has no artwork for breadboard, schematic or pcb")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if old, exists := l.by[id]; exists {
		if !overwrite {
			return nil, fmt.Errorf("an imported part %q already exists; set overwrite to replace it", id)
		}
		out.Replaced = true
		_ = old
	}
	dirName := folderName(id)
	tmp, err := os.MkdirTemp(l.root, ".import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(tmp, n), b, 0o644); err != nil {
			return nil, err
		}
	}
	meta := Meta{Source: source, ImportedAt: time.Now().UTC()}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(tmp, metaFile), mb, 0o644); err != nil {
		return nil, err
	}
	final := filepath.Join(l.root, dirName)
	if err := os.RemoveAll(final); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	l.by[id] = &entry{dir: dirName, fzp: fzp, file: fzpName, meta: meta}
	return out, nil
}

// Delete removes an imported part.
func (l *Library) Delete(moduleID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.by[moduleID]
	if !ok {
		return fmt.Errorf("imported part %q: %w", moduleID, parts.ErrNotFound)
	}
	if err := os.RemoveAll(filepath.Join(l.root, e.dir)); err != nil {
		return err
	}
	delete(l.by, moduleID)
	return nil
}

func (l *Library) get(moduleID string) (*entry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.by[moduleID]
	if !ok {
		return nil, fmt.Errorf("imported part %q: %w", moduleID, parts.ErrNotFound)
	}
	return e, nil
}

// FZP returns the parsed definition and the file name inside the bundle.
func (l *Library) FZP(moduleID string) (*parts.FZP, string, error) {
	e, err := l.get(moduleID)
	if err != nil {
		return nil, "", err
	}
	return e.fzp, e.file, nil
}

// SVG returns the artwork for breadboard, schematic, pcb or icon.
func (l *Library) SVG(moduleID, view string) ([]byte, error) {
	e, err := l.get(moduleID)
	if err != nil {
		return nil, err
	}
	img := e.fzp.Image(view)
	if img == "" {
		return nil, fmt.Errorf("part %q has no %s image: %w", moduleID, view, parts.ErrNotFound)
	}
	for _, cand := range []string{"svg." + view + "." + base(img), base(img)} {
		if b, err := os.ReadFile(filepath.Join(l.root, e.dir, cand)); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("part %q: artwork %s is missing: %w", moduleID, img, parts.ErrNotFound)
}

// File is a name and its content, the shape of a project file.
type File struct {
	Name    string
	Content string
}

// Files returns the files of a part as they go into an .fzz.
func (l *Library) Files(moduleID string) ([]File, error) {
	e, err := l.get(moduleID)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(l.root, e.dir))
	if err != nil {
		return nil, err
	}
	var out []File
	for _, f := range ents {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(l.root, e.dir, f.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: f.Name(), Content: string(b)})
	}
	return out, nil
}

// Source tells where a part came from.
func (l *Library) Source(moduleID string) string {
	e, err := l.get(moduleID)
	if err != nil {
		return ""
	}
	return e.meta.Source
}

// Get describes a part the way the library does.
func (l *Library) Get(moduleID string) (*parts.Part, error) {
	e, err := l.get(moduleID)
	if err != nil {
		return nil, err
	}
	f := e.fzp
	p := &parts.Part{Hit: hit(f), Label: strings.TrimSpace(f.Label), Tags: f.Tags, FZPPath: e.file, Connectors: []parts.Connector{}}
	p.Hit.Description = strings.Join(strings.Fields(f.Description), " ")
	for _, c := range f.Connectors {
		k := parts.Connector{ID: c.ID, Name: c.Name, Description: strings.TrimSpace(c.Description), Type: c.Type}
		if strings.EqualFold(k.Description, k.Name) {
			k.Description = ""
		}
		p.Connectors = append(p.Connectors, k)
	}
	for _, b := range f.Buses {
		var m []string
		for _, x := range b.Members {
			m = append(m, x.ConnectorID)
		}
		p.Buses = append(p.Buses, m)
	}
	for _, v := range parts.Views {
		if _, err := l.SVG(moduleID, v); err == nil {
			p.Views = append(p.Views, v)
		}
	}
	return p, nil
}

func prop(f *parts.FZP, name string) string {
	for _, p := range f.Props {
		if strings.EqualFold(p.Name, name) {
			return strings.TrimSpace(p.Value)
		}
	}
	return ""
}

func hit(f *parts.FZP) parts.Hit {
	h := parts.Hit{ModuleID: f.ModuleID, Title: f.Title, Family: prop(f, "family"), Source: "user", Properties: map[string]string{}}
	if h.Title == "" {
		h.Title = f.ModuleID
	}
	h.Description = shorten(f.Description, 200)
	for _, p := range f.Props {
		if v := strings.TrimSpace(p.Value); v != "" && !strings.EqualFold(p.Name, "part number") {
			if _, dup := h.Properties[p.Name]; !dup {
				h.Properties[p.Name] = v
			}
		}
	}
	return h
}

// Search finds imported parts: every word of Text has to occur in the title, label, description,
// module id, a tag or a property value. Empty Text lists everything.
func (l *Library) Search(q parts.Query) []parts.Hit {
	if l == nil {
		return nil
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 25
	}
	words := strings.Fields(strings.ToLower(q.Text))
	l.mu.RLock()
	defer l.mu.RUnlock()
	type ranked struct {
		h    parts.Hit
		rank int
	}
	var res []ranked
Next:
	for _, e := range l.by {
		f := e.fzp
		h := hit(f)
		hay := strings.ToLower(strings.Join([]string{f.Title, f.Label, f.Description, f.ModuleID, strings.Join(f.Tags, " "), propValues(f)}, "\n"))
		for _, w := range words {
			if !strings.Contains(hay, w) {
				continue Next
			}
		}
		if q.Family != "" && !strings.Contains(strings.ToLower(h.Family), strings.ToLower(q.Family)) {
			continue
		}
		for n, v := range q.Properties {
			got := ""
			for pn, pv := range h.Properties {
				if strings.EqualFold(pn, n) {
					got = pv
				}
			}
			if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(v)) {
				continue Next
			}
		}
		rank := 2
		title := strings.ToLower(h.Title)
		if len(words) > 0 && title == strings.Join(words, " ") {
			rank = 0
		} else if len(words) > 0 && containsAll(title, words) {
			rank = 1
		}
		res = append(res, ranked{h, rank})
	}
	sort.Slice(res, func(i, j int) bool {
		a, b := res[i], res[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if len(a.h.Title) != len(b.h.Title) {
			return len(a.h.Title) < len(b.h.Title)
		}
		return a.h.Title < b.h.Title
	})
	out := make([]parts.Hit, 0, min(len(res), q.Limit))
	for _, r := range res {
		if len(out) == q.Limit {
			break
		}
		out = append(out, r.h)
	}
	return out
}

func propValues(f *parts.FZP) string {
	var s []string
	for _, p := range f.Props {
		if !strings.EqualFold(p.Name, "part number") {
			s = append(s, p.Value)
		}
	}
	return strings.Join(s, " ")
}

func containsAll(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}

// ---------- bundle handling ----------

// readBundle unpacks a zip into a flat name -> content map, refusing anything odd.
func readBundle(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip/.fzpz file: %w", err)
	}
	files := map[string][]byte{}
	var total int64
	n := 0
	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || strings.HasPrefix(name, ".") || name == "" {
			return nil, fmt.Errorf("unexpected file name %q in the bundle (a .fzpz is a flat zip)", name)
		}
		if n++; n > maxEntries {
			return nil, fmt.Errorf("more than %d files in the bundle", maxEntries)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxBundle-total+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if total += int64(len(b)); total > maxBundle {
			return nil, fmt.Errorf("the bundle is larger than %d MB unpacked", maxBundle>>20)
		}
		files[name] = b
	}
	return files, nil
}

func findArtwork(files map[string][]byte, view, image string) ([]byte, bool) {
	for _, cand := range []string{"svg." + view + "." + base(image), base(image)} {
		if b, ok := files[cand]; ok {
			return b, true
		}
	}
	return nil, false
}

func base(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func sortedNames(m map[string][]byte) []string {
	n := make([]string, 0, len(m))
	for k := range m {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// folderName is a file-system-safe name for a module id; the hash keeps ids that only differ in
// odd characters apart.
func folderName(id string) string {
	h := sha1.Sum([]byte(id))
	s := strings.Trim(unsafe.ReplaceAllString(id, "_"), "._")
	if len(s) > 60 {
		s = s[:60]
	}
	return s + "-" + hex.EncodeToString(h[:4])
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
