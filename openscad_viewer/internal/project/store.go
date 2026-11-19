// Package project keeps OpenSCAD projects as plain files:
//
//	<root>/<project>/project.json      description, customizer params
//	<root>/<project>/*.scad            sources; main.scad is what gets rendered
//	<root>/<project>/.history/         every saved version (NNNNNN_<file>) + log.jsonl
//	<root>/<project>/out/              render output (owned by package render)
//
// Deleted projects are moved to <root>/../trash instead of being removed.
package project

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MainFile is the entry point that is rendered; other .scad files can be include<>d from it.
const MainFile = "main.scad"

const template = `// Customizer parameters (override with render params)
size = 20; // [5:100]

cube(size, center = true);
`

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	fileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}\.scad$`)

	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// Meta is stored in project.json.
type Meta struct {
	Description string    `json:"description"`
	Created     time.Time `json:"created"`
	// Params are OpenSCAD -D overrides (name -> OpenSCAD expression) used for every render.
	Params map[string]string `json:"params,omitempty"`
}

// Info describes a project.
type Info struct {
	Name    string    `json:"name"`
	Meta              // description, created, params
	Files   []string  `json:"files"`
	Updated time.Time `json:"updated"`
	Version int       `json:"version"` // number of the latest saved version
	// OpenComments counts unresolved comments (list_comments / the UI's comments tab).
	OpenComments int `json:"open_comments"`
	// Images are uploaded scans and photos of the real part, used by measuring.
	Images []string `json:"images,omitempty"`
}

// File is one source file with its content.
type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Version is one entry of the history log; the content after the change is kept in .history.
type Version struct {
	N       int       `json:"n"`
	File    string    `json:"file"`
	Time    time.Time `json:"time"`
	Source  string    `json:"source,omitempty"`
	Note    string    `json:"note,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
}

// Store manages the project folders below root.
type Store struct {
	root string
	mu   sync.Mutex // serialises writes (history numbering)
}

// Open creates root if needed.
func Open(root string) (*Store, error) {
	// openscad runs inside the project folder, so a relative root would point at the wrong place
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// ValidName reports whether name is a usable project name.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid project name %q: use lowercase letters, digits, - and _ (max 64)", name)
	}
	return nil
}

// ValidFile reports whether file is a usable source file name.
func ValidFile(file string) error {
	if !fileRe.MatchString(file) {
		return fmt.Errorf("invalid file name %q: letters, digits, - and _, ending in .scad", file)
	}
	return nil
}

// Dir is the folder of a project (no existence check).
func (s *Store) Dir(name string) string { return filepath.Join(s.root, name) }

func (s *Store) dir(name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	d := s.Dir(name)
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", fmt.Errorf("project %q: %w", name, ErrNotFound)
	}
	return d, nil
}

// List returns all projects, most recently updated first.
func (s *Store) List() ([]Info, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := []Info{}
	for _, e := range entries {
		if !e.IsDir() || ValidName(e.Name()) != nil {
			continue
		}
		info, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// Get returns a project's metadata and file list.
func (s *Store) Get(name string) (Info, error) {
	d, err := s.dir(name)
	if err != nil {
		return Info{}, err
	}
	info := Info{Name: name, Files: []string{}}
	if b, err := os.ReadFile(filepath.Join(d, "project.json")); err == nil {
		json.Unmarshal(b, &info.Meta)
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return Info{}, err
	}
	for _, e := range entries {
		if e.IsDir() || ValidFile(e.Name()) != nil {
			continue
		}
		info.Files = append(info.Files, e.Name())
		if fi, err := e.Info(); err == nil && fi.ModTime().After(info.Updated) {
			info.Updated = fi.ModTime()
		}
	}
	// main.scad first, the rest alphabetically
	sort.Slice(info.Files, func(i, j int) bool {
		a, b := info.Files[i], info.Files[j]
		if (a == MainFile) != (b == MainFile) {
			return a == MainFile
		}
		return a < b
	})
	if vs, err := s.History(name, ""); err == nil && len(vs) > 0 {
		info.Version = vs[len(vs)-1].N
	}
	info.Images = imageNames(d)
	if cs, err := readComments(d); err == nil {
		for _, c := range cs {
			if !c.Resolved {
				info.OpenComments++
			}
		}
	}
	return info, nil
}

// Create makes a new project with main.scad (source, or a small template when empty).
func (s *Store) Create(name, description, source, by string) (Info, error) {
	if err := ValidName(name); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	d := s.Dir(name)
	if _, err := os.Stat(d); err == nil {
		s.mu.Unlock()
		return Info{}, fmt.Errorf("project %q: %w", name, ErrExists)
	}
	if err := os.MkdirAll(filepath.Join(d, ".history"), 0o755); err != nil {
		s.mu.Unlock()
		return Info{}, err
	}
	err := s.writeMeta(d, Meta{Description: description, Created: time.Now()})
	s.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	if source == "" {
		source = template
	}
	if _, err := s.WriteFile(name, MainFile, source, by, "created"); err != nil {
		return Info{}, err
	}
	return s.Get(name)
}

func (s *Store) writeMeta(d string, m Meta) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return writeAtomic(filepath.Join(d, "project.json"), b)
}

// UpdateMeta changes description and/or params (nil leaves a field unchanged).
func (s *Store) UpdateMeta(name string, description *string, params map[string]string) (Info, error) {
	d, err := s.dir(name)
	if err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	info, err := s.Get(name)
	if err == nil {
		m := info.Meta
		if description != nil {
			m.Description = *description
		}
		if params != nil {
			m.Params = params
		}
		err = s.writeMeta(d, m)
	}
	s.mu.Unlock()
	if err != nil {
		return Info{}, err
	}
	return s.Get(name)
}

// ReadFile returns one source file.
func (s *Store) ReadFile(name, file string) (string, error) {
	d, err := s.dir(name)
	if err != nil {
		return "", err
	}
	if err := ValidFile(file); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(d, file))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("file %s/%s: %w", name, file, ErrNotFound)
	}
	return string(b), err
}

// Files returns all source files with content, main.scad first.
func (s *Store) Files(name string) ([]File, error) {
	info, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(info.Files))
	for _, f := range info.Files {
		c, err := s.ReadFile(name, f)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: f, Content: c})
	}
	return out, nil
}

// WriteFile replaces (or creates) a source file and records the new content as a version.
func (s *Store) WriteFile(name, file, content, by, note string) (Version, error) {
	d, err := s.dir(name)
	if err != nil {
		return Version{}, err
	}
	if err := ValidFile(file); err != nil {
		return Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(d, file, content, by, note)
}

// write saves a file and records the version; s.mu must be held.
func (s *Store) write(d, file, content, by, note string) (Version, error) {
	if err := writeAtomic(filepath.Join(d, file), []byte(content)); err != nil {
		return Version{}, err
	}
	if err := reanchor(d, file, &content); err != nil {
		return Version{}, err
	}
	return s.record(d, Version{File: file, Source: by, Note: note}, content)
}

// EditFile replaces old with new in a file. old must occur exactly once unless replaceAll.
func (s *Store) EditFile(name, file, old, new string, replaceAll bool, by, note string) (Version, error) {
	if old == "" {
		return Version{}, errors.New("old_string is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := s.ReadFile(name, file)
	if err != nil {
		return Version{}, err
	}
	switch n := strings.Count(content, old); {
	case n == 0:
		return Version{}, fmt.Errorf("old_string not found in %s", file)
	case n > 1 && !replaceAll:
		return Version{}, fmt.Errorf("old_string occurs %d times in %s; add context to make it unique or set replace_all", n, file)
	}
	if replaceAll {
		content = strings.ReplaceAll(content, old, new)
	} else {
		content = strings.Replace(content, old, new, 1)
	}
	return s.write(s.Dir(name), file, content, by, note)
}

// DeleteFile removes a source file (not main.scad); its history is kept.
func (s *Store) DeleteFile(name, file, by string) (Version, error) {
	d, err := s.dir(name)
	if err != nil {
		return Version{}, err
	}
	if err := ValidFile(file); err != nil {
		return Version{}, err
	}
	if file == MainFile {
		return Version{}, errors.New("main.scad can't be deleted")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(d, file)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Version{}, fmt.Errorf("file %s/%s: %w", name, file, ErrNotFound)
		}
		return Version{}, err
	}
	if err := reanchor(d, file, nil); err != nil {
		return Version{}, err
	}
	return s.record(d, Version{File: file, Source: by, Deleted: true}, "")
}

// record appends a version to the history; s.mu must be held.
func (s *Store) record(d string, v Version, content string) (Version, error) {
	hist := filepath.Join(d, ".history")
	if err := os.MkdirAll(hist, 0o755); err != nil {
		return Version{}, err
	}
	vs, err := readLog(hist)
	if err != nil {
		return Version{}, err
	}
	v.N = 1
	if len(vs) > 0 {
		v.N = vs[len(vs)-1].N + 1
	}
	v.Time = time.Now()
	if !v.Deleted {
		if err := os.WriteFile(filepath.Join(hist, versionFile(v)), []byte(content), 0o644); err != nil {
			return Version{}, err
		}
	}
	line, _ := json.Marshal(v)
	f, err := os.OpenFile(filepath.Join(hist, "log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Version{}, err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return v, err
}

func versionFile(v Version) string { return fmt.Sprintf("%06d_%s", v.N, v.File) }

func readLog(hist string) ([]Version, error) {
	f, err := os.Open(filepath.Join(hist, "log.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Version
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var v Version
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			out = append(out, v)
		}
	}
	return out, sc.Err()
}

// History returns the versions of a project (only of file, if given), oldest first.
func (s *Store) History(name, file string) ([]Version, error) {
	d, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	vs, err := readLog(filepath.Join(d, ".history"))
	if err != nil || file == "" {
		return vs, err
	}
	var out []Version
	for _, v := range vs {
		if v.File == file {
			out = append(out, v)
		}
	}
	return out, nil
}

// GetVersion returns a version and the file content it saved.
func (s *Store) GetVersion(name string, n int) (Version, string, error) {
	vs, err := s.History(name, "")
	if err != nil {
		return Version{}, "", err
	}
	for _, v := range vs {
		if v.N != n {
			continue
		}
		if v.Deleted {
			return v, "", fmt.Errorf("version %d deleted %s; restore an earlier version of it", n, v.File)
		}
		b, err := os.ReadFile(filepath.Join(s.Dir(name), ".history", versionFile(v)))
		return v, string(b), err
	}
	return Version{}, "", fmt.Errorf("version %d of %s: %w", n, name, ErrNotFound)
}

// Delete moves a project to the trash folder next to root.
func (s *Store) Delete(name string) error {
	d, err := s.dir(name)
	if err != nil {
		return err
	}
	trash := filepath.Join(filepath.Dir(s.root), "trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	return os.Rename(d, filepath.Join(trash, name+"-"+time.Now().Format("20060102-150405")))
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
