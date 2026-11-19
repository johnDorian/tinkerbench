package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"strconv"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/measure"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
)

// maxUpload is the request size accepted for an image upload, a little over the per-image
// limit so a request carrying one can report that limit rather than be cut off first.
const maxUpload = project.MaxImageBytes + 1<<20

func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.Images(r.PathValue("p"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

// uploadImages takes a multipart form with one or more "file" parts, which is what a drag
// and drop or a file picker sends. Uploads never replace an existing image: a repeat of
// the same name gets a number.
func (s *Server) uploadImages(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("p")
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "upload: "+err.Error(), 400)
		return
	}
	defer r.MultipartForm.RemoveAll()

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, `no "file" part in the upload`, 400)
		return
	}
	out := []project.Image{}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			fail(w, err)
			return
		}
		b, err := io.ReadAll(io.LimitReader(f, project.MaxImageBytes+1))
		f.Close()
		if err != nil {
			fail(w, err)
			return
		}
		img, err := s.app.UploadImage(p, s.app.Store.UniqueImageName(p, fh.Filename), b, by)
		if err != nil {
			fail(w, err)
			return
		}
		out = append(out, img)
	}
	writeJSON(w, 201, out)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	p, f := r.PathValue("p"), r.PathValue("f")
	if err := project.ValidName(p); err != nil {
		fail(w, err)
		return
	}
	if err := project.ValidImage(f); err != nil {
		fail(w, err)
		return
	}
	if _, err := s.app.Store.ReadImage(p, f); err != nil {
		fail(w, err)
		return
	}
	// ?w= serves a shrunk copy, for thumbnails and for the measuring dialog: the stored
	// file can be tens of megabytes and the browser only ever shows it small.
	if n, _ := strconv.Atoi(r.URL.Query().Get("w")); n > 0 {
		png, _, _, err := s.app.ViewImage(p, f, n)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(png)
		return
	}
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+f+`"`)
	}
	w.Header().Set("Content-Type", project.ContentType(f))
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, s.app.Store.ImagePath(p, f))
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteImage(r.PathValue("p"), r.PathValue("f"), by); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(204)
}

// measureResult is a measurement plus the annotated check image, inline so that one
// request gives the UI everything it needs to show the result.
type measureResult struct {
	*measure.Result
	Image   string `json:"image"`
	Overlay string `json:"overlay,omitempty"` // base64 PNG
}

func (s *Server) measureImage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Image string `json:"image"`
		measure.Options
	}
	body.Overlay = true
	if r.ContentLength != 0 && !readJSON(w, r, &body) {
		return
	}
	res, err := s.app.Measure(r.PathValue("p"), body.Image, body.Options)
	if err != nil {
		fail(w, err)
		return
	}
	out := measureResult{Result: res, Image: body.Image}
	if len(res.Overlay) > 0 {
		out.Overlay = base64.StdEncoding.EncodeToString(res.Overlay)
	}
	writeJSON(w, 200, out)
}
