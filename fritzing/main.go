// Fritzing workbench: an agent builds electronics sketches over MCP while a person watches the
// breadboard, schematic and PCB views update live in the web UI. Fritzing itself runs as a
// subprocess to draw the SVGs.
//
// Environment:
//
//	DATA_DIR         folder with projects/ and userparts/ (imported parts) (default ./data, /data in the container)
//	PORT             web UI + REST API + MCP at /mcp, no auth (default 8030)
//	FRITZING_BIN     the Fritzing executable (default Fritzing)
//	FRITZING_HOME    HOME for Fritzing, holding .local/lib/fritzing/fritzing-parts (default $HOME)
//	PARTS_DB         parts.db (default $FRITZING_HOME/.local/lib/fritzing/fritzing-parts/parts.db)
//	PARTS_DIR        Fritzing's parts folder, with core/ and svg/ (default /usr/share/fritzing/parts)
//	RSVG_CONVERT     rsvg-convert, for PNG snapshots (default rsvg-convert)
//	RENDER_TIMEOUT   max time per Fritzing run (default 2m)
//	PUBLIC_URL       how people reach the UI, told to the agent (default http://localhost:8030)
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/johnDorian/tinkerbench/fritzing/internal/app"
	"github.com/johnDorian/tinkerbench/fritzing/internal/events"
	"github.com/johnDorian/tinkerbench/fritzing/internal/mcp"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/runner"
	"github.com/johnDorian/tinkerbench/fritzing/internal/server"
	"github.com/johnDorian/tinkerbench/fritzing/internal/userparts"
)

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
	timeout, err := time.ParseDuration(env("RENDER_TIMEOUT", "2m"))
	if err != nil {
		log.Fatalf("RENDER_TIMEOUT: %v", err)
	}
	home := env("FRITZING_HOME", os.Getenv("HOME"))
	r := &runner.Runner{Bin: env("FRITZING_BIN", "Fritzing"), Home: home, Timeout: timeout}
	if p, err := exec.LookPath(env("RSVG_CONVERT", "rsvg-convert")); err == nil {
		r.Rsvg = p
	} else {
		log.Printf("rsvg-convert not found: PNG snapshots are unavailable")
	}
	if _, err := exec.LookPath(r.Bin); err != nil && !filepath.IsAbs(r.Bin) {
		log.Printf("warning: %s not found, renders will fail", r.Bin)
	}

	dbPath := env("PARTS_DB", filepath.Join(home, ".local/lib/fritzing/fritzing-parts/parts.db"))
	cat, err := parts.Open(dbPath, env("PARTS_DIR", "/usr/share/fritzing/parts"))
	if err != nil {
		log.Printf("parts library unavailable (%v): part search and wiring are disabled", err)
		cat = nil
	} else {
		log.Printf("parts library: %d parts", cat.Count)
	}

	users, err := userparts.Open(filepath.Join(dataDir, "userparts"))
	if err != nil {
		log.Printf("imported parts unavailable: %v", err)
		users = nil
	} else {
		log.Printf("imported parts: %d", users.Count())
	}

	a := &app.App{Store: store, Runner: r, Catalog: cat, Users: users, Hub: events.NewHub()}
	publicURL := env("PUBLIC_URL", "http://localhost:8030")
	version := "0.9.6"
	cfg := server.Config{FritzingVersion: version, MCPURL: publicURL + "/mcp"}
	if cat != nil {
		cfg.Parts = cat.Count
	}
	m := mcp.New(a, publicURL, version)
	web, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	api := server.New(a, m, web, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: ":" + env("PORT", "8030"), Handler: api.Routes()}
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
