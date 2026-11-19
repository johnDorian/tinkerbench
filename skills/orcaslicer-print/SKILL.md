---
name: orcaslicer-print
description: Use when slicing or printing a model with the tinkerbench OrcaSlicer front end (orcaslicer MCP) - importing an OpenSCAD workbench project or an STL, setting up a plate, slicing and sending the print to the printer through Moonraker. Covers asking which filament and profile to use, not touching settings unasked, and the confirm rules for the real printer. Trigger on - print, slice, slicer, orcaslicer, filament, plate, printer status, "let's print it".
---

# Slicing and printing

The orcaslicer MCP puts models on a plate, slices them with the OrcaSlicer CLI and prints through Moonraker. The person watches the bed view, plates and printer status live in the web UI. Keep it simple; if something more involved is needed, the person will say so.

## Rules

- **Ask which filament is loaded and which profile to use** if the person hasn't said. Don't guess. `list_profiles` shows the system presets; the plate defaults are the server's configured printer.
- **Don't change slicer settings** (presets, overrides, temperatures, brim, supports, anything) unless the person says to. Temperatures come from the filament profile.
- **Print only when the person asks.** Don't pause, resume or cancel unless they ask. `print_plate`, `pause_print`, `resume_print` and `cancel_print` drive the real printer and need `confirm: true`.

## Steps

1. `printer_status`: it must not be printing or paused.
2. Use the existing plate (`list_plates` / `get_plate`), or `create_plate` with the presets the person named.
3. Get the model onto it:
   - an OpenSCAD workbench project: `import_openscad(project)`. It takes that project's **last render**, so re-import after the model changes.
   - an STL: `upload_model` (base64).
   Then `add_object` (placed automatically unless x/y are given); `move_object` / `arrange` to rearrange. Rotation is degrees about X, Y, Z in that order; check the print orientation matches what the model was designed for.
4. `slice` (waits for the result). Report time, filament, bed temperature and any warnings.
5. When the person says to print: `print_plate(plate, confirm: true)`, then check `printer_status` once. A plate changed after slicing is stale and must be sliced again; the server refuses to print it.

## Overrides, when asked

Overrides are OrcaSlicer setting keys with string values on top of the presets (`update_plate` with `overrides`; a null value removes one). `get_profile` shows a preset's keys and values. Common: `layer_height`, `sparse_infill_density` (`"20%"`), `wall_loops`, `enable_support` (`"1"`/`"0"`), `brim_type`.
