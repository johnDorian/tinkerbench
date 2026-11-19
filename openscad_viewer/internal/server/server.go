// Package server is the REST API and event stream for the web UI, plus the MCP endpoint.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/app"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

// Config is what the UI needs to know about the server.
type Config struct {
	Bed             [2]float64 `json:"bed"` // print bed size in mm (x, y)
	OpenSCADVersion string     `json:"openscad_version"`
	MCPURL          string     `json:"mcp_url"`
	SlicerURL       string     `json:"slicer_url,omitempty"` // OrcaSlicer front end; the UI's Slice button opens SlicerURL/?import=<project>
}

type Server struct {
	app *app.App
	mcp http.Handler
	web fs.FS // built Svelte app (index.html + assets/)
	cfg Config
}

func New(a *app.App, mcp http.Handler, web fs.FS, cfg Config) *Server {
	return &Server{app: a, mcp: mcp, web: web, cfg: cfg}
}

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
	mux.HandleFunc("GET /api/projects/{p}/model.stl", s.stl)
	mux.HandleFunc("GET /api/projects/{p}/snapshot.png", s.snapshot)
	mux.HandleFunc("GET /api/projects/{p}/history", s.history)
	mux.HandleFunc("GET /api/projects/{p}/history/{n}", s.version)
	mux.HandleFunc("POST /api/projects/{p}/history/{n}/restore", s.restore)
	mux.HandleFunc("GET /api/projects/{p}/images", s.listImages)
	mux.HandleFunc("POST /api/projects/{p}/images", s.uploadImages)
	mux.HandleFunc("GET /api/projects/{p}/images/{f}", s.getImage)
	mux.HandleFunc("DELETE /api/projects/{p}/images/{f}", s.deleteImage)
	mux.HandleFunc("POST /api/projects/{p}/measure", s.measureImage)
	mux.HandleFunc("GET /api/projects/{p}/parts", s.listParts)
	mux.HandleFunc("GET /api/projects/{p}/parts/{file}", s.partSTL)
	mux.HandleFunc("GET /api/projects/{p}/comments", s.listComments)
	mux.HandleFunc("POST /api/projects/{p}/comments", s.addComment)
	mux.HandleFunc("POST /api/projects/{p}/comments/{id}/replies", s.replyComment)
	mux.HandleFunc("POST /api/projects/{p}/comments/{id}/resolve", s.resolveComment)
	mux.HandleFunc("POST /api/projects/{p}/comments/{id}/apply", s.applySuggestion)
	mux.HandleFunc("DELETE /api/projects/{p}/comments/{id}", s.deleteComment)
	return mux
}

const by = "web"

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

// projectView is everything the UI shows for one project.
type projectView struct {
	project.Info
	Content    []project.File    `json:"content"`
	LastRender *render.Result    `json:"last_render"`
	Comments   []project.Comment `json:"comments"` // including resolved ones
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
	comments, err := s.app.Store.Comments(p, "", true)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, projectView{Info: info, Content: files, LastRender: s.app.LastRender(p), Comments: comments})
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
	var body struct {
		Content string `json:"content"`
		Note    string `json:"note"`
	}
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
	var body struct {
		Params    map[string]any    `json:"params"`     // JSON values; omitted: keep the stored params
		RawParams map[string]string `json:"raw_params"` // OpenSCAD expressions (the UI's params editor)
	}
	if r.ContentLength != 0 && !readJSON(w, r, &body) {
		return
	}
	params := body.RawParams
	if body.Params != nil {
		var err error
		if params, err = app.FormatParams(body.Params); err != nil {
			fail(w, err)
			return
		}
	}
	res, err := s.app.Render(r.Context(), r.PathValue("p"), params, by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) stl(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	if err := project.ValidName(p); err != nil {
		fail(w, err)
		return
	}
	path := render.STLPath(s.app.Store.Dir(p))
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "not rendered yet", 404)
		return
	}
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.stl"`, p))
	}
	w.Header().Set("Content-Type", "model/stl")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	view := q.Get("view")
	if view == "" {
		view = "iso"
	}
	width, _ := strconv.Atoi(q.Get("w"))
	height, _ := strconv.Atoi(q.Get("h"))
	pngs, err := s.app.Snapshot(r.Context(), r.PathValue("p"), []string{view}, width, height, q.Get("source") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(pngs[0])
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

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	cs, err := s.app.Store.Comments(r.PathValue("p"), r.URL.Query().Get("file"), r.URL.Query().Has("all"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, cs)
}

// listParts is the named pieces of the model and its features (see package parts).
func (s *Server) listParts(w http.ResponseWriter, r *http.Request) {
	l, err := s.app.Parts(r.PathValue("p"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, l)
}

// partSTL serves one part's binary STL: /parts/<name>.stl, the name URL-encoded.
func (s *Server) partSTL(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".stl")
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	path, err := s.app.PartFile(r.PathValue("p"), name)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "model/stl")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File       string          `json:"file"`
		Line       int             `json:"line"`
		EndLine    int             `json:"end_line"`
		Body       string          `json:"body"`
		Suggestion *string         `json:"suggestion"`
		Anchor     *project.Anchor `json:"anchor"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	c, err := s.app.AddComment(r.PathValue("p"), body.File, body.Line, body.EndLine, body.Body, body.Suggestion, body.Anchor, by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, c)
}

func commentID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad comment id", 400)
		return 0, false
	}
	return id, true
}

func (s *Server) replyComment(w http.ResponseWriter, r *http.Request) {
	id, ok := commentID(w, r)
	var body struct{ Body string }
	if !ok || !readJSON(w, r, &body) {
		return
	}
	c, err := s.app.ReplyComment(r.PathValue("p"), id, body.Body, by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) resolveComment(w http.ResponseWriter, r *http.Request) {
	id, ok := commentID(w, r)
	var body struct{ Resolved bool }
	if !ok || !readJSON(w, r, &body) {
		return
	}
	c, err := s.app.ResolveComment(r.PathValue("p"), id, body.Resolved, by)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) applySuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := commentID(w, r)
	if !ok {
		return
	}
	c, res, err := s.app.ApplySuggestion(r.Context(), r.PathValue("p"), id, by, app.RenderAsync)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"comment": c, "version": res.Version})
}

func (s *Server) deleteComment(w http.ResponseWriter, r *http.Request) {
	id, ok := commentID(w, r)
	if !ok {
		return
	}
	if err := s.app.DeleteComment(r.PathValue("p"), id, by); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
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
	ping := time.NewTicker(25 * time.Second) // keeps proxies from closing an idle stream
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), 400)
		return false
	}
	return true
}

func fail(w http.ResponseWriter, err error) {
	code := 400
	switch {
	case errors.Is(err, project.ErrNotFound):
		code = 404
	case errors.Is(err, project.ErrExists), errors.Is(err, project.ErrOutdated):
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
