// OrcaSlicer front end for the Anycubic Kobra 3: an agent or a person places STL models on a
// plate, picks OrcaSlicer presets, slices with the OrcaSlicer command line and prints through
// Moonraker. The web UI is embedded; the REST API, the event stream and MCP are on the same port.
//
// Environment:
//
//	DATA_DIR            models/, plates/ and the OrcaSlicer config live here (default ./data, /data in the container)
//	PORT                web UI + REST API + MCP at /mcp, no auth (default 8050)
//	PUBLIC_URL          how people reach the UI, told to the agent (default http://localhost:8050)
//	ORCA                OrcaSlicer launcher: AppRun from the AppImage, or bin/orca-slicer (default orca-slicer)
//	ORCA_PROFILES       OrcaSlicer's resources/profiles (default: next to ORCA)
//	ORCA_VERSION        OrcaSlicer version, shown in the UI and part of the plate hash (default 2.4.2)
//	OPENSCAD_URL        OpenSCAD workbench API (default http://localhost:8020)
//	OPENSCAD_PUBLIC_URL the workbench as people open it, for links (default http://localhost:8020)
//	PRINTER_URL         Moonraker, e.g. http://<printer-ip>:7125; empty disables printing
//	WEBCAM_URL          optional webcam image or stream shown next to the printer
//	DEFAULT_MACHINE, DEFAULT_PROCESS, DEFAULT_FILAMENT   presets new plates start with
//	SLICE_TIMEOUT       max time per slice (default 15m)
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
	"syscall"
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/app"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/events"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/mcp"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/models"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/openscad"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/plates"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/printer"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/profiles"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/server"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/slicer"
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

// profilesDir finds OrcaSlicer's resources/profiles: $ORCA_PROFILES, or next to the launcher.
func profilesDir(orca string) string {
	if d := os.Getenv("ORCA_PROFILES"); d != "" {
		return d
	}
	dir := filepath.Dir(orca)
	for _, c := range []string{filepath.Join(dir, "resources", "profiles"), filepath.Join(dir, "..", "resources", "profiles")} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return filepath.Join(dir, "resources", "profiles")
}

func main() {
	dataDir := env("DATA_DIR", "data")
	modelStore, err := models.Open(dataDir)
	if err != nil {
		log.Fatalf("open data dir %s: %v", dataDir, err)
	}
	plateStore, err := plates.Open(dataDir)
	if err != nil {
		log.Fatalf("open data dir %s: %v", dataDir, err)
	}
	timeout, err := time.ParseDuration(env("SLICE_TIMEOUT", "15m"))
	if err != nil {
		log.Fatalf("SLICE_TIMEOUT: %v", err)
	}
	orca := env("ORCA", "orca-slicer")
	profs := profiles.Open(profilesDir(orca))

	hub := events.NewHub()
	a := &app.App{
		Cfg: app.Config{
			PublicURL:         env("PUBLIC_URL", "http://localhost:8050"),
			OpenSCADPublicURL: env("OPENSCAD_PUBLIC_URL", "http://localhost:8020"),
			WebcamURL:         os.Getenv("WEBCAM_URL"),
			OrcaVersion:       env("ORCA_VERSION", "2.4.2"),
			Machine:           env("DEFAULT_MACHINE", "Anycubic Kobra 3 0.4 nozzle"),
			Process:           env("DEFAULT_PROCESS", "0.20mm Standard @Anycubic Kobra 3 0.4 nozzle"),
			Filament:          env("DEFAULT_FILAMENT", "Anycubic PETG @Anycubic Kobra 3 0.4 nozzle"),
		},
		Profiles: profs,
		Models:   modelStore,
		Plates:   plateStore,
		OpenSCAD: openscad.New(env("OPENSCAD_URL", "http://localhost:8020")),
		Printer:  printer.New(os.Getenv("PRINTER_URL")),
		Hub:      hub,
	}
	a.Slicer = slicer.New(slicer.Options{
		Binary:   orca,
		DataDir:  dataDir,
		Timeout:  timeout,
		Profiles: profs,
		Models:   modelStore,
		Plates:   plateStore,
		Version:  a.Cfg.OrcaVersion,
	})
	a.Wire()
	if err := a.Slicer.Recover(); err != nil {
		log.Fatalf("slicer: %v", err)
	}
	if err := profs.CheckNames(a.Cfg.Machine, a.Cfg.Process, a.Cfg.Filament); err != nil {
		log.Printf("warning: default presets: %v (set ORCA_PROFILES / DEFAULT_*)", err)
	}

	cfg, err := a.Config()
	bedText := "unknown size"
	if err == nil {
		bedText = fmt.Sprintf("%gx%g mm, %g mm high", cfg.Bed.Width, cfg.Bed.Depth, cfg.Bed.MaxHeight)
	} else {
		log.Printf("warning: config: %v", err)
	}
	log.Printf("orcaslicer %s, profiles %s, printer %q", a.Cfg.OrcaVersion, profs.Root(), os.Getenv("PRINTER_URL"))

	if err := a.DropBedOverrides(); err != nil {
		log.Fatal(err)
	}
	m := mcp.New(a, a.Cfg.PublicURL, bedText)
	web, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	api := server.New(a, m, web)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go a.Slicer.Run(ctx)
	go a.Poll(ctx, 5*time.Second)

	srv := &http.Server{Addr: ":" + env("PORT", "8050"), Handler: api.Routes()}
	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
}
