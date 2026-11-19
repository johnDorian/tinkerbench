---
name: openscad-workbench
description: Use when creating, changing or reviewing a 3D model in the tinkerbench OpenSCAD workbench (openscad MCP). Covers the project/render/snapshot loop, naming pieces with part() and feature(), customizer-style variables, versions, and working through the person's line and face comments. Hands off to scad-printability (anything printed) and scad-copy-part (copying a real object). Trigger on - openscad, .scad, model, design a part, enclosure, bracket, render, snapshot, "the comments on the model".
---

# OpenSCAD workbench

The openscad MCP edits `.scad` projects on the workbench server and renders them. The person watches the web UI: code and the 3D model update live after every write, so they see each change as you make it.

## Loop

1. `list_projects` first; extend an existing project rather than creating a near-duplicate. `create_project(name, description)` otherwise (lowercase letters, digits, `-`, `_`).
2. `write_file` / `edit_file` on `main.scad`. Each write renders and returns errors, warnings, `echo()` output and the bounding box: read them before the next edit.
3. `snapshot` to look, **once at the end of a step**, not after every edit. Use the bounding box and `echo()` numbers to check sizes; a picture is for shape.
4. Pass `by: "claude"` on every write. Every save is a version: `list_versions` / `get_version` / `restore_version` undo mistakes.

## Writing models the workbench understands

- Start `main.scad` with `use <workbench.scad>`. Wrap each physical piece in `part("lid") { ... }` (short lowercase names, same name for the same piece) and mark holes and small details with `feature("M3 hole", [x, y, z])` (absolute model mm). `list_parts` then gives each part's bounding box and source line, and the person can comment on its faces, edges and points. Geometry outside every part renders but can't be selected: wrap it too.
- Tunable dimensions are top-level variables at the top of `main.scad` (customizer style), never numbers inlined in geometry. `render` with `params` tries values without editing.
- Units mm, Z up, the bed is the XY plane: keep every part at min z = 0 and inside the bed. Lay out multi-part models side by side in print orientation.
- BOSL2 is installed (`include <BOSL2/std.scad>`). Use `$fn` 32-96; avoid `minkowski` and `hull` over many objects (slow renders).
- Fit checks belong in the model as `assert()` with an `echo()` of the number, so a later change re-runs them.

## Comments from the person

`get_project` shows open comments. To work through them: `list_comments`, make the change (`apply_suggestion` applies their replacement code verbatim, or edit yourself), `reply_comment` saying what you did, `resolve_comment`. Comments with an `anchor` (part, face/edge/point, model mm) and no code line are model-only: answer and resolve them; they can't be applied.

Ask with `reply_comment` instead of guessing; `add_comment` pins your own question or note on a line.

## Related skills

- **scad-printability** before adding anything that starts above the bed, and before calling a model done.
- **scad-copy-part** when the model copies a real object (scans, photos, caliper readings).
- **orcaslicer-print** to slice and print the result.
