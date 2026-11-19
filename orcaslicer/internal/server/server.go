// Package server is the REST API, the event stream for the web UI and the MCP endpoint.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/app"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
)

const maxUpload = 64 << 20

// Server wires the app to HTTP.
type Server struct {
	app *app.App
	mcp http.Handler
	web fs.FS
}

// New returns the HTTP server. web is the built UI (index.html and assets/ at its root).
func New(a *app.App, mcp http.Handler, web fs.FS) *Server {
	return &Server{app: a, mcp: mcp, web: web}
}

// Routes returns the handler for every path.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(s.web))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/mcp", s.mcp)

	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/profiles", s.listProfiles)
	mux.HandleFunc("GET /api/profiles/{kind}/{name}", s.profile)
	mux.HandleFunc("GET /api/models", s.listModels)
	mux.HandleFunc("POST /api/models", s.uploadModel)
	mux.HandleFunc("GET /api/models/{id}", s.getModel)
	mux.HandleFunc("GET /api/models/{id}/model.stl", s.modelSTL)
	mux.HandleFunc("DELETE /api/models/{id}", s.deleteModel)
	mux.HandleFunc("GET /api/openscad/projects", s.openscadProjects)
	mux.HandleFunc("POST /api/openscad/import", s.openscadImport)
	mux.HandleFunc("GET /api/plates", s.listPlates)
	mux.HandleFunc("POST /api/plates", s.createPlate)
	mux.HandleFunc("GET /api/plates/{id}", s.getPlate)
	mux.HandleFunc("PUT /api/plates/{id}", s.putPlate)
	mux.HandleFunc("DELETE /api/plates/{id}", s.deletePlate)
	mux.HandleFunc("POST /api/plates/{id}/objects", s.addObject)
	mux.HandleFunc("PATCH /api/plates/{id}/objects/{oid}", s.patchObject)
	mux.HandleFunc("DELETE /api/plates/{id}/objects/{oid}", s.removeObject)
	mux.HandleFunc("POST /api/plates/{id}/objects/{oid}/duplicate", s.duplicateObject)
	mux.HandleFunc("POST /api/plates/{id}/arrange", s.arrange)
	mux.HandleFunc("POST /api/plates/{id}/slice", s.slice)
	mux.HandleFunc("GET /api/plates/{id}/slice", s.sliceStatus)
	mux.HandleFunc("GET /api/plates/{id}/plate.gcode", s.plateFile("plate.gcode", "application/octet-stream"))
	mux.HandleFunc("GET /api/plates/{id}/plate.3mf", s.plateFile("plate.3mf", "model/3mf"))
	mux.HandleFunc("POST /api/plates/{id}/print", s.print)
	mux.HandleFunc("GET /api/printer", s.printer)
	mux.HandleFunc("POST /api/printer/{action}", s.printerAction)
	return mux
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.app.Config()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, cfg)
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.ListProfiles(r.URL.Query().Get("machine"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.Profile(r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.ListModels()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) uploadModel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		writeJSON(w, 400, errBody(fmt.Errorf("upload: %w", err)))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, errBody(fmt.Errorf("upload a file in the form field \"file\"")))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		fail(w, err)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, ".stl")
	}
	m, err := s.app.UploadModel(name, data)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, m)
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.GetModel(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) modelSTL(w http.ResponseWriter, r *http.Request) {
	path, err := s.app.ModelSTLPath(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "model/stl")
	w.Header().Set("Cache-Control", "no-cache")
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.stl"`, r.PathValue("id")))
	}
	http.ServeFile(w, r, path)
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteModel(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) openscadProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.OpenSCADProjects(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) openscadImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
		Plate   string `json:"plate"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Project == "" {
		writeJSON(w, 400, errBody(fmt.Errorf("project is required")))
		return
	}
	m, err := s.app.ImportOpenSCAD(r.Context(), body.Project, body.Plate)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) listPlates(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.ListPlates()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createPlate(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Machine, Process, Filament string }
	if !readJSON(w, r, &body) {
		return
	}
	p, err := s.app.CreatePlate(body.Name, body.Machine, body.Process, body.Filament)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) getPlate(w http.ResponseWriter, r *http.Request) {
	p, err := s.app.GetPlate(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) putPlate(w http.ResponseWriter, r *http.Request) {
	var body plates.Plate
	if !readJSON(w, r, &body) {
		return
	}
	p, err := s.app.PutPlate(r.PathValue("id"), body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) deletePlate(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeletePlate(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// number fields are pointers so a missing one is distinguishable from 0
type objectBody struct {
	Model    string      `json:"model"`
	X        *float64    `json:"x"`
	Y        *float64    `json:"y"`
	Rotation *[3]float64 `json:"rotation"`
	Scale    *float64    `json:"scale"`
	Name     *string     `json:"name"`
}

func (s *Server) addObject(w http.ResponseWriter, r *http.Request) {
	var body objectBody
	if !readJSON(w, r, &body) {
		return
	}
	p, err := s.app.AddObject(r.PathValue("id"), app.ObjectSpec{Model: body.Model, X: body.X, Y: body.Y, Rotation: body.Rotation, Scale: body.Scale})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) patchObject(w http.ResponseWriter, r *http.Request) {
	var body objectBody
	if !readJSON(w, r, &body) {
		return
	}
	p, err := s.app.PatchObject(r.PathValue("id"), r.PathValue("oid"), app.ObjectPatch{
		Name: body.Name, X: body.X, Y: body.Y, Rotation: body.Rotation, Scale: body.Scale,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) removeObject(w http.ResponseWriter, r *http.Request) {
	p, err := s.app.RemoveObject(r.PathValue("id"), r.PathValue("oid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) duplicateObject(w http.ResponseWriter, r *http.Request) {
	p, err := s.app.DuplicateObject(r.PathValue("id"), r.PathValue("oid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) arrange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Spacing *float64 `json:"spacing"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &body) {
		return
	}
	p, err := s.app.Arrange(r.PathValue("id"), body.Spacing)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) slice(w http.ResponseWriter, r *http.Request) {
	res, err := s.app.Slice(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, res)
}

func (s *Server) sliceStatus(w http.ResponseWriter, r *http.Request) {
	res, err := s.app.SliceStatus(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) plateFile(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path, err := s.app.PlateFile(r.PathValue("id"), name)
		if err != nil {
			fail(w, err)
			return
		}
		if name == "plate.gcode" {
			if res, err := s.app.SliceStatus(r.PathValue("id")); err == nil && res.GcodeName != "" {
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, res.GcodeName))
			}
		} else if r.URL.Query().Has("download") {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.3mf"`, r.PathValue("id")))
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, path)
	}
}

func (s *Server) print(w http.ResponseWriter, r *http.Request) {
	name, err := s.app.PrintPlate(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"filename": name})
}

func (s *Server) printer(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.app.PrinterStatus(r.Context()))
}

func (s *Server) printerAction(w http.ResponseWriter, r *http.Request) {
	if err := s.app.PrinterAction(r.Context(), r.PathValue("action")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.app.PrinterStatus(r.Context()))
}

// events streams change notifications as server-sent events.
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v); err != nil {
		writeJSON(w, 400, errBody(fmt.Errorf("invalid JSON: %w", err)))
		return false
	}
	return true
}

func errBody(err error) map[string]string { return map[string]string{"error": err.Error()} }

func fail(w http.ResponseWriter, err error) {
	writeJSON(w, app.StatusOf(err), errBody(err))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}
