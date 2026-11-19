// Package server is the REST API and event stream for the web UI, plus the MCP endpoint.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/fritzing/internal/app"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/runner"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
	"github.com/johnDorian/tinkerbench/fritzing/internal/userparts"
)

// Config is what the UI needs to know about the server.
type Config struct {
	FritzingVersion string `json:"fritzing_version"`
	MCPURL          string `json:"mcp_url"`
	Parts           int    `json:"parts"`
}

type Server struct {
	app *app.App
	mcp http.Handler
	web fs.FS
	cfg Config
}

func New(a *app.App, mcp http.Handler, web fs.FS, cfg Config) *Server {
	return &Server{app: a, mcp: mcp, web: web, cfg: cfg}
}

const by = "web"

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(s.web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/mcp", s.mcp)

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.cfg) })
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("GET /api/projects/{p}", s.getProject)
	mux.HandleFunc("PATCH /api/projects/{p}", s.patchProject)
	mux.HandleFunc("DELETE /api/projects/{p}", s.deleteProject)
	mux.HandleFunc("PUT /api/projects/{p}/files/{f}", s.putFile)
	mux.HandleFunc("DELETE /api/projects/{p}/files/{f}", s.deleteFile)
	mux.HandleFunc("POST /api/projects/{p}/render", s.render)
	mux.HandleFunc("GET /api/projects/{p}/views/{v}", s.viewSVG)
	mux.HandleFunc("GET /api/projects/{p}/wiring", s.wiring)
	mux.HandleFunc("GET /api/projects/{p}/wiring.md", s.wiringMD)
	mux.HandleFunc("PUT /api/projects/{p}/notes", s.putNotes)
	mux.HandleFunc("GET /api/projects/{p}/snapshot.png", s.snapshot)
	mux.HandleFunc("GET /api/projects/{p}/download.fzz", s.download)
	mux.HandleFunc("POST /api/projects/{p}/parts", s.addPart)
	mux.HandleFunc("DELETE /api/projects/{p}/parts/{t}", s.removePart)
	mux.HandleFunc("GET /api/projects/{p}/history", s.history)
	mux.HandleFunc("GET /api/projects/{p}/history/{n}", s.version)
	mux.HandleFunc("POST /api/projects/{p}/history/{n}/restore", s.restore)

	mux.HandleFunc("POST /api/userparts", s.importPart)
	mux.HandleFunc("DELETE /api/userparts/{id}", s.deleteUserPart)
	mux.HandleFunc("GET /api/parts", s.searchParts)
	mux.HandleFunc("GET /api/parts/{id}", s.getPart)
	mux.HandleFunc("GET /api/parts/{id}/{view}", s.partImage)
	mux.HandleFunc("GET /api/families", s.families)
	return mux
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.Store.List()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Description, Source string }
	if !readJSON(w, r, &body) {
		return
	}
	info, _, err := s.app.Create(r.Context(), body.Name, body.Description, body.Source, by, app.RenderAsync)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, info)
}

type projectView struct {
	project.Info
	Content    []project.File  `json:"content"`
	LastRender *runner.Result  `json:"last_render"`
	Sketch     *sketch.Summary `json:"sketch"`
	SketchErr  string          `json:"sketch_error,omitempty"`
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	info, err := s.app.Store.Get(p)
	if err != nil {
		fail(w, err)
		return
	}
	files, err := s.app.Store.Files(p)
	if err != nil {
		fail(w, err)
		return
	}
	v := projectView{Info: info, Content: files, LastRender: s.app.LastRender(p)}
	if sum, err := s.app.Describe(p); err == nil {
		v.Sketch = &sum
	} else {
		v.SketchErr = err.Error()
	}
	writeJSON(w, 200, v)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	var body struct{ Description string }
	if !readJSON(w, r, &body) {
		return
	}
	info, err := s.app.SetDescription(r.PathValue("p"), body.Description, by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Delete(r.PathValue("p"), by); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	var body struct{ Content, Note string }
	if !readJSON(w, r, &body) {
		return
	}
	res, err := s.app.WriteFile(r.Context(), r.PathValue("p"), r.PathValue("f"), body.Content, by, body.Note, app.RenderAsync)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	res, err := s.app.DeleteFile(r.Context(), r.PathValue("p"), r.PathValue("f"), by, app.RenderAsync)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request) {
	res, err := s.app.Render(r.Context(), r.PathValue("p"), by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) viewSVG(w http.ResponseWriter, r *http.Request) {
	b, err := s.app.SVG(r.PathValue("p"), strings.TrimSuffix(r.PathValue("v"), ".svg"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	if s.app.Users.Has(r.PathValue("id")) {
		w.Header().Set("Cache-Control", "no-cache") // can be replaced by a new import
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.Write(b)
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = parts.Breadboard
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("w"))
	png, err := s.app.Snapshot(r.Context(), r.PathValue("p"), view, width)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func (s *Server) wiring(w http.ResponseWriter, r *http.Request) {
	wr, err := s.app.Wiring(r.PathValue("p"), r.URL.Query().Get("view"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, wr)
}

func (s *Server) wiringMD(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	wr, err := s.app.Wiring(p, r.URL.Query().Get("view"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-wiring.md"`, p))
	}
	w.Write([]byte(wr.Markdown()))
}

func (s *Server) putNotes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
		By   string `json:"by"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	who := body.By
	if who == "" {
		who = by
	}
	if _, err := s.app.SetNotes(r.PathValue("p"), body.Text, who); err != nil {
		fail(w, err)
		return
	}
	wr, err := s.app.Wiring(r.PathValue("p"), r.URL.Query().Get("view"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, wr)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	b, err := s.app.FZZ(p)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.fzz"`, p))
	w.Write(b)
}

func (s *Server) addPart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModuleID   string            `json:"module_id"`
		Title      string            `json:"title"`
		Properties map[string]string `json:"properties"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	res, err := s.app.Edit(r.Context(), r.PathValue("p"), by, "added "+body.ModuleID, app.RenderAsync,
		func(sk *sketch.Sketch, rs sketch.Resolver) (any, error) {
			t, err := sk.AddPart(rs, body.ModuleID, sketch.AddOptions{Title: body.Title, Properties: body.Properties})
			return map[string]string{"title": t}, err
		})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) removePart(w http.ResponseWriter, r *http.Request) {
	t := r.PathValue("t")
	res, err := s.app.Edit(r.Context(), r.PathValue("p"), by, "removed "+t, app.RenderAsync,
		func(sk *sketch.Sketch, rs sketch.Resolver) (any, error) {
			gone, err := sk.Remove(t)
			return map[string]any{"removed": gone}, err
		})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	vs, err := s.app.Store.History(r.PathValue("p"), r.URL.Query().Get("file"))
	if err != nil {
		fail(w, err)
		return
	}
	if vs == nil {
		vs = []project.Version{}
	}
	writeJSON(w, 200, vs)
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad version", 400)
		return
	}
	v, content, err := s.app.Store.GetVersion(r.PathValue("p"), n)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"version": v, "content": content})
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "bad version", 400)
		return
	}
	res, err := s.app.Restore(r.Context(), r.PathValue("p"), n, by, app.RenderAsync)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) searchParts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := parts.Query{Text: q.Get("q"), Family: q.Get("family"), IncludeObsolete: q.Has("obsolete"), Source: q.Get("source")}
	query.Limit, _ = strconv.Atoi(q.Get("limit"))
	for _, kv := range q["prop"] { // prop=package:THT
		if k, v, ok := strings.Cut(kv, ":"); ok {
			if query.Properties == nil {
				query.Properties = map[string]string{}
			}
			query.Properties[k] = v
		}
	}
	hits, err := s.app.SearchParts(query)
	if err != nil {
		fail(w, err)
		return
	}
	if hits == nil {
		hits = []parts.Hit{}
	}
	writeJSON(w, 200, hits)
}

func (s *Server) getPart(w http.ResponseWriter, r *http.Request) {
	p, err := s.app.GetPart(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) partImage(w http.ResponseWriter, r *http.Request) {
	b, err := s.app.PartSVG(r.PathValue("id"), strings.TrimSuffix(r.PathValue("view"), ".svg"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Content-Security-Policy", "sandbox") // imported artwork is not ours: no scripts
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

// importPart adds one part. The body is either the .fzpz itself (any other content type) or JSON
// {"url": "...", "overwrite": false}; ?overwrite=1 also works for an upload.
func (s *Server) importPart(w http.ResponseWriter, r *http.Request) {
	overwrite := r.URL.Query().Get("overwrite") == "1"
	var data []byte
	var link string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			URL       string `json:"url"`
			Overwrite bool   `json:"overwrite"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		link, overwrite = in.URL, overwrite || in.Overwrite
	} else {
		var err error
		if data, err = io.ReadAll(http.MaxBytesReader(w, r.Body, userparts.MaxDownload)); err != nil {
			fail(w, err)
			return
		}
	}
	res, err := s.app.ImportPart(r.Context(), data, link, overwrite)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) deleteUserPart(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteImportedPart(r.PathValue("id"), r.URL.Query().Get("force") == "1"); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) families(w http.ResponseWriter, r *http.Request) {
	fs, err := s.app.Families(r.URL.Query().Get("q"), 0)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, fs)
}

// events streams change notifications (events.Event as JSON) as server-sent events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := s.app.Hub.Subscribe()
	defer s.app.Hub.Unsubscribe(ch)
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		flusher.Flush()
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(v); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), 400)
		return false
	}
	return true
}

func fail(w http.ResponseWriter, err error) {
	code := 400
	switch {
	case errors.Is(err, project.ErrNotFound), errors.Is(err, parts.ErrNotFound):
		code = 404
	case errors.Is(err, project.ErrExists):
		code = 409
	case errors.Is(err, project.ErrTooLarge):
		code = 413
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}
