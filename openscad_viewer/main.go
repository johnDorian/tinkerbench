// OpenSCAD workbench: an agent writes and renders OpenSCAD models over MCP while a person
// watches the code and the 3D model update live in the web UI.
//
// Environment:
//
//	DATA_DIR            folder with projects/ (default ./data, /data in the container)
//	PORT                web UI + REST API + MCP at /mcp, no auth (default 8020)
//	OPENSCAD            openscad binary (default openscad)
//	RENDER_TIMEOUT      max time per openscad run (default 5m)
//	RENDER_CONCURRENCY  openscad processes at once (default 2)
//	BED_SIZE            print bed in mm, shown in the viewer and told to the agent (default 220x220)
//	PUBLIC_URL          how people reach the UI, told to the agent (default http://localhost:8020)
//	SLICER_URL          the OrcaSlicer front end as people open it; shows a Slice button (default none)
//	OPENSCAD_ARGS      extra openscad arguments for every run, e.g. --backend=manifold (default none)
//
// The helper library (workbench.scad) is written to DATA_DIR/lib at start-up and put on OPENSCADPATH.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/app"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/mcp"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/server"
)

// The Svelte app, built by `npm run build` in web/ (the Docker build does this).
//
//go:embed all:web/dist
var webFS embed.FS

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := env("DATA_DIR", "data")
	store, err := project.Open(filepath.Join(dataDir, "projects"))
	if err != nil {
		log.Fatalf("open data dir %s: %v", dataDir, err)
	}
	timeout, err := time.ParseDuration(env("RENDER_TIMEOUT", "5m"))
	if err != nil {
		log.Fatalf("RENDER_TIMEOUT: %v", err)
	}
	concurrency, _ := strconv.Atoi(env("RENDER_CONCURRENCY", "2"))
	var bed [2]float64
	if _, err := fmt.Sscanf(env("BED_SIZE", "220x220"), "%gx%g", &bed[0], &bed[1]); err != nil {
		log.Fatalf("BED_SIZE: want e.g. 220x220: %v", err)
	}
	renderer := render.New(env("OPENSCAD", "openscad"), timeout, concurrency)
	renderer.ExtraArgs = strings.Fields(os.Getenv("OPENSCAD_ARGS"))
	renderer.LibDir = filepath.Join(dataDir, "lib")
	if err := render.InstallLibrary(renderer.LibDir); err != nil {
		log.Fatalf("install workbench.scad into %s: %v", renderer.LibDir, err)
	}
	version := renderer.Version()
	log.Printf("openscad: %s", version)

	a := app.New(store, renderer, events.NewHub())
	publicURL := env("PUBLIC_URL", "http://localhost:8020")
	m := mcp.New(a, publicURL, fmt.Sprintf("%gx%g", bed[0], bed[1]), version)
	web, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	api := server.New(a, m, web, server.Config{Bed: bed, OpenSCADVersion: version, MCPURL: publicURL + "/mcp", SlicerURL: strings.TrimRight(os.Getenv("SLICER_URL"), "/")})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: ":" + env("PORT", "8020"), Handler: api.Routes()}
	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdown) // open event streams are cut off by the timeout
}
