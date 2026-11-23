package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/mat"
)

// matInfo is one entry of GET /api/mats.
type matInfo struct {
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Paper   string  `json:"paper"`
	InnerMM float64 `json:"inner_mm"`
	URL     string  `json:"url"`
}

func (s *Server) listMats(w http.ResponseWriter, r *http.Request) {
	list := []matInfo{}
	for _, m := range mat.All() {
		list = append(list, matInfo{Name: m.Name, Title: m.Title, Paper: m.Paper, InnerMM: m.InnerMM, URL: "/api/mats/" + m.Name + ".pdf"})
	}
	writeJSON(w, 200, list)
}

// getMat serves one mat as a PDF to download. The PDF is built from the name alone and
// is the same bytes every time, so the ETag is a hash of them and a repeat download with
// If-None-Match gets a 304.
func (s *Server) getMat(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".pdf")
	if !ok {
		http.NotFound(w, r)
		return
	}
	m, ok := mat.ByName(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	pdf := m.PDF()
	sum := sha256.Sum256(pdf)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="measuring-mat-`+m.Name+`.pdf"`)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:8])+`"`)
	http.ServeContent(w, r, m.Name+".pdf", time.Time{}, bytes.NewReader(pdf))
}
