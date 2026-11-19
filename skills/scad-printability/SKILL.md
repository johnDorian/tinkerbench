---
name: scad-printability
description: Use whenever designing or changing a part in the openscad workbench (openscad MCP) that will be 3D printed on an FDM printer - before adding any feature that starts above the bed (lips, ledges, rims, hooks, bosses, shelves, collars, snap bumps, roofs, holes through a side wall) and before calling a model done. Catches the features that print in mid-air and turn into spaghetti - a ring or ledge that sits next to a wall instead of on it, an overhang flatter than 45 degrees, an unsupported bridge, parts that only touch - and makes the model prove each one is supported with assert()/echo() checks. Trigger on: new or changed openscad model, lip, snap fit, ledge, overhang, bridge, support, "printable", "without supports", spaghetti, failed print, print orientation.
---

# Printable without supports

Every layer must sit on the layer below it. The slicer will happily print a
feature that starts in mid-air — the render looks perfect, the print turns into
spaghetti at that height. A look at the iso snapshot does not catch this: a ring
0.15 mm off a wall looks attached.

**Real failure (laser_tag_puck, 2026-11):** the snap lip on top of the base was
a tube with outer radius `(in_d - gap)/2` = 46.85 standing at `split_h - 1`,
while the wall's inner face was at `in_d/2` = 47.0. It never touched the wall
and had nothing under it down to the floor. Two prints failed at z ≈ 14. Fix:
a 45° ledge (rotate_extrude'd triangle) from the wall out under the lip, and an
assert that the overlap is positive.

## The rule for every feature you add

For each solid that does not start at z = 0, answer in numbers, not by eye:

1. **What is directly under its lowest face?** Name the solid and the
   overlap in mm (radial for rings/tubes, XY for blocks). If the answer is
   "nothing" or "a gap of 0.x mm", it floats.
2. **Overlap, don't touch.** Unioned parts must overlap by ≥ 0.4 mm (one
   extrusion width) to fuse in the slicer; faces that merely coincide or sit a
   clearance (`gap`, `fit`) apart print as separate, unsupported shells.
   Clearance values belong *between parts that move*, never between a feature
   and the thing holding it up.
3. **If it overhangs, how?**
   - ≤ 45° from vertical: fine.
   - Flatter: turn the underside into a 45° chamfer/cone (a rotate_extrude'd
     triangle for rings, a `chamfer` or wedge for blocks), or a teardrop for
     horizontal holes (> ~6 mm diameter).
   - Horizontal underside: only as a **bridge** — supported at both ends, span
     ≲ 10 mm. A cantilever (one end free) wider than ~1 mm is not a bridge.
4. **Small horizontal ledges** (hooks, snap bumps ≤ ~1.2 mm) are OK flat;
   anything wider gets the chamfer.

## Make the model prove it

Put the check next to the existing fit checks, so a later change of a
measured number re-runs it:

```openscad
// lip must sit on (overlap) the wall or on a ledge, not next to it
lip_or = (in_d - gap)/2;
assert(ledge_r - lip_ir >= 0.4 || lip_or >= in_d/2 + 0.4,
       "snap lip is not supported from below - add a ledge");
echo(lip_support_overlap = ledge_r - lip_ir);
```

Rules for these checks:
- One `assert` per feature that starts above the bed, with the support it
  relies on in the message.
- `echo` the overlap / overhang angle / bridge span so the number is visible
  in every render result.
- When a clearance variable (`gap`, `*_fit`) appears in a feature's
  radius/offset, check which side it moves the feature to — that is exactly
  how the puck lip ended up 0.15 mm off its wall.

## Before calling a model done

- State the print orientation of every part (which face is on the bed); a lid
  printed flipped has its "overhangs" the other way up — run the checklist in
  print orientation, not assembled orientation.
- Walk the model bottom-up by height: list every z where a new feature starts
  (from the code, e.g. `split_h - 1`, `wall + pack_end_h`) and for each say
  what it stands on.
- Walls ≥ 0.8 mm (2 perimeters), slots/gaps ≥ 0.4 mm, or the slicer drops them.
- Snapshot `front` and `bottom` with `source=true` and `#` on the suspicious
  feature if you want a visual, but the asserts are the proof.

If the person reports spaghetti at a certain height, go straight to step 1
for every feature whose lowest face is at or just below that height.
