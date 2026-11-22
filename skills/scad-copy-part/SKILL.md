---
name: scad-copy-part
description: Use when replicating a real, physical object as an OpenSCAD model in the openscad workbench (openscad MCP) from a scan, photos, a sketch or a description. Covers the whole loop - getting a usable image, measure_part for the outline in millimetres, checking the overlay, turning that into code, and pinning add_comment questions on the lines whose dimensions were never measured (anything in Z) instead of guessing them. Trigger on: copying or replicating a part, "model this", "from these photos", "I scanned it", measure, caliper, scan, replacement part, or any model where a dimension is estimated, assumed, inferred or approximate.
---

# Copying a real part

The workbench can measure a part's **outline** for you. It cannot measure
anything in **Z**. A copy is only as good as its worst dimension, and the
failure mode is not a wrong number — it is a **guessed number that looks
measured** once it sits in the file next to twelve real ones.

So the job splits cleanly:

| | where it comes from |
|---|---|
| Length, width, every step along the outline, a round end's radius | `measure_part` |
| Thickness, boss height, bore depth, wall, counterbore, chamfer | the person's caliper — you ask |
| Whether a hole is blind or through, a floor flat or domed | the person — you ask |

## 1. Get an image worth measuring

The person has to produce this; you cannot fix it afterwards. Ask for, in order
of preference:

1. **A flatbed scan**, part flat face down, 600–1200 dpi. The scan records its
   own resolution, so it carries its scale: no perspective, no parallax,
   nothing to calibrate. This is the whole reason it is the first choice.
2. **A photo on 1 mm graph paper**, flat and head-on, diffuse light. Graph paper
   with solid lines or with dots both work. Pass `grid_mm: 1`, and `object: "dark"`
   for a dark part (it leaves out a shadow beside the part and the paper's dots).
3. **Any photo plus one caliper reading** of the overall length, passed as
   `scale_ref_mm`.

A photo's recorded dpi is never a scale: a camera writes 72 or 300 whatever it
photographed. `list_images` says which photos are cameras (`camera`, `dpi_usable`).
`measure_part` refuses to scale by a camera's dpi; pass `grid_mm`, `scale_ref_mm` or
an explicit `dpi` instead.

Tell them to crop to the part, and crop with `crop: [left, top, right, bottom]` in
the upright photo's pixels rather than cutting the file. Over 40 MP is refused,
and cropping costs no accuracy because the scale comes from the grid or the dpi.

They upload in the UI's **Images** tab; `list_images` shows what a project has.
If they send a link, `upload_image` with `url` fetches it. If they paste an image
into chat, `upload_image` it with `data`, so it lives with the project. For a big
photo the person can send it as multipart with `curl -F file=@photo.jpg
<server>/api/projects/<project>/images` (the field is `file`), which needs no
base64. `read_image` to look at one — it comes back shrunk, so looking is cheap.

## 2. Measure, then check the overlay

`measure_part` returns the outline in millimetres plus an annotated image. **Look
at the overlay every time.** A shadow, a caliper jaw or a finger merged into the
silhouette is invisible in the numbers and obvious in the picture: the red
outline will not be the part's.

If the outline is wrong, it is nearly always one of:

- the background got picked instead of the part → `invert: true` (the result
  says so when the shape reaches every edge of the image)
- a photo's shadow or the paper's dots are in the outline → `object: "dark"` (the
  default for a photo on a grid; it takes out the dots and keeps the darkest
  tone), and `crop` to the part
- the shadow beside the part is still in the outline → `threshold`: the grey level
  between the part and its shadow (about 50–60 for a black part in a shadow).
  Check the overlay after each change
- a shadow is as dark as the part → `channel: "a"`, `"b"` or `"sat"`, which
  separate by colour where brightness cannot
- the steps are split too finely or too coarsely → `tol_mm`, `min_step_mm`
- the wrong end is x = 0 → `flip: true`

A photo is not flat, so its grid pitch differs across the frame. `grid_spread_pct` says by how
much; above about 2% the result carries a perspective note. The scale is taken where the part
is. For better accuracy anchor with `scale_ref_mm`, or ask for a shot from further away and
straight down. Quote the numbers as measured.

A part standing above the paper (a photo from above) reads large, because its top
is nearer the lens than the paper. When the scale came from graph paper or a camera
and `camera_mm` was not given, the result says so. Ask the person for the height of
the feature above the paper (a caliper reading) and the lens-to-paper distance, then
pass `thickness_mm` and `camera_mm`. Until then, the numbers are the paper's scale.

Do not quietly accept a measurement you would not defend. Say what the overlay
shows, and ask for a better image if that is the real answer.

## 3. Turn it into code

- Paste the `openscad` block from the result at the top of `main.scad` as
  customizer variables, then build the geometry from those names. Never inline
  the numbers — the person is going to want to tune them.
- Keep the measured numbers as measured. If you round 11.07 to 11, say so.
- `x` in the result runs the length of the part from the leading end and `y`
  across the width, centred on the axis, which is the frame to model in.
- A round end is a **stadium**, not a disc: the step's width is the flank, the
  circle fit gives the tip radius, and `centre_x_mm` is where the straight part
  ends.
- `centred: false` on a step means it is off the axis — model the offset, do not
  centre it out of habit.

## 4. Ask for everything the outline cannot show

Every dimension that was not measured gets an `add_comment` on its own line,
before you hand the model over. No exceptions for "close enough" or "standard".

Do not instead:

- bury it in a chat message (it scrolls away; the file outlives the session)
- leave only a `// estimated` comment in the code (nothing prompts a reply)
- pick a "standard" value because the part resembles a known standard — a part
  that is 11.08 wide is not a LEGO part, and 4.8 is not its stud

How to ask well:

1. **Model the whole part anyway**, with your best estimate, so there is
   something to look at and rotate. Never block on the question.
2. **Name the estimate as a top-level variable**, so there is a line to pin to,
   and mark it: `stud_height = 2.60; // NOT measured`.
3. **One comment per dimension.** Several questions in one comment get one answer.
4. **Say how to take the reading**, not just what you want: "stand the part on
   its flat face and measure from the plate's top surface to the top of the
   tube" beats "how tall is the stud?".
5. **Give your estimate and where it came from**, so a plausible answer can be
   sanity-checked: "2.6 from the ellipse foreshortening in photo 2; anything in
   2.4–2.9 would look the same."
6. **Say what it changes.** "This sets how deep a pin seats" tells them how much
   precision is worth their time.

Also comment on **assumptions about hidden geometry** (blind or through, flat
floor or domed, a fillet you cannot see), **anything one photo cannot
distinguish** (disc or stadium, square step or chamfered), and **dimensions you
derived** rather than read, naming the readings they came from.

## 5. Close the loop

When the person replies: apply it, `reply_comment` saying what you changed,
`resolve_comment`, re-render. Leave no stale comment. If they left a suggestion,
`apply_suggestion` uses their code verbatim.

Then `snapshot` and actually look at it against their image.

Before handing over, list the top-level variables and classify each: measured,
derived from measurements, or guessed. Every guess has an open comment, or you
are not done. Say in one line what is open — "3 dimensions are estimates,
comments are on those lines in main.scad".

## Fit, not just size

For a bore that has to fit something, the measured hole and the printed result
differ, and a worn part measures small. Ask for the **mating** dimension too —
the pin's diameter, not only the hole's — so clearance is a deliberate choice
rather than a copy of the wear.
