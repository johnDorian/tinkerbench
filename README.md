# tinkerbench

> **A completely vibe-coded project.** Everything here was written by an AI agent, with the aim of making prototyping quick and easy rather than producing polished software. Expect rough edges.

Workbenches for hobby electronics and 3D printing that an AI agent drives over MCP while you watch in the browser. Each one is a small Go server with the real tool (OpenSCAD, Fritzing, OrcaSlicer) in its Docker image and a Svelte UI embedded in the binary.

| App | What it does | Port |
|---|---|---|
| [openscad_viewer/](openscad_viewer/) | The agent writes and renders OpenSCAD models; you see code and a 3D view update live and comment on lines or faces. Includes `measure_part`, which turns a scan or photo of a real part into millimetres. | 8020 |
| [fritzing/](fritzing/) | The agent builds Fritzing sketches (breadboard, schematic, PCB); the three views redraw live. | 8030 |
| [orcaslicer/](orcaslicer/) | Put STL models (uploaded or from the OpenSCAD workbench) on a plate, slice with the OrcaSlicer CLI, and print through Moonraker. | 8050 |

Every app serves its web UI, a REST API and an MCP endpoint at `/mcp` on one port, **without authentication**. Run them on a trusted network only.

## Quick start

```sh
cp .env.example .env    # optional: PRINTER_URL, BED_SIZE
docker compose up -d --build
```

Then open http://localhost:8020, http://localhost:8030 and http://localhost:8050, and connect your agent, e.g. for Claude Code:

```sh
claude mcp add --transport http openscad   http://localhost:8020/mcp
claude mcp add --transport http fritzing   http://localhost:8030/mcp
claude mcp add --transport http orcaslicer http://localhost:8050/mcp
```

[skills/](skills/) has agent skills for using the workbenches well (`task skills:install`).

Data lives in `./data/<app>`. The images are amd64 only.

## Connecting the apps

- `SLICER_URL` on the OpenSCAD workbench (the slicer as the browser reaches it) shows a "Slice" button that opens the current model in the slicer.
- `OPENSCAD_URL` (server to server) and `OPENSCAD_PUBLIC_URL` (for links) on the slicer let it import STLs from the workbench.
- `PRINTER_URL` on the slicer points to a Moonraker API. Without it, slicing works and printing is disabled. The default presets are for an Anycubic Kobra 3; change them with `DEFAULT_MACHINE`, `DEFAULT_PROCESS` and `DEFAULT_FILAMENT`.

Each app's README lists all its settings.

## Development

Needs Go, Node and [Task](https://taskfile.dev). `task --list` shows everything; the main ones:

- `task test`: Go tests of all three apps (see each README for the test data some tests need).
- `task openscad:run`, `task orcaslicer:run`: run locally against `./data`.
- `task fritzing:docker:run`: Fritzing only runs in its image.
- `task <app>:web:dev`: Vite dev server with hot reload.
- `task hooks:install`: once per clone, enables a pre-commit hook that runs [gitleaks](https://github.com/gitleaks/gitleaks) on the staged changes (`go install github.com/zricethezav/gitleaks/v8@latest`). `task secrets:scan` checks the whole history.
