// Package mcp is a minimal MCP server (Streamable HTTP transport, stateless, JSON responses only).
// One endpoint: POST /mcp with a JSON-RPC 2.0 message or batch.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/app"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/measure"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/render"
)

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructionsTmpl = `OpenSCAD workbench for designing 3D-printable parts. A person watches the web UI (%s): it shows the code and the rendered model live, so they see every change you make.
Each project is a folder of .scad files; main.scad is rendered. Other files can be used from it with include <x.scad> / use <x.scad>. Libraries installed: BOSL2 (include <BOSL2/std.scad>).
Units are millimetres, Z is up, the print bed is the XY plane at z=0; keep parts on the bed (min z = 0) and inside a %s mm bed.
Workflow: create_project or get_project -> write_file / edit_file (renders automatically and returns errors, warnings, echo() output and the bounding box) -> snapshot to look at the result -> iterate.
Put tunable dimensions as top-level variables (customizer style) at the top of main.scad; render with params overrides them without editing code.
Every save is a version: list_versions / restore_version undo mistakes. Pass by (your name) on writes so history shows who changed what.
The person can comment on lines of code in the UI, optionally with a suggestion (replacement code for those lines). get_project shows open comments; when asked to address them: list_comments, make the change (apply_suggestion applies a suggestion verbatim, or edit yourself), reply_comment saying what you did, then resolve_comment. Ask questions with reply_comment instead of guessing; add_comment leaves your own note on a line for the person.
Name the physical pieces of a model: wrap each distinct piece in part("lid") (short lowercase names; use the same name for the same piece) and start main.scad with use <workbench.scad>. Mark holes and small details with feature("M3 hole", [x, y, z]), a point in absolute model mm. list_parts gives the parts, their bounding boxes and the source line of each. Anything outside every part still renders, but it can't be selected in the viewer; wrap it too.
When the person comments on a face, edge or point of the model, the comment carries an anchor: part, kind (face, edge or point), point in model mm. list_comments shows it. Comments with an anchor and no line of code are model-only: answer them with reply_comment and resolve them; they can't be applied as suggestions.
Renders on the server use OpenSCAD %s (CGAL, can be slow): use $fn of 32-96 for curves and avoid needless minkowski/hull over many objects.
To copy a real part: the person uploads a scan or photo of it (list_images shows what a project has, read_image looks at one), then measure_part returns its outline in millimetres - overall size, every step along it, and a circle fit for a rounded end - plus an annotated image of what was measured. Upload with upload_image (data or url) or, for a big file, curl -F file=@photo.jpg to the images endpoint. A flatbed scan at 600-1200 dpi carries its own scale; a phone photo needs grid_mm (graph paper, lines or dots), scale_ref_mm (one known length) or an explicit dpi, since a camera's recorded dpi is a default and is never a scale (list_images says which files it applies to). Crop to the part with crop instead of cutting the file. Measuring only sees the silhouette, so nothing in Z: ask the person for a caliper reading of the thickness, a boss height, a bore depth or a wall rather than guessing one - add_comment on the line that needs it is the way to ask.`

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

// Server serves MCP over HTTP on top of the app.
type Server struct {
	app          *app.App
	instructions string
}

// New creates the MCP server. uiURL and bed are only used in the instructions for the agent.
func New(a *app.App, uiURL, bed, openscadVersion string) *Server {
	return &Server{app: a, instructions: fmt.Sprintf(instructionsTmpl, uiURL, bed, openscadVersion)}
}

func (m *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// No server-initiated stream and no sessions.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	body = []byte(strings.TrimSpace(string(body)))

	var reqs []rpcReq
	batch := len(body) > 0 && body[0] == '['
	if batch {
		err = json.Unmarshal(body, &reqs)
	} else {
		var one rpcReq
		err = json.Unmarshal(body, &one)
		reqs = []rpcReq{one}
	}
	if err != nil {
		writeJSON(w, 400, rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
		return
	}

	var out []rpcResp
	for _, req := range reqs {
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue // notification (e.g. notifications/initialized) or a response: nothing to send back
		}
		res, e := m.handle(r.Context(), req)
		resp := rpcResp{JSONRPC: "2.0", ID: req.ID}
		if e != nil {
			resp.Error = e
		} else {
			resp.Result = res
		}
		out = append(out, resp)
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if batch {
		writeJSON(w, 200, out)
	} else {
		writeJSON(w, 200, out[0])
	}
}

func (m *Server) handle(ctx context.Context, req rpcReq) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		ver := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		return map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "openscad", "version": "1.0.0"},
			"instructions":    m.instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		content, err := m.call(ctx, p.Name, p.Arguments)
		if err != nil {
			var unknown unknownTool
			if errors.As(err, &unknown) {
				return nil, &rpcErr{-32602, err.Error()}
			}
			return map[string]any{"content": []any{text(err.Error())}, "isError": true}, nil
		}
		return map[string]any{"content": content}, nil
	}
	return nil, &rpcErr{-32601, "method not found: " + req.Method}
}

// ---------- tools ----------

type unknownTool string

func (u unknownTool) Error() string { return "unknown tool: " + string(u) }

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// anchorSchema is the JSON schema of a comment anchor (model mm).
func anchorSchema() map[string]any {
	vec := map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "minItems": 3, "maxItems": 3}
	return map[string]any{"type": "object", "description": "A place on the model: a face, edge or point of a part",
		"properties": map[string]any{
			"part":   map[string]any{"type": "string", "description": "Part name from list_parts; empty for the whole model"},
			"kind":   map[string]any{"type": "string", "enum": []string{"face", "edge", "point"}, "description": "default point"},
			"point":  map[string]any{"description": "[x, y, z] in model mm", "allOf": []any{vec}},
			"normal": map[string]any{"description": "face only: outward normal [x, y, z]", "allOf": []any{vec}},
			"edge":   map[string]any{"description": "edge only: [[x, y, z], [x, y, z]] in model mm", "type": "array", "minItems": 2, "maxItems": 2, "items": vec},
		}, "required": []string{"point"}, "additionalProperties": false}
}

func viewNames() []string { return []string{"iso", "front", "back", "left", "right", "top", "bottom"} }

func mcpTools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	proj := str("Project name (lowercase letters, digits, - and _)")
	file := str("Source file, default main.scad")
	source := str("Who makes the change (your agent name); shown in the history")
	note := str("Short description of the change; shown in the history")
	doRender := map[string]any{"type": "boolean", "description": "Render main.scad after saving (default true)"}
	image := str("Image file name; default the most recently uploaded one")
	num := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	params := map[string]any{"type": "object", "additionalProperties": true,
		"description": "Customizer overrides for top-level variables, e.g. {\"width\": 40, \"label\": \"A\", \"holes\": [3, 4]}. Stored with the project and used by every later render until changed; {} clears them."}
	tool := func(name, desc string, schema map[string]any, readOnly, destructive bool) map[string]any {
		return map[string]any{"name": name, "description": desc, "inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive}}
	}
	return []map[string]any{
		tool("list_projects", "All projects (newest change first) with description, files and stored params.",
			obj(map[string]any{}), true, false),
		tool("create_project", "Create a project with main.scad (source, or a small template) and render it. The web UI switches to it.",
			obj(map[string]any{"name": proj, "description": str("What the part is for"), "source": str("Initial main.scad"), "by": source}, "name"), false, false),
		tool("get_project", "A project's description, params, every source file with content, and the last render result (errors, warnings, echo, bounding box, volume).",
			obj(map[string]any{"project": proj}, "project"), true, false),
		tool("read_file", "Content of one source file, with line numbers if numbered=true (OpenSCAD errors cite line numbers).",
			obj(map[string]any{"project": proj, "file": file, "numbered": map[string]any{"type": "boolean"}}, "project"), true, false),
		tool("write_file", "Replace (or create) a source file with content, then render. Returns the render result.",
			obj(map[string]any{"project": proj, "file": file, "content": str("Full file content"), "note": note, "render": doRender, "by": source}, "project", "content"), false, false),
		tool("edit_file", "Replace old_string with new_string in a source file (exact match, must be unique unless replace_all), then render. Cheaper than write_file for small changes.",
			obj(map[string]any{"project": proj, "file": file, "old_string": str("Exact text to replace"), "new_string": str("Replacement"),
				"replace_all": map[string]any{"type": "boolean"}, "note": note, "render": doRender, "by": source}, "project", "old_string", "new_string"), false, false),
		tool("delete_file", "Delete a source file other than main.scad (its history is kept), then render.",
			obj(map[string]any{"project": proj, "file": file, "by": source}, "project", "file"), false, true),
		tool("render", "Render main.scad to STL. Returns ok, errors, warnings, echo() output, duration and stats (triangles, bounding box min/max/size in mm, volume in mm³). On failure the previous model stays in the viewer.",
			obj(map[string]any{"project": proj, "params": params, "by": source}, "project"), false, false),
		tool("snapshot", "Look at the model: PNG images of the rendered STL from the given views (default iso). source=true previews main.scad instead, which shows color() and #/% debug modifiers.",
			obj(map[string]any{"project": proj,
				"views":  map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": viewNames()}},
				"width":  map[string]any{"type": "integer", "description": "px, default 800, max 1600"},
				"height": map[string]any{"type": "integer", "description": "px, default 600, max 1600"},
				"source": map[string]any{"type": "boolean"}}, "project"), true, false),
		tool("set_description", "Change a project's description.",
			obj(map[string]any{"project": proj, "description": str("What the part is for"), "by": source}, "project", "description"), false, false),
		tool("list_versions", "Saved versions of a project (or of one file), oldest first: n, file, time, source, note.",
			obj(map[string]any{"project": proj, "file": str("Only this file")}, "project"), true, false),
		tool("get_version", "Content of a file as saved in version n.",
			obj(map[string]any{"project": proj, "version": map[string]any{"type": "integer"}}, "project", "version"), true, false),
		tool("list_comments", "Comments the person left on code lines, oldest first per file: id, file, line/end_line (current position), quote (the commented code), body, suggestion (replacement code for those lines, if any), replies, outdated (the commented code changed, so line is approximate). Open ones only unless include_resolved.",
			obj(map[string]any{"project": proj, "file": str("Only this file"), "include_resolved": map[string]any{"type": "boolean"}}, "project"), true, false),
		tool("add_comment", "Leave a comment for the person, e.g. a question or an explanation of a design choice: on lines of a file (file, line), on a face/edge/point of the model (anchor), or both (the anchored part's line is used when file and line are left out). An anchor without a line is model-only.",
			obj(map[string]any{"project": proj, "file": str("Source file; default main.scad when a line is given"), "line": map[string]any{"type": "integer"}, "end_line": map[string]any{"type": "integer", "description": "Last line, default = line"},
				"body": str("Comment text"), "anchor": anchorSchema(), "by": source}, "project", "body"), false, false),
		tool("reply_comment", "Reply in a comment's thread, e.g. what you changed or a question back.",
			obj(map[string]any{"project": proj, "id": map[string]any{"type": "integer"}, "body": str("Reply text"), "by": source}, "project", "id", "body"), false, false),
		tool("resolve_comment", "Mark a comment as done (resolved=false reopens it). Optional reply is added first.",
			obj(map[string]any{"project": proj, "id": map[string]any{"type": "integer"}, "resolved": map[string]any{"type": "boolean", "description": "default true"},
				"reply": str("Optional reply, e.g. what you changed"), "by": source}, "project", "id"), false, false),
		tool("apply_suggestion", "Replace the commented lines with the comment's suggestion exactly as written (a new version), resolve the comment and render. Fails if the lines changed since the comment was made; then edit by hand.",
			obj(map[string]any{"project": proj, "id": map[string]any{"type": "integer"}, "render": doRender, "by": source}, "project", "id"), false, false),
		tool("restore_version", "Write the content of version n back to its file (as a new version), then render.",
			obj(map[string]any{"project": proj, "version": map[string]any{"type": "integer"}, "render": doRender, "by": source}, "project", "version"), false, false),
		tool("list_images", "Scans and photos of the real part uploaded to a project: name, byte size, pixel size (upright, after EXIF rotation), the resolution recorded in the file (dpi, 0 if it records none), camera (make and model, for a photo) and dpi_usable (false for a camera's dpi, which is a default and not a scale).",
			obj(map[string]any{"project": proj}, "project"), true, false),
		tool("read_image", "Look at one uploaded image. It comes back shrunk to max_px on its long edge, because a 1200 dpi scan is far too big to send whole.",
			obj(map[string]any{"project": proj, "image": image, "max_px": map[string]any{"type": "integer", "description": "Long edge in px, default 1000, max 3000"}}, "project", "image"), true, false),
		tool("upload_image", "Add a PNG or JPEG to a project, from exactly one of data (the file, base64 encoded) or url (an http or https address the server can reach; fetched with a 30 s timeout and the 32 MB limit). An existing image of the same name is replaced. name defaults to the file name in the URL. Big files are better sent as multipart from a shell, which needs no base64: curl -F file=@photo.jpg <server>/api/projects/<project>/images (the field is named file; the server is the same address as the MCP endpoint without /mcp).",
			obj(map[string]any{"project": proj, "name": str("File name ending in .png, .jpg or .jpeg; default the name in the URL, or required with data"),
				"data": str("The file, base64 encoded"), "url": str("http or https address of the file"), "by": source}, "project"), false, false),
		tool("delete_image", "Remove an uploaded image.",
			obj(map[string]any{"project": proj, "image": image, "by": source}, "project", "image"), false, true),
		tool("list_parts", "The named parts of the model (part() calls in the source) with their source line, bounding box and triangle count, the features (feature() points, absolute mm) and whether part renders are still running (pending). The web viewer shows the same. An unlabelled entry means geometry outside every part.",
			obj(map[string]any{"project": proj}, "project"), true, false),
		tool("measure_part", measureDesc, obj(map[string]any{"project": proj, "image": image,
			"dpi":          num("Resolution in dpi, for a scan whose file records none or whose recorded value you do not trust. The file's own dpi is a scale only when it is a scanner's: a camera's (Make/Model in the file, reported as dpi_usable false) is a default and is never used; pass dpi, grid_mm or scale_ref_mm instead"),
			"grid_mm":      num("Graph paper pitch in mm, for a photo on squared paper, lines or dots; the scale is recovered from the grid. A phone photo is not flat, so the pitch is taken where the part is"),
			"scale_ref_mm": num("Known overall length in mm, to anchor the scale when there is nothing else"),
			"thickness_mm": num("Height of the measured feature above the paper, in mm (from a caliper). With camera_mm it corrects parallax: a feature standing above the paper reads large in a photo. The measure result asks for both when the scale came from graph paper or a camera"),
			"camera_mm":    num("Lens-to-paper distance in mm (about 300 for a phone held over a sheet); with thickness_mm it corrects parallax"),
			"object":       map[string]any{"type": "string", "enum": []string{measure.ObjectAuto, measure.ObjectDark, measure.ObjectLight, measure.ObjectSplit}, "description": "How the part is found. auto (default): split for a scan, dark for a photo on a grid or from a camera. dark: the darkest thing on the paper, after the dots and lines of graph paper are removed (a black part with a soft shadow on grey-dotted paper; a shadow lighter than the part is left out). light: the lightest thing. split: the two-class light/dark threshold of a scan"},
			"threshold":    map[string]any{"type": "integer", "description": "Grey level (1-255) that separates the part from what is around it, instead of the automatic choice. For a photo with a shadow beside the part, take the level between the part and the shadow (about 50-60 for a black part on paper in a shadow); check the overlay. 0 = automatic"},
			"crop":         map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "minItems": 4, "maxItems": 4, "description": "[left, top, right, bottom] in pixels of the photo as it is shown upright (after EXIF rotation): measure only inside it. The grid is still read from the whole photo. Crop to the part instead of cutting the file"},
			"invert":       map[string]any{"type": "boolean", "description": "The part is lighter than the background (same as object light; with object dark it flips to light, with split it inverts the split)"},
			"channel":      map[string]any{"type": "string", "enum": imaging.Channels(), "description": "What to threshold on: gray for a scan; a, b or sat separate a shadow from the part by colour when brightness cannot"},
			"tol_mm":       num("Width spread allowed inside one step, default 0.08"),
			"min_step_mm":  num("Shortest step to report, default 0.5"),
			"flip":         map[string]any{"type": "boolean", "description": "Force the other end to be x = 0 (by default the rounder tip leads)"},
			"overlay":      map[string]any{"type": "boolean", "description": "Also return the annotated check image (default true)"},
			"overlay_px":   map[string]any{"type": "integer", "description": "Long edge of that image in px, default 1200; drop it to 800 if you only need a sanity check"},
		}, "project"), true, false),
	}
}

const measureDesc = `Measure a flat part from an uploaded scan or photo and return its outline in millimetres: overall length and width, every step along the part (start, end, length, width, whether it is centred) and a circle fitted to a rounded end, plus a block of OpenSCAD variables and an annotated image of what was measured.
It needs a scale: a flatbed scan records its dpi and needs nothing else. A photo needs grid_mm (graph paper, solid lines or dots), scale_ref_mm (one known length) or an explicit dpi: a camera's recorded dpi is a default and is refused. The grid is read from the whole photo, and the scale is taken where the part is, since a photo is not flat: grid_spread_pct and grid_px_per_mm_min/max say how much the scale varies across it, and above about 2% a note asks for scale_ref_mm, or a shot from further away and straight down, for better accuracy.
On a small crop the grid has few patches of paper to agree on; grid_blocks says how many did, and under 3 a note asks to check the 10 mm bar against the grid. If the dots show no square lattice the call is refused: pass dpi or scale_ref_mm.
objects_found counts the part-sized objects in the photo; only the largest is measured, the others are listed in other_objects (boxes in photo pixels) and marked in blue: pass crop to measure one of them. The overlay is zoomed to the measured part when it is small in the frame.
A long thin part whose axis bends (a spring strip, a hook edge-on) also gets centreline: points_mm (a polyline from (0,0) with the chord along +x), thickness_mm along it, median_thickness_mm, length_mm, sag_mm and circle fits for knuckles at its ends, and the OpenSCAD block gets a centreline points list. Model a bent strip from that, not from the steps.
Segmentation: object auto picks split for a scan and dark for a photo; for a photo with a shadow beside the part, crop to the part, and if the outline still takes the shadow, pass threshold between the part and the shadow.
It only sees the silhouette, so nothing in Z - thickness, boss height, bore depth, wall thickness - and no raised boss, recess or blind hole. Ask the person to measure those with a caliper instead of guessing them. For a part standing above the paper (a photo from above), pass thickness_mm and camera_mm; without them the features read large.
Always look at the overlay before using the numbers: a shadow or a caliper jaw merged into the outline is the usual way this goes wrong, and it is obvious in the picture. A glossy part with a bright reflection is outside the dark class too; the notes say when a crop or a threshold is the way round.`

type args map[string]any

func (a args) str(k string) string {
	s, _ := a[k].(string)
	return s
}

func (a args) num(k string) (int, bool) {
	f, ok := a[k].(float64)
	return int(f), ok
}

func (a args) f(k string) float64 {
	v, _ := a[k].(float64)
	return v
}

// ints reads an array of whole numbers, or nil when the key is absent. A value that is not a
// number becomes -1, which no option accepts, so the measurement reports it.
func (a args) ints(k string) []int {
	raw, ok := a[k].([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		if f, ok := v.(float64); ok {
			out = append(out, int(f))
		} else {
			out = append(out, -1)
		}
	}
	return out
}

func (a args) boolean(k string, def bool) bool {
	if b, ok := a[k].(bool); ok {
		return b
	}
	return def
}

func (a args) file() string {
	if f := a.str("file"); f != "" {
		return f
	}
	return project.MainFile
}

func (a args) by() string {
	if s := a.str("by"); s != "" {
		return s
	}
	return "agent"
}

// image is the image a tool should act on; empty means the most recent upload.
func (a args) image() string { return a.str("image") }

// measureOptions maps the tool arguments onto a measurement.
func (a args) measureOptions() measure.Options {
	return measure.Options{
		DPI:         a.f("dpi"),
		GridMM:      a.f("grid_mm"),
		ScaleRefMM:  a.f("scale_ref_mm"),
		ThicknessMM: a.f("thickness_mm"),
		CameraMM:    a.f("camera_mm"),
		Object:      a.str("object"),
		Threshold:   intOr(a, "threshold", 0),
		Crop:        a.ints("crop"),
		Invert:      a.boolean("invert", false),
		Channel:     a.str("channel"),
		TolMM:       a.f("tol_mm"),
		MinStepMM:   a.f("min_step_mm"),
		Flip:        a.boolean("flip", false),
		Overlay:     a.boolean("overlay", true),
		OverlayPx:   intOr(a, "overlay_px", 0),
	}
}

func intOr(a args, k string, def int) int {
	if n, ok := a.num(k); ok {
		return n
	}
	return def
}

func (a args) mode() app.RenderMode {
	if a.boolean("render", true) {
		return app.RenderSync
	}
	return app.NoRender
}

func (m *Server) call(ctx context.Context, name string, raw map[string]any) ([]any, error) {
	a := args(raw)
	p := a.str("project")
	switch name {
	case "list_projects":
		list, err := m.app.Store.List()
		if err != nil {
			return nil, err
		}
		return []any{jsonText(list)}, nil

	case "create_project":
		info, res, err := m.app.Create(ctx, a.str("name"), a.str("description"), a.str("source"), a.by(), app.RenderSync)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(map[string]any{"project": info, "render": summary(res)})}, nil

	case "get_project":
		info, err := m.app.Store.Get(p)
		if err != nil {
			return nil, err
		}
		files, err := m.app.Store.Files(p)
		if err != nil {
			return nil, err
		}
		comments, err := m.app.Store.Comments(p, "", false)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(map[string]any{"project": info, "files": files, "last_render": summary(m.app.LastRender(p)), "open_comments": comments})}, nil

	case "read_file":
		c, err := m.app.Store.ReadFile(p, a.file())
		if err != nil {
			return nil, err
		}
		if a.boolean("numbered", false) {
			lines := strings.Split(c, "\n")
			for i, l := range lines {
				lines[i] = fmt.Sprintf("%4d  %s", i+1, l)
			}
			c = strings.Join(lines, "\n")
		}
		return []any{text(c)}, nil

	case "write_file":
		if _, ok := a["content"].(string); !ok {
			return nil, errors.New("content is required")
		}
		res, err := m.app.WriteFile(ctx, p, a.file(), a.str("content"), a.by(), a.str("note"), a.mode())
		return saved(res, err)

	case "edit_file":
		res, err := m.app.EditFile(ctx, p, a.file(), a.str("old_string"), a.str("new_string"), a.boolean("replace_all", false), a.by(), a.str("note"), a.mode())
		return saved(res, err)

	case "delete_file":
		res, err := m.app.DeleteFile(ctx, p, a.str("file"), a.by(), app.RenderSync)
		return saved(res, err)

	case "render":
		var params map[string]string
		if raw, ok := a["params"].(map[string]any); ok {
			var err error
			if params, err = app.FormatParams(raw); err != nil {
				return nil, err
			}
		}
		res, err := m.app.Render(ctx, p, params, a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(summary(&res))}, nil

	case "snapshot":
		views := []string{}
		if vs, ok := a["views"].([]any); ok {
			for _, v := range vs {
				if s, ok := v.(string); ok {
					views = append(views, s)
				}
			}
		}
		if len(views) == 0 {
			views = []string{"iso"}
		}
		if len(views) > len(render.Views) {
			return nil, errors.New("too many views")
		}
		w, _ := a.num("width")
		h, _ := a.num("height")
		pngs, err := m.app.Snapshot(ctx, p, views, w, h, a.boolean("source", false))
		if err != nil {
			return nil, err
		}
		out := []any{}
		for i, png := range pngs {
			out = append(out, text(views[i]+" view:"), imageContent(png))
		}
		return out, nil

	case "set_description":
		info, err := m.app.SetDescription(p, a.str("description"), a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(info)}, nil

	case "list_versions":
		vs, err := m.app.Store.History(p, a.str("file"))
		if err != nil {
			return nil, err
		}
		if vs == nil {
			vs = []project.Version{}
		}
		return []any{jsonText(vs)}, nil

	case "get_version":
		n, ok := a.num("version")
		if !ok {
			return nil, errors.New("version is required")
		}
		v, c, err := m.app.Store.GetVersion(p, n)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(v), text(c)}, nil

	case "list_comments":
		cs, err := m.app.Store.Comments(p, a.str("file"), a.boolean("include_resolved", false))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(commentsWithAnchors(cs))}, nil

	case "add_comment":
		line, hasLine := a.num("line")
		file := a.str("file")
		var anchor *project.Anchor
		if raw, ok := a["anchor"]; ok && raw != nil {
			b, _ := json.Marshal(raw)
			anchor = &project.Anchor{}
			if err := json.Unmarshal(b, anchor); err != nil {
				return nil, fmt.Errorf("anchor: %w", err)
			}
		}
		if anchor == nil && !hasLine {
			return nil, errors.New("line is required (or an anchor on the model)")
		}
		end, _ := a.num("end_line")
		c, err := m.app.AddComment(p, file, line, end, a.str("body"), nil, anchor, a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(c)}, nil

	case "list_parts":
		l, err := m.app.Parts(p)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(l)}, nil

	case "reply_comment":
		id, ok := a.num("id")
		if !ok {
			return nil, errors.New("id is required")
		}
		c, err := m.app.ReplyComment(p, id, a.str("body"), a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(c)}, nil

	case "resolve_comment":
		id, ok := a.num("id")
		if !ok {
			return nil, errors.New("id is required")
		}
		if r := a.str("reply"); r != "" {
			if _, err := m.app.ReplyComment(p, id, r, a.by()); err != nil {
				return nil, err
			}
		}
		c, err := m.app.ResolveComment(p, id, a.boolean("resolved", true), a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(c)}, nil

	case "apply_suggestion":
		id, ok := a.num("id")
		if !ok {
			return nil, errors.New("id is required")
		}
		c, res, err := m.app.ApplySuggestion(ctx, p, id, a.by(), a.mode())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(map[string]any{"comment": c, "version": res.Version, "render": summary(res.Render)})}, nil

	case "restore_version":
		n, ok := a.num("version")
		if !ok {
			return nil, errors.New("version is required")
		}
		res, err := m.app.Restore(ctx, p, n, a.by(), a.mode())
		return saved(res, err)

	case "list_images":
		list, err := m.app.Images(p)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(list)}, nil

	case "read_image":
		png, w, h, err := m.app.ViewImage(p, a.image(), intOr(a, "max_px", 0))
		if err != nil {
			return nil, err
		}
		return []any{text(fmt.Sprintf("%s, shown at %dx%d px:", a.image(), w, h)), imageContent(png)}, nil

	case "upload_image":
		b, name, err := imageFromArgs(ctx, a)
		if err != nil {
			return nil, err
		}
		img, err := m.app.UploadImage(p, project.SafeImageName(name), b, a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(img)}, nil

	case "delete_image":
		if err := m.app.DeleteImage(p, a.image(), a.by()); err != nil {
			return nil, err
		}
		return []any{text("deleted " + a.image())}, nil

	case "measure_part":
		res, err := m.app.Measure(p, a.image(), a.measureOptions())
		if err != nil {
			return nil, err
		}
		out := []any{jsonText(res)}
		if len(res.Overlay) > 0 {
			out = append(out, text("What was measured (red: the outline found; blue: the step boundaries; orange: the circle fitted to the round end). Check it before using the numbers:"),
				imageContent(res.Overlay))
		}
		return out, nil
	}
	return nil, unknownTool(name)
}

// summary is a render result without the raw log when the parsed lines say it all. It names
// the parts and features the model declares.
func summary(r *render.Result) any {
	if r == nil {
		return nil
	}
	s := *r
	if s.OK && len(s.Errors) == 0 {
		s.Log = ""
	}
	if len(s.Labels.Parts) == 0 && len(s.Labels.Features) == 0 {
		return s
	}
	b, _ := json.Marshal(s)
	var m map[string]any
	json.Unmarshal(b, &m)
	m["parts"] = s.Labels.Parts
	feats := []string{}
	for _, f := range s.Labels.Features {
		feats = append(feats, f.Name)
	}
	m["features"] = feats
	return m
}

// commentsWithAnchors adds anchor_text, a readable form of the anchor, to each comment.
func commentsWithAnchors(cs []project.Comment) []any {
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		b, _ := json.Marshal(c)
		var m map[string]any
		json.Unmarshal(b, &m)
		if c.Anchor != nil {
			m["anchor_text"] = describeAnchor(*c.Anchor)
		}
		out = append(out, m)
	}
	return out
}

// describeAnchor reads like "on lid, face at (12.0, 3.5, 20.0) normal +Z".
func describeAnchor(a project.Anchor) string {
	where := "the model"
	if a.Part != "" {
		where = a.Part
	}
	var s string
	switch a.Kind {
	case project.KindFace:
		s = fmt.Sprintf("on %s, face at %s", where, vecText(a.Point))
		if a.Normal != nil {
			s += " normal " + axisText(*a.Normal)
		}
	case project.KindEdge:
		if a.Edge != nil {
			s = fmt.Sprintf("on %s, edge from %s to %s", where, vecText(a.Edge[0]), vecText(a.Edge[1]))
		} else {
			s = fmt.Sprintf("on %s, edge at %s", where, vecText(a.Point))
		}
	default:
		s = fmt.Sprintf("on %s, point at %s", where, vecText(a.Point))
	}
	if a.Stale {
		s += " (stale: no geometry there any more)"
	}
	return s
}

func vecText(v [3]float64) string {
	return fmt.Sprintf("(%.1f, %.1f, %.1f)", v[0], v[1], v[2])
}

// axisText names a direction: +Z for an axis, the numbers otherwise.
func axisText(n [3]float64) string {
	axes := "XYZ"
	for i := range 3 {
		if math.Abs(n[i]) > 0.99 {
			sign := "+"
			if n[i] < 0 {
				sign = "-"
			}
			return sign + string(axes[i])
		}
	}
	return vecText(n)
}

func saved(res app.SaveResult, err error) ([]any, error) {
	if err != nil {
		return nil, err
	}
	return []any{jsonText(map[string]any{"version": res.Version, "render": summary(res.Render)})}, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func imageContent(png []byte) map[string]any {
	return map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(png)}
}

func jsonText(v any) map[string]any {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return text(strings.TrimSuffix(sb.String(), "\n"))
}
