// Package parts searches Fritzing's parts database (parts.db, SQLite, read-only) and reads the
// part definitions (.fzp) and artwork (.svg) it points to.
package parts

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned for an unknown part.
var ErrNotFound = errors.New("not found")

// Catalog is the parts library: the database plus the folder its paths are relative to.
type Catalog struct {
	db       *sql.DB
	partsDir string // the folder holding core/, contrib/, obsolete/, svg/, bins/
	Count    int
}

// Open opens parts.db read-only. partsDir is Fritzing's parts folder (/usr/share/fritzing/parts).
func Open(dbPath, partsDir string) (*Catalog, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	// immutable: the file is never written while we run, and it lets SQLite skip locking
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(dbPath)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	c := &Catalog{db: db, partsDir: partsDir}
	if err := db.QueryRow(`SELECT count(*) FROM parts`).Scan(&c.Count); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", dbPath, err)
	}
	return c, nil
}

func (c *Catalog) Close() error { return c.db.Close() }

// Hit is one search result.
type Hit struct {
	ModuleID    string            `json:"module_id"`
	Title       string            `json:"title"`
	Family      string            `json:"family"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
	Obsolete    bool              `json:"obsolete,omitempty"`
	// Source is "library" (Fritzing's own parts) or "user" (imported with import_part).
	Source string `json:"source,omitempty"`
}

// Query is a parts search. Every word of Text has to match somewhere (title, label, description,
// module id, family, tags or a property value); Properties further require name = value
// (case-insensitive, value may be a prefix like "DIP").
type Query struct {
	Text            string
	Family          string
	Properties      map[string]string
	IncludeObsolete bool
	Limit           int
	Source          string // "", "library" or "user"; only the app looks at it
}

const hitCols = `p.id, p.moduleID, p.title, p.family, coalesce(p.description,''), coalesce(p.replacedby,'')`

// Search returns the best matches first: exact title, then title hits, then the rest.
func (c *Catalog) Search(q Query) ([]Hit, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 25
	}
	var where []string
	var args []any
	words := strings.Fields(strings.ToLower(q.Text))
	for _, w := range words {
		like := "%" + escapeLike(w) + "%"
		where = append(where, `(lower(p.title) LIKE ? ESCAPE '\' OR lower(p.label) LIKE ? ESCAPE '\' OR lower(p.description) LIKE ? ESCAPE '\'
			OR lower(p.moduleID) LIKE ? ESCAPE '\' OR lower(p.family) LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM tags t WHERE t.part_id = p.id AND lower(t.tag) LIKE ? ESCAPE '\')
			OR EXISTS (SELECT 1 FROM properties pr WHERE pr.part_id = p.id AND pr.name <> 'part number' AND lower(pr.value) LIKE ? ESCAPE '\'))`)
		for i := 0; i < 7; i++ {
			args = append(args, like)
		}
	}
	if q.Family != "" {
		where = append(where, `lower(p.family) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(strings.ToLower(q.Family))+"%")
	}
	names := make([]string, 0, len(q.Properties))
	for n := range q.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		where = append(where, `EXISTS (SELECT 1 FROM properties pr WHERE pr.part_id = p.id AND lower(pr.name) = ? AND lower(pr.value) LIKE ? ESCAPE '\')`)
		args = append(args, strings.ToLower(n), escapeLike(strings.ToLower(q.Properties[n]))+"%")
	}
	if !q.IncludeObsolete {
		where = append(where, `p.family NOT LIKE '//obsolete//%' AND coalesce(p.replacedby,'') = ''`)
	}
	sqlText := `SELECT ` + hitCols + ` FROM parts p`
	if len(where) > 0 {
		sqlText += ` WHERE ` + strings.Join(where, " AND ")
	}
	// rank: whole title = the query, then title contains every word, then everything else
	rankArgs := []any{}
	rank := `2`
	if len(words) > 0 {
		cond := make([]string, len(words))
		for i, w := range words {
			cond[i] = `lower(p.title) LIKE ? ESCAPE '\'`
			rankArgs = append(rankArgs, "%"+escapeLike(w)+"%")
		}
		rank = `CASE WHEN lower(p.title) = ? THEN 0 WHEN ` + strings.Join(cond, " AND ") + ` THEN 1 ELSE 2 END`
		rankArgs = append([]any{strings.Join(words, " ")}, rankArgs...)
	}
	sqlText += ` ORDER BY ` + rank + `, length(p.title), p.title LIMIT ?`
	args = append(append(args, rankArgs...), q.Limit)
	rows, err := c.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	var ids []int64
	for rows.Next() {
		var id int64
		var h Hit
		var replaced string
		if err := rows.Scan(&id, &h.ModuleID, &h.Title, &h.Family, &h.Description, &replaced); err != nil {
			return nil, err
		}
		h.Source = "library"
		h.Obsolete = replaced != "" || strings.HasPrefix(h.Family, "//obsolete//")
		h.Description = shorten(h.Description, 200)
		out = append(out, h)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, id := range ids {
		props, err := c.properties(id)
		if err != nil {
			return nil, err
		}
		out[i].Properties = props
	}
	return out, nil
}

// properties are the part's searchable properties (package, resistance, ...); the part number is
// noise in a listing.
func (c *Catalog) properties(id int64) (map[string]string, error) {
	rows, err := c.db.Query(`SELECT name, value FROM properties WHERE part_id = ? AND value <> '' AND name <> 'part number' ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			return nil, err
		}
		if _, dup := m[n]; !dup {
			m[n] = v
		}
	}
	return m, rows.Err()
}

// Connector is one pin as the database knows it.
type Connector struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // male, female, pad
}

// Part is everything about one part that is useful for placing and wiring it.
type Part struct {
	Hit
	Label      string      `json:"label,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	FZPPath    string      `json:"fzp_path"`
	Connectors []Connector `json:"connectors"`
	// Buses lists connectors that are wired together inside the part (e.g. the ground pins of a
	// header, or the two ends of a breadboard strip).
	Buses [][]string `json:"buses,omitempty"`
	// Views says which of breadboard, schematic and pcb the part has artwork for.
	Views []string `json:"views"`
}

// Get returns a part by module id.
func (c *Catalog) Get(moduleID string) (*Part, error) {
	var id int64
	var p Part
	var replaced, path string
	err := c.db.QueryRow(`SELECT `+hitCols+`, coalesce(p.label,''), coalesce(p.path,'') FROM parts p WHERE p.moduleID = ?`, moduleID).
		Scan(&id, &p.ModuleID, &p.Title, &p.Family, &p.Description, &replaced, &p.Label, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("part %q: %w", moduleID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	p.Obsolete = replaced != "" || strings.HasPrefix(p.Family, "//obsolete//")
	p.Source = "library"
	p.FZPPath = path
	if p.Properties, err = c.properties(id); err != nil {
		return nil, err
	}
	if p.Tags, err = c.strings(`SELECT DISTINCT tag FROM tags WHERE part_id = ? ORDER BY tag`, id); err != nil {
		return nil, err
	}
	rows, err := c.db.Query(`SELECT connectorid, name, coalesce(description,''), type FROM connectors WHERE part_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k Connector
		var t int
		if err := rows.Scan(&k.ID, &k.Name, &k.Description, &t); err != nil {
			rows.Close()
			return nil, err
		}
		k.Type = [...]string{"male", "female", "pad"}[min(max(t, 0), 2)]
		if strings.EqualFold(k.Description, k.Name) {
			k.Description = ""
		}
		p.Connectors = append(p.Connectors, k)
	}
	rows.Close()
	buses, err := c.db.Query(`SELECT b.id, m.connectorid FROM buses b JOIN busmembers m ON m.bus_id = b.id WHERE b.part_id = ? ORDER BY b.id, m.id`, id)
	if err != nil {
		return nil, err
	}
	defer buses.Close()
	cur, last := []string(nil), int64(-1)
	for buses.Next() {
		var bid int64
		var cid string
		if err := buses.Scan(&bid, &cid); err != nil {
			return nil, err
		}
		if bid != last && cur != nil {
			p.Buses = append(p.Buses, cur)
			cur = nil
		}
		last = bid
		cur = append(cur, cid)
	}
	if cur != nil {
		p.Buses = append(p.Buses, cur)
	}
	views, err := c.db.Query(`SELECT DISTINCT viewid FROM viewimages WHERE part_id = ? ORDER BY viewid`, id)
	if err != nil {
		return nil, err
	}
	defer views.Close()
	for views.Next() {
		var v int
		if err := views.Scan(&v); err != nil {
			return nil, err
		}
		if v >= 1 && v <= 3 {
			p.Views = append(p.Views, Views[v-1])
		}
	}
	return &p, nil
}

func (c *Catalog) strings(q string, args ...any) ([]string, error) {
	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// FZP reads and parses a part's definition file.
func (c *Catalog) FZP(moduleID string) (*FZP, string, error) {
	p, err := c.Get(moduleID)
	if err != nil {
		return nil, "", err
	}
	full, err := c.safe(p.FZPPath)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, "", fmt.Errorf("part %q: %w", moduleID, err)
	}
	f, err := ParseFZP(b)
	return f, p.FZPPath, err
}

// SVG returns the artwork of a part in one view (breadboard, schematic, pcb) or "icon".
func (c *Catalog) SVG(moduleID, view string) ([]byte, error) {
	viewid := map[string]int{"icon": 0, Breadboard: 1, Schematic: 2, PCB: 3}
	id, ok := viewid[view]
	if !ok {
		return nil, fmt.Errorf("unknown view %q (breadboard, schematic, pcb, icon)", view)
	}
	var image, path string
	err := c.db.QueryRow(`SELECT v.image, p.path FROM viewimages v JOIN parts p ON p.id = v.part_id WHERE p.moduleID = ? AND v.viewid = ? LIMIT 1`, moduleID, id).Scan(&image, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("part %q has no %s image: %w", moduleID, view, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return c.SVGFile(path, image)
}

// SVGFile reads image (e.g. "breadboard/led.svg") from the svg folder next to the .fzp at fzpPath
// (e.g. "core/led.fzp"): artwork lives in svg/<core|contrib|...>/<image>.
func (c *Catalog) SVGFile(fzpPath, image string) ([]byte, error) {
	root, _, _ := strings.Cut(filepath.ToSlash(fzpPath), "/")
	full, err := c.safe(filepath.Join("svg", root, image))
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// safe joins rel below partsDir and refuses anything that escapes it.
func (c *Catalog) safe(rel string) (string, error) {
	full := filepath.Join(c.partsDir, filepath.FromSlash(rel))
	if r, err := filepath.Rel(c.partsDir, full); err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("bad path %q", rel)
	}
	return full, nil
}

// Family is a kind of part with how many there are.
type Family struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Families lists the part families (led, resistor, microcontroller board (arduino), ...) matching
// text, most populated first. Obsolete ones are left out.
func (c *Catalog) Families(text string, limit int) ([]Family, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := c.db.Query(`SELECT family, count(*) n FROM parts WHERE family NOT LIKE '//obsolete//%' AND coalesce(replacedby,'') = ''
		AND lower(family) LIKE ? ESCAPE '\' GROUP BY family ORDER BY n DESC, family LIMIT ?`, "%"+escapeLike(strings.ToLower(text))+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Family
	for rows.Next() {
		var f Family
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// PropertyValues lists the values a property takes (package, resistance, color, ...), optionally
// within one family, so a search can be narrowed with real values.
func (c *Catalog) PropertyValues(name, family string, limit int) ([]Family, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := c.db.Query(`SELECT pr.value, count(*) n FROM properties pr JOIN parts p ON p.id = pr.part_id
		WHERE lower(pr.name) = ? AND pr.value <> '' AND p.family NOT LIKE '//obsolete//%' AND coalesce(p.replacedby,'') = ''
		AND lower(p.family) LIKE ? ESCAPE '\' GROUP BY pr.value ORDER BY n DESC, pr.value LIMIT ?`,
		strings.ToLower(name), "%"+escapeLike(strings.ToLower(family))+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Family
	for rows.Next() {
		var f Family
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
