---
name: fritzing-workbench
description: Use when designing, wiring, checking or tidying a circuit in the tinkerbench Fritzing workbench (fritzing MCP) - breadboard, schematic or PCB sketches, often for a microcontroller firmware project. Covers keeping pins in sync with the firmware, placing before wiring, the check loop (check_circuit, check_layout, diff_versions, zoomed snapshots instead of a screenshot after every edit), how to read the findings, broken part art (art-mismatch) and notes. Trigger on - fritzing, breadboard, schematic, PCB, wiring diagram, circuit, pinout, "how do I connect", transistor/relay/sensor/LED wiring.
---

# Fritzing workbench

The fritzing MCP edits Fritzing 0.9.6 sketches on the workbench server and renders them. The person watches the web UI while you work. Every write is a version (`list_versions` / `restore_version` undo), so pass `by: "claude"`.

## Conventions

- `list_projects` first and extend an existing sketch rather than starting a new one. If the circuit belongs to a firmware project, give the sketch the same name.
- **The firmware is the source of truth for pins.** Read the pin constants in the firmware before wiring and wire the pins the code uses. If a signal has to move to another pin, change the firmware constant too and say so. Don't wire from memory of the board.
- Check the board's boot-strapping pins before using them for outputs that can float or be pulled low at power-up (ESP8266: GPIO0, GPIO2, GPIO15; `check_circuit` flags these). Mind the logic level (ESP boards are 3.3 V) and what the board's 5 V pin actually is (often USB 5 V through a diode, not a regulated output).
- A wiring job never needs flashing or the serial port.

## Workflow

1. `search_parts` -> `get_part` (connector ids, names, properties, which views exist) -> `add_part`. Use connector ids (`connector0`) when names repeat. A missing part: `import_part` a `.fzpz`.
2. **Place parts first, wires second.** `move_part` / `place_near` / `align_parts` (grid 2.54 mm in breadboard and schematic), then `connect`. Align the pins a wire joins so the wire has no jog.
3. Draw in **every view you care about**: a wire exists only in the view it was drawn in. `get_wiring` warns when a connection is in one view and not the other.
4. `connect style=orthogonal` for horizontal/vertical wires; pass `via` waypoints when the router fails or you want a specific path.
5. `render=false` on intermediate edits, render on the last one: every render costs a few seconds.
6. Check, in this order, and only then look at a picture:
   - `check_circuit`: electrical sense by pin names (supply shorted to ground, 5 V joined to 3.3 V, part with no ground, LED straight across the supply, boot pins in use).
   - `describe_sketch` (nets) and `check_layout` per view (geometry).
   - The render result's `warnings`: `art-mismatch` means Fritzing draws that part's pins somewhere other than where they are (see below).
   - `snapshot` **once at the end**, not after every edit. To look at one spot, zoom: `snapshot around="Q1" margin_mm=12` (a part title, or a wire title for its whole run) or `region={x,y,w,h}` in mm.
   - After an edit, `diff_versions` (defaults to the last two versions; `from`/`to` from `list_versions` to compare further apart) says in words what changed. Use it instead of a before/after picture.
   - If `check_circuit` / `diff_versions` are missing from the tool list, the MCP client has not refreshed since the server was updated: start a new session.
7. When it works: `set_notes` (purpose, power, build steps, what to verify on the real parts, firmware pins), then `get_wiring` to read it back. Notes go stale when the sketch changes (`get_wiring` says so); refresh them.

## Reading the findings

- `check_layout` **error** (pin-contact: pins of different nets touch): fix always.
- **warn** (wire-over-part, parts-overlap; on a PCB also crossings): fix or justify.
- **info crossing** in breadboard/schematic is **not a problem**: Fritzing wires only connect at their ends, never where they pass over each other. Avoid *ambiguous* crossings (same colour) and cross at a right angle, not along each other.
- **warn parallel-close**: wires of different nets run side by side under 1 mm for more than 3 mm and read as one wire. Move one.
- **warn crossing, same colour** (breadboard): looks joined; recolour one.
- `check_circuit` levels: `error` (power-short, rail-short) must be fixed; `warn` (no-ground, led-no-resistor) probably wrong; `info` (no-power, boot-pin) worth knowing. It goes by pin names, so a part with odd names (`V+`, `Vin`) may need a look.
- The tidiness score (1 per crossing, 0.5 per bend, 3 per diagonal, 10 per warning, 20 per error) is for comparing before/after, not a goal.
- Wire colours: signal orange, ground black, power red.

## Layout lessons

- `reroute_wire` / `tidy_view` keep old bends: after moving parts a lot, `remove` the wire chain and `connect` again rather than rerouting on top of stale waypoints.
- Waypoints must use the real pin coordinates from `check_layout` (pins list), or the last segment comes out a hair diagonal (`orthogonal_pct` below 100).
- A three-leg part (transistor) forces one crossing for its middle pin: drop the middle pin's wire below the legs first and cross the neighbour once at 90 degrees.
- `part_image` takes a `module_id`, not a part title, to see a part on its own.

## Parts with broken art

Fritzing 0.9.6's SVG exporter drops `rotate(...)` groups in some part SVGs, so their pin rings and labels are drawn away from the real pins (a row of floating holes beside the board). The render warning `art-mismatch: B1 (...): 14 of 16 connectors are drawn away from where the part puts them` reports exactly this, for breadboard and schematic views. Fritzing's stock WeMos D1 mini is one such part.

Fix: take the part's `.fzp` and SVGs, rewrite every `transform="translate(...) rotate(a cx cy) ..."` in the affected SVG into a single `matrix(...)`, give it a new `moduleId` and image name, zip it as `.fzpz` (fzp plus `svg.<view>.<name>.svg`) and import it (`import_part`, or `POST <workbench>/api/userparts?overwrite=1` for a file too big to send base64). Then swap the instance in `sketch.fz` with `edit_file` (change `moduleIdRef` and `path`; connector ids must match). Check `search_parts source=user` first: a fixed copy may already be imported.
