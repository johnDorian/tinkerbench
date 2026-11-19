package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Comment is a note on a line range of a source file, optionally with suggested replacement
// code for those lines. Comments live in <project>/comments.json. A comment with an Anchor is
// about a face, edge or point of the model; if it has no source line either (File "", Line
// 0) it is a model-only comment and has no quote or suggestion.
type Comment struct {
	ID      int     `json:"id"`
	File    string  `json:"file"`
	Line    int     `json:"line"`     // first line, 1-based; 0 for model-only comments
	EndLine int     `json:"end_line"` // last line, inclusive
	Anchor  *Anchor `json:"anchor,omitempty"`
	// Quote is the text of the lines when the comment was made. After every save the comment
	// moves to wherever that text is now; if it's gone, the comment is Outdated.
	Quote      string    `json:"quote"`
	Body       string    `json:"body"`
	Suggestion *string   `json:"suggestion,omitempty"` // replacement for the lines
	Author     string    `json:"author"`
	Created    time.Time `json:"created"`
	Resolved   bool      `json:"resolved"`
	ResolvedBy string    `json:"resolved_by,omitempty"`
	Outdated   bool      `json:"outdated,omitempty"`
	Replies    []Reply   `json:"replies"`
}

// Anchor places a comment on the model: a face, edge or point of a named part (or of the
// whole model when Part is ""). Points and normals are in model mm. Stale means the geometry
// under the anchor is gone or moved since the comment was made; it is flagged, not moved.
type Anchor struct {
	Part   string         `json:"part"`
	Kind   string         `json:"kind"` // face, edge or point
	Point  [3]float64     `json:"point"`
	Normal *[3]float64    `json:"normal,omitempty"` // face only
	Edge   *[2][3]float64 `json:"edge,omitempty"`   // edge only
	Camera *Camera        `json:"camera,omitempty"` // where the person was looking
	Stale  bool           `json:"stale"`
}

// Camera is the view an anchor was made from.
type Camera struct {
	Position [3]float64 `json:"position"`
	Target   [3]float64 `json:"target"`
}

// Anchor kinds.
const (
	KindFace  = "face"
	KindEdge  = "edge"
	KindPoint = "point"
)

// NormalizeAnchor checks an anchor and drops the fields its kind doesn't use.
func NormalizeAnchor(a *Anchor) error {
	if a == nil {
		return nil
	}
	if a.Kind == "" {
		a.Kind = KindPoint
	}
	switch a.Kind {
	case KindFace, KindEdge, KindPoint:
	default:
		return fmt.Errorf("anchor kind %q: want face, edge or point", a.Kind)
	}
	for _, v := range a.Point {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New("anchor point must be finite")
		}
	}
	if a.Kind != KindFace {
		a.Normal = nil
	}
	if a.Kind != KindEdge {
		a.Edge = nil
	}
	a.Stale = false
	return nil
}

// Reply is an answer in a comment thread.
type Reply struct {
	Author string    `json:"author"`
	Body   string    `json:"body"`
	Time   time.Time `json:"time"`
}

// ErrOutdated is returned when a suggestion's lines no longer match the file.
var ErrOutdated = errors.New("the commented lines have changed since the comment was made")

func readComments(d string) ([]Comment, error) {
	b, err := os.ReadFile(filepath.Join(d, "comments.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return []Comment{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cs []Comment
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("comments.json: %w", err)
	}
	return cs, nil
}

func writeComments(d string, cs []Comment) error {
	b, _ := json.MarshalIndent(cs, "", "  ")
	return writeAtomic(filepath.Join(d, "comments.json"), b)
}

// Comments returns the comments of a project (only of file, if given), in file and line order.
// Resolved comments are only included with all.
func (s *Store) Comments(name, file string, all bool) ([]Comment, error) {
	d, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	cs, err := readComments(d)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := []Comment{}
	for _, c := range cs {
		if (file == "" || c.File == file) && (all || !c.Resolved) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// AddComment adds a comment on lines line..endLine (endLine 0 = just line) of file. With
// file "" and line 0 it is a model-only comment, which needs an anchor and can't have a
// suggestion.
func (s *Store) AddComment(name, file string, line, endLine int, body string, suggestion *string, anchor *Anchor, author string) (Comment, error) {
	d, err := s.dir(name)
	if err != nil {
		return Comment{}, err
	}
	if strings.TrimSpace(body) == "" && suggestion == nil {
		return Comment{}, errors.New("comment is empty")
	}
	if err := NormalizeAnchor(anchor); err != nil {
		return Comment{}, err
	}
	c := Comment{ID: 1, Body: strings.TrimSpace(body), Anchor: anchor, Author: author, Created: time.Now(), Replies: []Reply{}}
	if file == "" && line == 0 {
		if anchor == nil {
			return Comment{}, errors.New("a comment needs a file and line, or an anchor on the model")
		}
		if suggestion != nil {
			return Comment{}, errors.New("a suggestion replaces lines of code: comment on a line of a file, not on the model")
		}
	} else {
		if endLine == 0 {
			endLine = line
		}
		s.mu.Lock()
		content, err := s.ReadFile(name, file)
		s.mu.Unlock()
		if err != nil {
			return Comment{}, err
		}
		lines := splitLines(content)
		if line < 1 || endLine < line || endLine > len(lines) {
			return Comment{}, fmt.Errorf("lines %d-%d are outside %s (%d lines)", line, endLine, file, len(lines))
		}
		c.File, c.Line, c.EndLine = file, line, endLine
		c.Quote = strings.Join(lines[line-1:endLine], "\n")
		c.Suggestion = normSuggestion(suggestion)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := readComments(d)
	if err != nil {
		return Comment{}, err
	}
	for _, o := range cs {
		c.ID = max(c.ID, o.ID+1)
	}
	return c, writeComments(d, append(cs, c))
}

// updateComment runs fn on comment id and saves it.
func (s *Store) updateComment(name string, id int, fn func(*Comment) error) (Comment, error) {
	d, err := s.dir(name)
	if err != nil {
		return Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := readComments(d)
	if err != nil {
		return Comment{}, err
	}
	for i := range cs {
		if cs[i].ID == id {
			if err := fn(&cs[i]); err != nil {
				return Comment{}, err
			}
			return cs[i], writeComments(d, cs)
		}
	}
	return Comment{}, fmt.Errorf("comment %d: %w", id, ErrNotFound)
}

// ReplyComment adds a reply to a comment's thread.
func (s *Store) ReplyComment(name string, id int, author, body string) (Comment, error) {
	if strings.TrimSpace(body) == "" {
		return Comment{}, errors.New("reply is empty")
	}
	return s.updateComment(name, id, func(c *Comment) error {
		c.Replies = append(c.Replies, Reply{Author: author, Body: strings.TrimSpace(body), Time: time.Now()})
		return nil
	})
}

// ResolveComment resolves (or reopens) a comment.
func (s *Store) ResolveComment(name string, id int, resolved bool, by string) (Comment, error) {
	return s.updateComment(name, id, func(c *Comment) error {
		c.Resolved = resolved
		c.ResolvedBy = ""
		if resolved {
			c.ResolvedBy = by
		}
		return nil
	})
}

// DeleteComment removes a comment.
func (s *Store) DeleteComment(name string, id int) error {
	d, err := s.dir(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := readComments(d)
	if err != nil {
		return err
	}
	for i := range cs {
		if cs[i].ID == id {
			return writeComments(d, append(cs[:i], cs[i+1:]...))
		}
	}
	return fmt.Errorf("comment %d: %w", id, ErrNotFound)
}

// ApplySuggestion replaces the commented lines with the comment's suggestion, saves the file
// as a new version and resolves the comment.
func (s *Store) ApplySuggestion(name string, id int, by string) (Comment, Version, error) {
	d, err := s.dir(name)
	if err != nil {
		return Comment{}, Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := readComments(d)
	if err != nil {
		return Comment{}, Version{}, err
	}
	i := -1
	for k := range cs {
		if cs[k].ID == id {
			i = k
		}
	}
	if i < 0 {
		return Comment{}, Version{}, fmt.Errorf("comment %d: %w", id, ErrNotFound)
	}
	c := &cs[i]
	if c.File == "" {
		return Comment{}, Version{}, fmt.Errorf("comment %d is on the model, not on lines of code: nothing to apply", id)
	}
	if c.Suggestion == nil {
		return Comment{}, Version{}, fmt.Errorf("comment %d has no suggestion", id)
	}
	if c.Resolved {
		return Comment{}, Version{}, fmt.Errorf("comment %d is already resolved", id)
	}
	content, err := s.ReadFile(name, c.File)
	if err != nil {
		return Comment{}, Version{}, err
	}
	lines := splitLines(content)
	if c.Outdated || c.EndLine > len(lines) || strings.Join(lines[c.Line-1:c.EndLine], "\n") != c.Quote {
		return Comment{}, Version{}, ErrOutdated
	}
	repl := splitLines(*c.Suggestion)
	if *c.Suggestion == "" {
		repl = nil // suggestion to delete the lines
	}
	next := append(append(append([]string{}, lines[:c.Line-1]...), repl...), lines[c.EndLine:]...)

	// The comment now describes the new lines, so re-anchoring keeps it in place.
	c.Quote = strings.Join(repl, "\n")
	c.EndLine = c.Line + max(len(repl), 1) - 1
	c.Resolved, c.ResolvedBy = true, by
	c.Replies = append(c.Replies, Reply{Author: by, Body: "Applied the suggestion.", Time: time.Now()})
	applied := *c
	if err := writeComments(d, cs); err != nil {
		return Comment{}, Version{}, err
	}
	v, err := s.write(d, applied.File, strings.Join(next, "\n"), by, fmt.Sprintf("applied suggestion #%d", id))
	return applied, v, err
}

// reanchor moves the comments of file to where their quoted lines are in the new content
// (the nearest match to their old position); s.mu must be held.
func reanchor(d, file string, content *string) error {
	cs, err := readComments(d)
	if err != nil || len(cs) == 0 {
		return err
	}
	var lines []string
	if content != nil {
		lines = splitLines(*content)
	}
	changed := false
	for i := range cs {
		c := &cs[i]
		if c.File != file || c.Resolved {
			continue
		}
		before := *c
		if content == nil { // file deleted
			c.Outdated = true
		} else if at := findLines(lines, splitLines(c.Quote), c.Line); at > 0 {
			c.EndLine = at + c.EndLine - c.Line
			c.Line = at
			c.Outdated = false
		} else {
			c.Outdated = true
		}
		changed = changed || before.Line != c.Line || before.Outdated != c.Outdated
	}
	if !changed {
		return nil
	}
	return writeComments(d, cs)
}

// findLines returns the 1-based line where quote occurs in lines, nearest to near, or 0.
func findLines(lines, quote []string, near int) int {
	best, bestDist := 0, -1
	for i := 0; i+len(quote) <= len(lines); i++ {
		match := true
		for k, q := range quote {
			if strings.TrimRight(lines[i+k], " \t\r") != strings.TrimRight(q, " \t\r") {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		dist := i + 1 - near
		if dist < 0 {
			dist = -dist
		}
		if bestDist < 0 || dist < bestDist {
			best, bestDist = i+1, dist
		}
	}
	return best
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

// normSuggestion drops one trailing newline, so a suggestion typed as whole lines doesn't
// add an empty line.
func normSuggestion(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSuffix(strings.ReplaceAll(*s, "\r\n", "\n"), "\n")
	return &t
}

// UpdateAnchors re-checks the anchors of a project's comments with fits (true when the
// geometry under the anchor is still there). Anchors that stop fitting are flagged stale, and
// flagged ones that fit again are cleared. It returns how many comments changed.
func (s *Store) UpdateAnchors(name string, fits func(Anchor) bool) (int, error) {
	d, err := s.dir(name)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cs, err := readComments(d)
	if err != nil {
		return 0, err
	}
	changed := 0
	for i := range cs {
		a := cs[i].Anchor
		if a == nil {
			continue
		}
		stale := !fits(*a)
		if stale != a.Stale {
			a.Stale = stale
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, writeComments(d, cs)
}
