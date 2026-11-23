# OpenSCAD workbench

An agent designs 3D-printable parts in OpenSCAD over MCP, and you watch the code and the model update live in the browser. It is a Go server with OpenSCAD in the same container and a Svelte UI embedded in the binary. It runs on port 8020 with no auth, so keep it LAN-only.

## Connecting an agent

The MCP endpoint is `http://localhost:8020/mcp` (Streamable HTTP, stateless). For Claude Code:

```sh
claude mcp add --transport http openscad http://localhost:8020/mcp
```

Tools:

| Tool | What it does |
| --- | --- |
| `list_projects`, `get_project`, `read_file` | Read projects, their sources and the last render result |
| `create_project` | New project with `main.scad` (given source or a template), rendered right away |
| `write_file`, `edit_file`, `delete_file` | Change sources. Each change is saved as a version, re-rendered, and returns errors, warnings, `echo()` output, bounding box and volume |
| `render` | Render again, optionally with customizer `params` (`{"width": 40}`) that override top-level variables. Params are stored with the project |
| `snapshot` | PNG images of the model from `iso`/`front`/`top`/… so the agent can see what it built. `source=true` previews `main.scad` instead, which shows `color()` and `#` debug modifiers |
| `list_versions`, `get_version`, `restore_version` | Undo |
| `set_description` | Describe the part |
| `list_comments`, `add_comment`, `reply_comment`, `resolve_comment`, `apply_suggestion` | Work through the comments you leave on the code or on the model (see below) |
| `list_parts` | The named pieces of the model (`part()`), their source lines, bounding boxes, and the `feature()` points |
| `list_images`, `read_image`, `upload_image`, `delete_image` | The scans and photos of the real part you upload, from `data` (base64) or `url`. `list_images` says which photos are cameras, whose dpi is not a scale. `read_image` comes back shrunk, so a 1200 dpi scan does not flood the agent |
| `measure_part` | Measure a flat part from one of those images: overall size, every step along it, a circle fit for a rounded end, a block of OpenSCAD variables, and an annotated image of what was measured (see below) |

The server's MCP instructions tell the agent the units (mm), Z-up, the bed size, that BOSL2 is installed, that a person is watching, and that anything in Z has to be asked for with a caliper rather than guessed.

## Parts and features

Name the pieces of a model in the source and the viewer, the agent and the comments can
talk about "the lid". `use <workbench.scad>` gives two modules:

```openscad
use <workbench.scad>
part("lid") translate([0, 0, 20]) cube([40, 40, 3]);
part("box") difference() {
    cube([40, 40, 20]);
    feature("M3 hole", [8, 8, 0]) translate([8, 8, -1]) cylinder(d = 3, h = 30, $fn = 32);
}
```

- `part("name") { ... }` marks one physical piece. Every piece should be in a part.
  Put a part as the first child of `difference()`, never as a subtracted child: a subtracted
  part is rendered without the body it cuts. Nested parts are not supported; the inner one is
  ignored with a warning.
- `feature("name", [x, y, z])` renders nothing. It names a point for holes and chamfers.
  Give the point in **absolute** model mm: OpenSCAD can't report the current transform.

After a successful render the server renders each part on its own in the background
(`out/parts/<n>.stl`, with a wrapper file that sets `$only_part` and includes `main.scad`, so
it works on any OpenSCAD version) and lists them in `GET /api/projects/<p>/parts`
(`list_parts`): name, source file and line, bounding box, triangle count, and the features.
The viewer has a parts list, and each part's STL is at `GET /api/projects/<p>/parts/<name>.stl`.
`parts_done` is sent on the event stream when the renders finish. A new render cancels the
part renders still running.

Each part mesh is the model rendered with only that part, so the meshes together are the
model. Geometry outside every part can't be selected on its own: it is listed as
`(unlabelled)` and is also included in every part's mesh, so the viewer should show the whole
model until the remainder is empty. Wrap everything in a part to avoid that.

Comments can be anchored on the model: a face (point plus normal), an edge or a point, of a
named part, in model mm. An anchor without a line of code is a model-only comment. An anchor
stays valid while a triangle of the part (or of the model) lies within 1 mm of its point (and
faces its normal, for a face); otherwise it is flagged `stale` and not moved.

The helper library is written to `DATA_DIR/lib` at start-up and put on `OPENSCADPATH`. Extra
openscad arguments for every run come from `OPENSCAD_ARGS`.

## Copying a real part

Drop a scan or a photo of the part into the **Images** tab, click **Measure**, and the
outline comes back in millimetres: overall length and width, every step along the part
(start, end, length, width, whether it is centred on the axis) and a circle fitted to a
rounded end, plus a block of variables to paste at the top of `main.scad`. The agent gets
the same thing from `measure_part`.

A flatbed scan is the easy path: lay the part flat face down, scan at 600–1200 dpi, and
the scan's own resolution is the scale, so there is no perspective and no parallax — the
two things that make a phone photo hard to trust.

A phone photo has no scale of its own. Its recorded resolution (72 or 300 dpi) is a default
the camera writes whatever it photographed, so a photo taken by a camera (its file names a
Make or Model) never uses that dpi, and `measure_part` refuses to scale by it. Give the
pitch of the graph paper the part sits on (`grid_mm`, solid lines or dots), one known length
(`scale_ref_mm`), or an explicit `dpi`. The paper is not flat in a photo, so the grid pitch
changes by a few per cent across the frame; the scale is taken where the part is. A part
standing above the paper reads large (its top is nearer the lens): pass `thickness_mm` and
`camera_mm` to correct it. Without them the result says so.

**Always look at the overlay.** It draws the outline that was actually measured in red,
the step boundaries in blue, the fitted circle in orange and a 10 mm bar, over the original
image. A shadow or a caliper jaw merged into the silhouette is the usual way this goes
wrong, and it is obvious in the picture and invisible in the numbers.

What it will not do:

- **Anything in Z.** Thickness, boss height, bore depth, wall thickness: caliper. The MCP
  instructions tell the agent to ask you for those (with `add_comment` on the line that
  needs one) instead of inventing them.
- **Features that are not in the silhouette.** A raised boss, a recess or a blind hole is
  not in the outline.
- **Oblique photos.** Graph paper plus flat, head-on lighting helps; a scanner removes the
  problem.

Images are read upright: the EXIF orientation is applied on upload and on every read, so
the sizes, the overlay and the crop coordinates match what a photo viewer shows.

If the outline comes out wrong, the dialog's second section has the knobs:

- `object`: how the part is found. `auto` (the default) is `split` for a scan, the original
  two-class light/dark threshold, and `dark` for a photo on graph paper or from a camera.
  `dark` removes the dots and lines of the paper first (grey closing, sized in millimetres
  once the scale is known), then keeps the darkest tone, so a soft shadow lighter than the
  part is left out. `light` is the mirror image.
- `threshold`: a grey level to use instead of the automatic one. For a part beside its
  shadow, the automatic level can land on the shadow; the level between the part and the
  shadow (about 50–60 for a black part on paper in a shadow) is the usual fix. Check the
  overlay.
- `crop`: `[left, top, right, bottom]` in pixels of the upright image. Measure only inside
  it; the grid is still read from the whole photo. Crop instead of cutting the file.
- `invert` for a light part on a dark background, a Lab or saturation channel when a shadow
  is as dark as the part but a different colour, which end is x = 0, and how finely the
  outline is split into steps.

If the outline reaches every edge of the image, the background was picked. An automatic
choice is tried the other way round once and the result says so.

A photo needs a scale, and the easiest one is a printable measuring mat: `GET /api/mats`
lists the four variants (A4 or US Letter, a 100 or 150 mm white square), and
`GET /api/mats/<name>.pdf` (for example `a4-100.pdf`) downloads one. Its white square is
bordered by a 1 mm grid, so `grid_mm = 1` gives the scale in the photo itself. Print it at
100% (turn off Fit to page), check the square with a caliper, lay the part flat inside it
and shoot from straight above. The MCP's `measure_part` description gives the same link.

### Uploading

`upload_image` takes exactly one of `data` (base64, for a small file) or `url` (an http or
https address the server can reach, fetched with a 30 s timeout and the 32 MB limit). For
a big photo, send the file as multipart instead, which needs no base64:

```sh
curl -F file=@photo.jpg http://localhost:8020/api/projects/<project>/images
```

The field is named `file`, as in the Images tab's own upload.

### Accuracy

Against synthetic parts of known size — a 1200 dpi scan rotated on the glass, and the same
part on a 1 mm grid at 20 px/mm:

| | true | scan | grid photo |
|---|---|---|---|
| total length | 26.95 | 26.95 | 26.94 |
| round tab | 7.98 × 8.01 | 7.98 × 7.96 | 7.94 × 7.94 |
| body | 11.08 × 14.24 | 11.07 × 14.28 | 11.04 × 14.27 |
| middle step | 7.83 × 3.10 | 7.81 × 3.10 | 7.79 × 3.12 |
| tongue | 4.61 × 1.60 | 4.59 × 1.61 | 4.57 × 1.61 |
| tab ⌀ (circle fit) | 7.98 | 7.96 (rms 0.005) | 7.91 (rms 0.012) |

So about ±0.05 mm on a clean scan and ±0.1 mm off a grid, the same order as a hand-held
caliper, and it gets every feature in one pass instead of one photo per dimension. These
are *synthetic* images, where the silhouette is perfect: an upper bound on what a real scan
will do, not a promise.

Measuring is pure CPU and runs while the request is open: about 0.35 s per megapixel here,
perhaps three times that on the Pi. Images over 40 MP are refused with a note to crop,
which costs no accuracy because the scale comes from the dpi.

## Web UI

- Left: the source files. The agent's changes appear as they happen, and error lines are marked in red in the gutter. "Edit" lets you change code yourself; Ctrl+S saves and renders.
- Middle: the three.js view of the rendered STL on the print bed. It shows the size, volume and triangle count, and warns when the part is larger than the bed, below it, or floating above it. The render log, a params editor and Download STL are here too.
- Right: an activity feed of what the agent is doing, the comments, the images of the real part, and the version history. Click a version to view it, then restore it.
- "Follow agent" switches to whichever project the agent works on.

### Comments and suggestions

Click a line number in the code view to comment on it (shift-click to select a range). Tick "Suggest replacement code" to propose new code for those lines; it shows as a diff, and "Apply suggestion" writes it in as a new version, without the agent. Threads appear under their lines, and the Comments tab lists them all.

The agent can't be notified, so after commenting, ask it to "address my comments on <project>". It reads them with `list_comments`, makes the changes or applies suggestions, replies, and resolves. You can watch this happen live. Comments are stored with the quoted code and move with it when lines are inserted or removed. If the code itself changes, the comment is marked "outdated" and its suggestion can no longer be applied.

Live updates use server-sent events (`/api/events`).

## Storage

`<DATA_DIR>/projects/<name>/`: `main.scad` and any other `.scad` files, `project.json` (description, params), `comments.json`, `images/` (uploaded scans and photos), `.history/` (every saved version plus `log.jsonl`), and `out/` (`model.stl`, `render.json`). A deleted project is moved to `<DATA_DIR>/trash/`.

Images are not versioned — a `.scad` file is small and worth a copy per save, a 20 MB scan
is not — and are capped at 32 MB each. `GET /api/projects/<p>/images/<f>?w=240` serves a
shrunk copy, which is what the UI's thumbnails and the agent's `read_image` use.

A failed render keeps the previous `model.stl`, so the viewer keeps showing the last good model and marks the render as failed.

## Measuring, without OpenCV

The measuring code is a port of an earlier Python tool (`measure_part.py`), which used OpenCV and
numpy. It is written against the Go standard library instead, so the server stays a single
static binary (`CGO_ENABLED=0`, no module dependencies) on a `bookworm-slim` image: gocv
would have meant CGO plus the OpenCV runtime, roughly +300 MB on a small home server that also runs
other services. The numpy half — an FFT for the grid pitch,
an SVD for the part axis, a least-squares circle fit — has no gocv equivalent and would
have been hand-written either way.

`internal/imaging` therefore has its own channel extraction, 5×5 blur, Otsu threshold,
morphological open/close, contour tracing, Canny and Hough transform, each matched to the
OpenCV call it replaces. `internal/measure` is the measurement itself. Both were checked
against the Python tool on the same images: the two agree to the last reported digit,
including through the Canny → Hough → rotate → spectrum chain that recovers the scale from
graph paper with solid lines. Paper with dots instead has no lines for the Hough transform, so
the grid is found from the 2D spectrum of blocks of the photo, with the part masked out
(`internal/measure/dotted.go`). The tests regenerate the synthetic parts from scratch, so they stay honest
without fixture files.

## OpenSCAD

The image runs an OpenSCAD development snapshot (x86_64 AppImage, extracted to `/opt/openscad`, pinned with `--build-arg OPENSCAD_SNAPSHOT=<YYYY.MM.DD>`) with the Manifold backend (`OPENSCAD_ARGS=--backend=manifold`). On the Mac mini that renders `laser_tag_puck` in 3.5 s instead of 130 s with Debian's 2021.01 (CGAL), and small models in under 0.1 s; it runs fine on the Mac mini's CPU, which has no AVX2. The image is amd64 only now. `OPENSCAD` picks another binary (e.g. a local `openscad` for `task openscad:run`, with `OPENSCAD_ARGS` empty for 2021.01). The defaults are `RENDER_TIMEOUT` 5m and `RENDER_CONCURRENCY` 2. [BOSL2](https://github.com/BelfrySCAD/BOSL2) is cloned into the image (`--build-arg BOSL2_REF=<tag>` pins it). Snapshots run under `xvfb-run`.

## Development

- `task openscad:run` (needs `openscad` on PATH and Node). It installs the npm packages if needed, builds the UI and serves everything on http://localhost:8020 from `./data`.
- `task openscad:web:dev` runs Vite with hot reload on http://localhost:5173, proxying the API to the Go server.
- `task openscad:test`. The render tests run the real `openscad` and are skipped if it isn't installed. With the snap build of OpenSCAD, set `TMPDIR` to a **non-hidden** folder under your home (`TMPDIR=$HOME/scadtest task openscad:test`), because snaps get a private `/tmp` and cannot see `~/.cache` either.
- Deploy with the rest of the stack: `task docker:build && task docker:up`. The Docker build compiles the UI itself, so `web/dist` isn't committed.
