# Fritzing workbench

An agent builds electronics sketches (breadboard, schematic, PCB) over MCP and you watch the three views redraw live in the browser. A Go server with Fritzing in the same container and a Svelte UI embedded in the binary. Port 8030, no auth, keep it LAN-only.

## How it works

- A project is a folder in `<DATA_DIR>/projects/<name>/` with `sketch.fz` (Fritzing's XML), optional custom parts (`*.fzp`, `*.svg`), per-save history in `.history/`, renders in `out/`. Download as `.fzz` (a zip of those files) from the UI.
- **Rendering** runs `Fritzing -svg <folder>` on a temporary `.fzz` and stores `breadboard.svg`, `schematic.svg`, `pcb.svg`. It takes about 3-7 s; only one render runs at a time. Qt uses the offscreen platform plugin, no X server. A failed render keeps the last good SVGs. Parts that are not in the library are caught before Fritzing starts, since it would open a dialog nobody can click.
- **Fritzing is Debian's 0.9.6** (amd64 in the image, the server is an Intel Mac mini, so no arm images are needed). Its `/usr/bin/fritzing` wrapper drops all arguments, so the Dockerfile prepares the private HOME the wrapper would create and the server calls the real binary.
- **Parts search** reads Fritzing's own `parts.db` (SQLite, read-only, pure-Go driver) with the title, description, tags, family and properties; the part's `.fzp` gives the connectors and the artwork `.svg` gives the pin positions.
- **Imported parts** live apart from Fritzing's library in `<DATA_DIR>/userparts/<module id>/`, one `.fzpz` at a time (`import_part` over MCP, `POST /api/userparts`, or the "Import a part" box in the UI; from a public https URL, GitHub blob links work, or an upload). The bundle is validated (one `.fzp`, artwork present and sized, no `<script>`, module id not in Fritzing's library); pins whose artwork element is missing come back as warnings. `search_parts` searches both, imported parts first (`source=user` lists only them, `library` only Fritzing's). When a sketch uses an imported part its files are copied into the `.fzz` that Fritzing renders (and the one you download), which is how Fritzing carries custom parts itself. Deleting a part that a sketch uses needs `force`. URLs that resolve to loopback or LAN addresses are refused. Community parts vary: old (0.7-era) schematic artwork can make Fritzing leave a part out of the schematic view, which shows up as a render warning.
- **Editing** is done by `internal/sketch` on a parsed copy of the XML, keeping attribute order: add a part (in every view it has), move/rotate, set properties, rename, remove (with attached wires), connect (a wire in the breadboard view, a trace in schematic, a copper trace in pcb, placed on the pin positions read from the SVGs). `describe_sketch` lists parts, wires and the nets they form. Anything else: `edit_file` / `write_file` on the raw XML, validated before it is saved.

## Connecting an agent

```sh
claude mcp add --transport http fritzing http://localhost:8030/mcp
```

| Tool | What it does |
| --- | --- |
| `search_parts`, `get_part`, `part_image`, `list_families`, `property_values` | Find parts in the library (and imported ones), see connectors and artwork |
| `import_part`, `delete_user_part` | Add one part from a `.fzpz` (URL or base64) to the imported-parts library; remove it |
| `create_project`, `list_projects`, `get_project`, `describe_sketch` | Projects and what is in a sketch (parts, wires, nets) |
| `add_part`, `move_part`, `set_property`, `rename_part`, `remove`, `connect` | Edit the sketch; each is a version and renders |
| `get_wiring`, `set_notes` | The readable wiring description (Markdown) and its written notes |
| `check_layout` | Geometry checks and tidiness numbers for a view without a picture; `connect`, `move_part`, `reroute_wire` report the same findings |
| `align_parts`, `place_near`, `reroute_wire`, `tidy_view` | Layout helpers, see below |
| `render`, `snapshot` | Render again; PNGs of the views (`snapshot` says when they are older than the sketch). `snapshot` can zoom: `around` a part or wire title (+ `margin_mm`) or a `region` in mm, one view, a fraction of the size |
| `check_circuit` | Electrical sanity checks by pin names (supply shorted to ground, two rails joined, no ground, LED without resistor, ESP8266 boot pins) |
| `diff_versions` | What changed between two sketch versions, in words: parts, nets, re-routed wires, layout numbers and problems before/after |
| `read_file`, `write_file`, `edit_file`, `delete_file` | Raw XML and custom parts |
| `list_versions`, `get_version`, `restore_version`, `set_description` | History, one-line description |

## Wiring description

Every project has a **wiring** tab (and `get_wiring` / `GET /api/projects/{p}/wiring.md`) that can be read cold, without the chat history. It has two layers:

- **Generated** from `sketch.fz` and the parts library each time, never stored: parts list with values (`1 kΩ`), connections table with human pin names (`Q1.B (base)`) and breadboard wire colours, nets named `GND` / `5 V` / `3.3 V` where obvious, pins in use per part, and warnings: a connection drawn in one view only ("wired in schematic only"), open wire ends, unconnected parts, parts missing from a view, and the layout findings below. `?view=` picks the view for the table (default breadboard).
- **Written notes** (`notes.md`, Markdown): purpose, power, build steps, what to check on the real parts. Written with `set_notes` or edited in the tab. It is an ordinary versioned file (history, restore, activity feed) but is left out of the `.fzz` Fritzing renders and out of the download, and saving never renders. Each save is stamped with the sketch version (`<!-- written against sketch v34 -->`); when the sketch moves on the page shows "notes written at v34, sketch is now v52: check them", and `get_project` / `list_projects` carry `has_notes` and `notes_stale`.

`describe_sketch` stays the raw view for checking; `get_wiring` is the readable one. A project that has notes opens on the wiring tab.

## Layout checks

`check_layout(project, view)` (also in the wiring warnings and in the result of `connect` / `move_part`) runs on the pin positions the server already reads from the part SVGs, so no screenshot is needed:

- **error** `pin-contact`: pins of different parts touch while on different nets (can short); `wire-over-pin`: a wire runs over a pin of another net.
- `crossing`: two wires cross. `info` in breadboard and schematic (wires only connect at their ends, so a crossing is not a connection; costs 1 tidiness point), `warn` on a PCB (same-layer copper would short); `wire-over-part`: a wire passes across a part it is not attached to; `parts-overlap`: artwork boxes overlap (silkscreen can be a little larger than the pins). A part sitting on a breadboard or PCB is not an overlap.
- `parallel-close`: wires of different nets run side by side for more than 3 mm: `warn` under 1 mm (they read as one wire), `info` under 2 mm. A breadboard crossing of two wires of the *same colour* is a `warn` too (nothing electrical, but it looks joined).
- **numbers**: wire count, total wire length and crossings per view, so "better or worse" after a move is a number.

Wires are straight segments, so intersection is exact; a wire with bends is a chain of segments and counts as one connection. A wire that ends on a part but cuts through its body is reported as `wire-over-part`. Info-level findings: `diagonal`, `long-wire`, `wire-near-pin`. Numbers per view: wires, bends, crossings, orthogonal share, total length, bounding box of the parts and a `tidiness` score 0-100 (100 minus 20 per error, 10 per warning, 1 per breadboard/schematic crossing, 3 per diagonal segment, 0.5 per bend, 1 per long wire).

## Checks that replace a screenshot

- **Artwork audit** (every render): Fritzing's export re-writes each part's SVG and drops some transforms (the WeMos D1 mini draws its pin rings in `rotate(90 cx cy)` groups: the rings land in a row beside the board). After each render the server compares where every connector is *drawn* in `out/<view>.svg` with where the part's own SVG puts it and adds an `art-mismatch` warning to the render result (so `add_part`, `connect`, ... show it) when one is more than 1 mm off. Fix: an imported copy of the part with the transforms flattened into `matrix(...)`.
- **`check_circuit`**: classifies pins by name (ground: `G`/`GND`/`VSS`; rails: `5V`/`VBUS`, `3V3`, `VCC`/`VDD`, `VIN`; `G` on a part whose description says gate is not ground). `error` `power-short` (supply and ground on one net), `rail-short` (5 V and 3.3 V joined); `warn` `no-ground`, `led-no-resistor`; `info` `no-power`, `boot-pin` (D3/D4/D8 of ESP8266 boards). `get_wiring` lists the errors and warnings of its view.
- **`diff_versions`**: compares two `sketch.fz` versions: parts added/removed/swapped/moved/property changes, nets joined or split, wires re-routed or recoloured, and per view the layout numbers plus new and fixed problems.
- **Zoomed `snapshot`**: the view SVG is cropped by its `viewBox` (Fritzing writes 1000 units per inch from the scene origin) before rasterising.

Laying out: place parts first, wires second. `move_part` snaps to 2.54 mm (breadboard, schematic), `align_parts` and `place_near` place by pins/boxes. `connect` takes `via` waypoints (a chain of wires; `remove` takes the whole chain unless `only_segment`) or `style: orthogonal`, which picks horizontal/vertical routes leaving each pin on the side it faces and refuses with a reason if every route crosses a part or foreign pin. `reroute_wire` redoes one wire, `tidy_view` (dry run by default) redoes a whole view and returns before/after numbers. Not done: `suggest_layout`, snapping parts into breadboard holes, net labels, 45° corners, layout markers in the web UI.

## Limits

- Parts plugged into a breadboard need a wire or matching pin positions; `connect` draws wires, it does not snap parts into holes.
- Parts Fritzing builds from properties (resistors, headers, DIP ICs) have pin positions from the library artwork, which can differ from what Fritzing generates for unusual property values.
- Only the orthogonal router for wires (no PCB autorouter), no DRC or gerber export yet (Fritzing has `-gerber`; the runner could grow that).

## Development

`task fritzing:docker:run` builds the image and runs it on http://localhost:8030 with data in `fritzing/data`. `task fritzing:web:dev` gives the hot-reloading UI against it.

Unit tests: `go test ./...` in `fritzing/`. The parts and sketch tests need a parts library: copy `/usr/share/fritzing/parts` and `parts.db.distrib` out of the image into a folder as `parts/` and `parts.db`, then `FRITZING_TEST_DATA=<folder> go test ./...` (they skip otherwise).
