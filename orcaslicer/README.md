# OrcaSlicer front end

Put STL models on a print plate for the Anycubic Kobra 3, pick OrcaSlicer presets, slice, and print. The server runs the OrcaSlicer 2.4.2 command line for the slicing and talks to the printer's Moonraker API. A Svelte UI (bed view, plates, models, presets, printer) is embedded in the binary. It runs on port 8050 with no auth, so keep it LAN-only.

Models come from uploads or from the OpenSCAD workbench: its "Slice" button (shown when the workbench has `SLICER_URL` set), or `http://localhost:8050/?import=<project>`.

## Connecting an agent

The MCP endpoint is `http://localhost:8050/mcp` (Streamable HTTP, stateless). For Claude Code:

```sh
claude mcp add --transport http orcaslicer http://localhost:8050/mcp
```

Tools:

| Tool | What it does |
| --- | --- |
| `list_profiles`, `get_profile` | Machines, and the processes and filaments that work with them; all settings of one preset (the override keys) |
| `list_openscad_projects`, `import_openscad` | Rendered OpenSCAD models as models here; re-importing replaces the geometry in place |
| `list_models`, `upload_model` | The model library; `upload_model` takes a base64 STL |
| `list_plates`, `get_plate`, `create_plate`, `update_plate` | Plates: presets and overrides (a null override removes that key) |
| `add_object`, `move_object`, `remove_object`, `arrange` | Place objects (x, y, rotation in degrees about X, Y, Z, scale); without x and y an object goes to free space |
| `slice` | Slice and wait for the result: status, time, filament, layers, G-code name, warnings |
| `printer_status` | Online, state, progress, time left, temperatures |
| `print_plate`, `pause_print`, `resume_print`, `cancel_print` | Act on the real printer; each needs `confirm: true` |

The server's MCP instructions give the agent the units, the bed, the origin (front-left) and the workflow.

## How it works

- **Presets.** The OrcaSlicer system profiles are read from the AppImage (`resources/profiles`). Their `inherits` chains are flattened, and the result is what the slicer gets. Overrides are applied on top: each key goes to the process or filament preset that defines it.
- **Placement.** Every object is turned, scaled and moved in the server (`internal/geom`), then written as world-space triangles into a 3MF. OrcaSlicer is run with `--arrange 0`, so it keeps those positions. The 3MF is the plate's download (`plate.3mf`). Checked against the real CLI in `TestSliceHonoursPositions`.
- **Checks before slicing.** An object that hangs over the bed or is taller than the printer stops the slice (OrcaSlicer would refuse it too); overlapping objects are a warning.
- **Slices.** One at a time, in the order asked for. A plate's slice is stale when anything that changes the G-code has changed since (a move, a preset, an override, or a re-import of its OpenSCAD model). Stale slices can't be printed.
- **Printing.** The G-code is uploaded to Moonraker with `print=true`, only when the printer is online and idle.

## Workflow

1. `import_openscad` (or upload an STL), `create_plate`, `add_object`.
2. `arrange` or `move_object` to place it, `update_plate` for overrides such as `sparse_infill_density`.
3. `slice`, read the warnings, look at the bed in the UI.
4. `printer_status`, then `print_plate` with `confirm: true`.

The UI: plates and models on the left, the bed in the middle, presets, settings, the slice and the print button on the right, the printer state in the header. When a slice finishes, the bed switches to a toolpath preview of the G-code (brim, supports, walls, infill coloured by OrcaSlicer's `;TYPE:` markers, a layer slider, features can be switched off; parsed in a Web Worker by `web/src/lib/gcode.js`); "Model" goes back to the objects, and the preview closes by itself once the slice goes stale.

## Storage

`DATA_DIR` (`/data` in the container): `models/<id>.stl` and `<id>.json`, `plates/<id>/plate.json` (the plate), `slice.json` (the last slice and the hash it came from), `plate.gcode` and `plate.3mf`, and `orca-config/` (OrcaSlicer's own config folder, from `--datadir`).

## REST API

`GET /api/config`, `GET /api/profiles?machine=`, `GET /api/profiles/{kind}/{name}`, `GET|POST /api/models`, `GET|DELETE /api/models/{id}`, `GET /api/models/{id}/model.stl`, `GET /api/openscad/projects`, `POST /api/openscad/import`, `GET|POST /api/plates`, `GET|PUT|DELETE /api/plates/{id}`, `POST /api/plates/{id}/objects`, `PATCH|DELETE /api/plates/{id}/objects/{oid}`, `POST /api/plates/{id}/objects/{oid}/duplicate`, `POST /api/plates/{id}/arrange`, `POST|GET /api/plates/{id}/slice`, `GET /api/plates/{id}/plate.gcode` and `plate.3mf` (`?download`), `POST /api/plates/{id}/print`, `GET /api/printer`, `POST /api/printer/{pause|resume|cancel}`, `GET /api/events` (server-sent events: `plate`, `slice`, `models`, `printer`). Errors are `{"error": "..."}`.

Plates from `GET /api/plates/{id}` include, for each object, its world `bounds` and a 16-number column-major `transform` (for three.js `Matrix4.fromArray`), which maps the STL's own coordinates onto the bed.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATA_DIR` | `data` (`/data` in the image) | models, plates, OrcaSlicer config |
| `PORT` | `8050` | UI, REST, MCP |
| `PUBLIC_URL` | `http://localhost:8050` | how people reach the UI (told to the agent) |
| `ORCA` | `orca-slicer` (`/opt/orcaslicer/AppRun` in the image) | the AppRun launcher from the AppImage (it sets the library path); the raw `bin/orca-slicer` needs `LD_LIBRARY_PATH` |
| `ORCA_PROFILES` | `resources/profiles` next to `ORCA` | OrcaSlicer's preset folder |
| `ORCA_VERSION` | `2.4.2` | shown in the UI; part of the slice hash |
| `OPENSCAD_URL` | `http://localhost:8020` | the OpenSCAD workbench API (`http://openscad:8020` in compose) |
| `OPENSCAD_PUBLIC_URL` | `http://localhost:8020` | the workbench as people open it, for links |
| `PRINTER_URL` | empty (printing disabled) | Moonraker, `http://<printer-ip>:7125` |
| `WEBCAM_URL` | empty | optional webcam image shown next to the printer |
| `DEFAULT_MACHINE`, `DEFAULT_PROCESS`, `DEFAULT_FILAMENT` | Kobra 3 0.4 nozzle, 0.20mm Standard, Anycubic PETG | presets new plates start with |
| `SLICE_TIMEOUT` | `15m` | per slice |

## Development

- Extract OrcaSlicer 2.4.2 for local runs: `~/Downloads/OrcaSlicer_Linux_AppImage_Ubuntu2404_V2.4.2.AppImage --appimage-extract` in an empty folder, then move the `squashfs-root` folder to `~/orcaslicer/`. The `run` task uses `~/orcaslicer/squashfs-root/AppRun` unless `ORCA` is set.
- `task orcaslicer:run` builds the UI and serves http://localhost:8050 from `./data`, with printing disabled (`PRINTER_URL` is empty).
- `task orcaslicer:web:dev` runs Vite on http://localhost:5173.
- `task orcaslicer:test`. The positioning test runs the real CLI and is skipped unless `ORCA_TEST_BIN` names the AppRun launcher: `ORCA_TEST_BIN=$HOME/orcaslicer/squashfs-root/AppRun task orcaslicer:test`. If Go is a snap, add `TMPDIR=$HOME/scadtest` (its `/tmp` is private).
- `task orcaslicer:docker:build` builds the image. It runs on `debian:trixie-slim` because the AppImage needs glibc 2.39 or newer. The image checks the AppImage's SHA-256.
- Deploy with the rest of the stack: `task docker:build && task docker:up` (on the server).

Notes from the spike: the CLI slices with no display (no xvfb). `--export-3mf` crashes headless (GTK), which is why the 3MF is written by the server. Orca's default auto brim widens the outline by 5 mm on each side; the positioning test turns it off.
