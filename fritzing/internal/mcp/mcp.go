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
	"net/http"
	"strings"

	"github.com/johnDorian/tinkerbench/fritzing/internal/app"
	"github.com/johnDorian/tinkerbench/fritzing/internal/parts"
	"github.com/johnDorian/tinkerbench/fritzing/internal/project"
	"github.com/johnDorian/tinkerbench/fritzing/internal/runner"
	"github.com/johnDorian/tinkerbench/fritzing/internal/sketch"
)

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const instructionsTmpl = `Fritzing workbench for designing electronics: breadboard layouts, schematics and PCBs. A person watches the web UI (%s): it shows the three views of the sketch and redraws them after every change you make.
Each project is a Fritzing sketch (sketch.fz, Fritzing's XML, packed as an .fzz when rendered) and is drawn by Fritzing %s running on the server. The three views are independent drawings of the same circuit: breadboard (parts plugged into a breadboard, wires), schematic (symbols, traces) and pcb (footprints on a board, copper traces). A wire only exists in the view you draw it in, so connect in every view you want it to show.
Workflow: search_parts (find the module_id; if the part is missing, import_part brings in a .fzpz from the Fritzing forum, GitHub or a vendor library) -> get_part (connector ids and names, properties, which views it has) -> add_part -> connect (view + two part connectors) -> describe_sketch (parts, wires and the nets they form: check the circuit here) -> check_circuit (electrical sense by pin names) -> check_layout (geometry) -> snapshot (PNG of a view; zoom with around/region to look at one spot; look once at the end rather than after every edit) -> iterate. After an edit, diff_versions says in words what changed. A render warning starting art-mismatch means Fritzing draws a part's pins somewhere else than they are. Every tool that changes the sketch renders and returns the result.
When the circuit works, call set_notes with purpose, power, build steps and what to check on the real parts, then get_wiring to read it back: it is the readable description (parts list, connections with pin names and wire colours, warnings such as a connection drawn in the schematic but not the breadboard) that a person or a later session opens first.
Units are millimetres, y grows downwards. Positions are the part's top-left corner; a part left without a position is put to the right of the others in that view. Parts have titles (LED1, R1): use them to refer to a part. Connectors are named by id (connector0) or by name from get_part (anode); ids are safest because several pins can share a name.
Parts whose shape depends on properties set them with add_part properties or set_property (resistor: resistance "220", ceramic capacitor: capacitance, LED: color, pin headers: pins). get_part lists the properties a part has and search_parts can filter on them (properties={"package":"THT"}); property_values shows which values exist.
For a breadboard, add the breadboard part, place parts so their pins sit in its holes and connect pins with breadboard wires; for a PCB add a board part (search "PCB") first and connect with traces (side bottom = copper0, top = copper1).
Every save is a version: list_versions / restore_version undo mistakes. Pass by (your name) on writes so the history shows who changed what. For anything the tools cannot do, read_file / edit_file / write_file work on the raw XML (it is validated before it is saved); custom parts are .fzp and .svg files in the project.`

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

// New creates the MCP server. uiURL and version are only used in the instructions for the agent.
func New(a *app.App, uiURL, fritzingVersion string) *Server {
	return &Server{app: a, instructions: fmt.Sprintf(instructionsTmpl, uiURL, fritzingVersion)}
}

func (m *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
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
			continue // notification: nothing to send back
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
	switch {
	case len(out) == 0:
		w.WriteHeader(http.StatusAccepted)
	case batch:
		writeJSON(w, 200, out)
	default:
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
			"serverInfo":      map[string]any{"name": "fritzing", "version": "1.0.0"},
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

func mcpTools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	integer := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	number := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	proj := str("Project name (lowercase letters, digits, - and _)")
	file := str("File, default sketch.fz")
	by := str("Who makes the change (your agent name); shown in the history")
	note := str("Short description of the change; shown in the history")
	doRender := map[string]any{"type": "boolean", "description": "Render after saving (default true)"}
	view := map[string]any{"type": "string", "enum": parts.Views, "description": "breadboard, schematic or pcb"}
	ref := str("Title of the part (LED1) or wire (Wire3)")
	connector := str("Connector id (connector0) or name (anode); see get_part")
	props := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
		"description": `Part properties, e.g. {"resistance": "220"} or {"color": "Red (633nm)"}`}
	viaSchema := map[string]any{"type": "array", "description": "Waypoints in mm (same coordinates as move_part): the wire becomes a chain of straight wires through them. A waypoint on a pin is refused.",
		"items": map[string]any{"type": "object", "properties": map[string]any{"x": number(""), "y": number("")}, "required": []string{"x", "y"}}}
	tool := func(name, desc string, schema map[string]any, readOnly, destructive bool) map[string]any {
		return map[string]any{"name": name, "description": desc, "inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive}}
	}
	return []map[string]any{
		tool("list_projects", "All projects (newest change first) with description and files.", obj(map[string]any{}), true, false),
		tool("create_project", "Create a project with an empty sketch (or the given sketch.fz XML) and render it. The web UI switches to it.",
			obj(map[string]any{"name": proj, "description": str("What the circuit is for"), "source": str("Initial sketch.fz XML, default empty"), "by": by}, "name"), false, false),
		tool("get_project", "A project's description, files, last render result and what is in the sketch (parts, wires, nets).",
			obj(map[string]any{"project": proj}, "project"), true, false),
		tool("describe_sketch", "What is in the sketch: every part with module_id, properties and position per view, every wire with its two ends, and the nets (groups of pins that are connected) per view. Use it to check the circuit.",
			obj(map[string]any{"project": proj}, "project"), true, false),
		tool("add_part", "Put a part from the library into the sketch, in every view the part has, and render. Returns the part's title. Find module_id with search_parts.",
			obj(map[string]any{"project": proj, "module_id": str("From search_parts"), "title": str("Default: the part's label + a number (LED1, R2)"),
				"properties": props,
				"positions":  map[string]any{"type": "object", "description": `Top-left corner in mm per view, e.g. {"schematic": {"x": 30, "y": 10}}; views left out are placed automatically`, "additionalProperties": map[string]any{"type": "object", "properties": map[string]any{"x": number(""), "y": number("")}}},
				"rotation":   number("Degrees clockwise, in every view"), "note": note, "render": doRender, "by": by}, "project", "module_id"), false, false),
		tool("move_part", "Move and/or rotate a part in one view; wires attached to it follow (their ends move, waypoints stay). x and y snap to a grid by default (2.54 mm in breadboard and schematic, none in pcb) so pins line up and orthogonal wires are really straight. Layout loop: place parts first (move_part, align_parts, place_near), wires second (connect style=orthogonal or tidy_view), then check_layout and snapshot.",
			obj(map[string]any{"project": proj, "part": ref, "view": view, "x": number("mm, new left edge"), "y": number("mm, new top edge"),
				"rotation": number("Degrees clockwise (absolute, not added to the current one)"),
				"snap":     number("Grid in mm to round x and y to; 0 = off. Default 2.54 (breadboard, schematic), 0 (pcb)"), "render": doRender, "by": by}, "project", "part", "view"), false, false),
		tool("align_parts", "Line parts up in one view by the box around their pins: mode left, right, top, bottom, hcenter (same centre x), vcenter (same centre y), distribute_h or distribute_v (equal gaps, three or more parts). Wires follow.",
			obj(map[string]any{"project": proj, "view": view, "parts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Titles"},
				"mode": map[string]any{"type": "string", "enum": []string{"left", "right", "top", "bottom", "hcenter", "vcenter", "distribute_h", "distribute_v"}}, "render": doRender, "by": by}, "project", "view", "parts", "mode"), false, false),
		tool("place_near", "Put a part next to another one (side left, right, above, below), centred on it, snapped to the 2.54 mm grid, with at least gap mm between the artwork boxes. Wires follow.",
			obj(map[string]any{"project": proj, "view": view, "part": ref, "anchor": ref, "side": map[string]any{"type": "string", "enum": []string{"left", "right", "above", "below"}},
				"gap": number("mm, default 5"), "render": doRender, "by": by}, "project", "view", "part", "anchor", "side"), false, false),
		tool("reroute_wire", "Replace one wire (or chain of wires) by a new route between the same two connectors, keeping colour and net. Default style orthogonal; or pass via waypoints, or style straight.",
			obj(map[string]any{"project": proj, "wire": ref, "style": map[string]any{"type": "string", "enum": []string{"straight", "orthogonal"}}, "via": viaSchema,
				"render": doRender, "by": by}, "project", "wire"), false, false),
		tool("tidy_view", "Re-route every wire of a view orthogonally (shortest first, never moves parts) and return before/after numbers (crossings, orthogonal share, wire length, tidiness). dry_run (default true) only proposes; set dry_run=false to save it as one version. Wires that cannot be routed legally are left alone and listed with the reason.",
			obj(map[string]any{"project": proj, "view": view, "dry_run": map[string]any{"type": "boolean", "description": "Default true: do not save"}, "render": doRender, "by": by}, "project", "view"), false, false),
		tool("set_property", "Set a property of a part (resistance, color, pins, ...); value \"\" removes it. Fritzing regenerates the artwork of parts that depend on it.",
			obj(map[string]any{"project": proj, "part": ref, "name": str("Property name"), "value": str("New value"), "render": doRender, "by": by}, "project", "part", "name", "value"), false, false),
		tool("rename_part", "Change a part's title.",
			obj(map[string]any{"project": proj, "part": ref, "title": str("New title"), "render": doRender, "by": by}, "project", "part", "title"), false, false),
		tool("remove", "Delete a part (with every wire attached to it) or a wire. A wire with bends is a chain of wires: the whole chain goes unless only_segment is set.",
			obj(map[string]any{"project": proj, "part": ref, "only_segment": map[string]any{"type": "boolean", "description": "Wire chains: remove just this straight wire"}, "render": doRender, "by": by}, "project", "part"), false, true),
		tool("connect", "Draw a wire (breadboard), trace (schematic) or copper trace (pcb) between two connectors of parts that are already in that view. Optional via waypoints (mm) make a chain of wires with bends. Returns the titles of all wires of the chain; remove one to undo the whole chain.",
			obj(map[string]any{"project": proj, "view": view, "from_part": ref, "from_connector": connector, "to_part": ref, "to_connector": connector,
				"side": str("pcb only: bottom (copper0, default) or top (copper1)"), "color": str("breadboard only: wire colour as #rrggbb, default blue"),
				"via":   viaSchema,
				"style": map[string]any{"type": "string", "enum": []string{"straight", "orthogonal"}, "description": "straight (default): one straight wire. orthogonal: horizontal and vertical segments with bends chosen by the server (leaves each pin on the side it faces, avoids parts and foreign pins, fewest crossings, then shortest); fails with the reason when no legal route exists."},
				"render": doRender, "by": by}, "project", "view", "from_part", "from_connector", "to_part", "to_connector"), false, false),
		tool("check_layout", "Geometry checks on one view, no picture needed: wires crossing wires (info in breadboard and schematic because wires only connect at their ends, warning on a PCB), pins of different nets touching (error: can short), wires running over foreign pins or across parts, overlapping parts, also diagonal wires, long wires and wires passing close to a foreign pin (info level), and numbers to compare before and after: wires (a chain counts once), bends, crossings, orthogonal share, total length, parts bounding box and one tidiness score 0-100 (100 minus 20 per error, 10 per warning, 1 per breadboard/schematic crossing, 3 per diagonal, 0.5 per bend, 1 per long wire). Wires that end on a part but cut through its body are reported as wire-over-part. connect, move_part and reroute_wire report the same findings for the view they changed.",
			obj(map[string]any{"project": proj, "view": view}, "project"), true, false),
		tool("check_circuit", "Electrical sanity checks by pin names, no picture needed (check_layout is the geometry check): a supply pin and ground on one net (error), two supply voltages joined (error), a part with ground pins but none connected (warn), an LED sitting straight across supply and ground without a series resistor (warn), parts with no supply connected and ESP8266 boot pins in use (info). Default: every view that has wires. get_wiring also lists the errors and warnings for its view.",
			obj(map[string]any{"project": proj, "view": view}, "project"), true, false),
		tool("render", "Render the sketch with Fritzing. Returns ok, errors, warnings, the views produced and how long it took. On failure the previous SVGs stay in the viewer.",
			obj(map[string]any{"project": proj, "by": by}, "project"), false, false),
		tool("snapshot", "Look at the sketch: PNG images of the rendered views (default all three). To check one spot at a fraction of the cost, zoom: around = a part or wire title (a wire takes its whole chain) with margin_mm (default 8), or region = {x, y, w, h} in mm (same coordinates as move_part); a zoom shows one view (the first of views, default breadboard).",
			obj(map[string]any{"project": proj, "views": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": parts.Views}},
				"width":     integer("px, default 1000, max 2400"),
				"around":    str("Zoom on this part or wire title (e.g. Q1, Wire9)"),
				"margin_mm": map[string]any{"type": "number", "description": "Space around it in mm, default 8"},
				"region": map[string]any{"type": "object", "description": "Zoom on this rectangle in mm",
					"properties": map[string]any{"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}, "w": map[string]any{"type": "number"}, "h": map[string]any{"type": "number"}}}}, "project"), true, false),
		tool("search_parts", "Search Fritzing's parts library (about 2000 parts). Every word in text has to match the title, description, tags, family or a property value, so use few, plain words (\"led red 5mm\", \"arduino uno\", \"breadboard\"). Narrow with family and properties. Obsolete parts are left out.",
			obj(map[string]any{"text": str("Words to look for"), "family": str("Part family, e.g. led, resistor, microcontroller board (arduino); see list_families"),
				"properties":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": `Required properties, value may be a prefix: {"package": "DIP"}`},
				"include_obsolete": map[string]any{"type": "boolean"},
				"source":           map[string]any{"type": "string", "enum": []string{"library", "user"}, "description": "library = Fritzing's own parts, user = parts imported with import_part (text may then be empty to list them all). Default: both, imported parts first."},
				"limit":            integer("default 25, max 200")}), true, false),
		tool("import_part", "Import one part from a .fzpz bundle into the separate imported-parts library (not Fritzing's own), where search_parts (source=user) and add_part find it. Give a public https url (a GitHub blob link works) or content_base64. The bundle is validated; warnings list pins whose artwork is missing. Imported parts are copied into the .fzz when a sketch uses them.",
			obj(map[string]any{"url": str("https URL of a .fzpz file (public internet only)"), "content_base64": str("The .fzpz file, base64 encoded"),
				"overwrite": map[string]any{"type": "boolean", "description": "Replace an imported part with the same module id"}}), false, false),
		tool("delete_user_part", "Delete an imported part from the imported-parts library. Refused while a sketch uses it unless force is set.",
			obj(map[string]any{"module_id": str("From search_parts with source=user"), "force": map[string]any{"type": "boolean"}}, "module_id"), false, true),
		tool("get_part", "A library part in detail: title, description, properties, tags, every connector (id, name, description) and the buses that join connectors inside the part, and which views it has.",
			obj(map[string]any{"module_id": str("From search_parts")}, "module_id"), true, false),
		tool("part_image", "Look at a library part: a PNG of its breadboard, schematic or pcb artwork.",
			obj(map[string]any{"module_id": str("From search_parts"), "view": map[string]any{"type": "string", "enum": []string{"breadboard", "schematic", "pcb", "icon"}}, "width": integer("px, default 500")}, "module_id"), true, false),
		tool("list_families", "Part families with how many parts each has, optionally filtered by a word.",
			obj(map[string]any{"text": str("Only families containing this"), "limit": integer("default 60")}), true, false),
		tool("property_values", "The values a property takes in the library (package, resistance, color, pins, ...), most common first, optionally within one family. Use them to filter search_parts.",
			obj(map[string]any{"name": str("Property name"), "family": str("Only within families containing this"), "limit": integer("default 60")}, "name"), true, false),
		tool("read_file", "Content of a project file (sketch.fz is Fritzing's XML), with line numbers if numbered=true.",
			obj(map[string]any{"project": proj, "file": file, "numbered": map[string]any{"type": "boolean"}}, "project"), true, false),
		tool("write_file", "Replace (or create) a project file: sketch.fz, or a custom part as name.fzp / name.svg. The XML is validated first. Renders afterwards.",
			obj(map[string]any{"project": proj, "file": file, "content": str("Full file content"), "note": note, "render": doRender, "by": by}, "project", "content"), false, false),
		tool("edit_file", "Replace old_string with new_string in a file (exact match, unique unless replace_all), then render. Cheaper than write_file for small changes.",
			obj(map[string]any{"project": proj, "file": file, "old_string": str("Exact text to replace"), "new_string": str("Replacement"),
				"replace_all": map[string]any{"type": "boolean"}, "note": note, "render": doRender, "by": by}, "project", "old_string", "new_string"), false, false),
		tool("delete_file", "Delete a file other than sketch.fz (its history is kept).",
			obj(map[string]any{"project": proj, "file": str("File to delete"), "by": by}, "project", "file"), false, true),
		tool("get_wiring", "The readable wiring description of a project as Markdown: warnings, your notes, the connections table with human pin names and wire colours, the pins in use per part, and the parts list. Derived from the sketch each time, so it is always current; use describe_sketch for the raw data. Read this first when picking up a project.",
			obj(map[string]any{"project": proj, "view": view}, "project"), true, false),
		tool("set_notes", "Save the written notes of the wiring description (notes.md). They are shown above the generated tables in get_wiring and on the wiring tab, stamped with the current sketch version so they show as out of date after later edits. Replaces the previous notes; empty text removes them. Does not render. Suggested headings: Purpose, Power, Build steps, Check before powering up (pinouts that differ from the real part, boot pins, voltage levels), Firmware (pin used, level for on/off), Not done / open questions.",
			obj(map[string]any{"project": proj, "notes": str("Markdown"), "by": by}, "project", "notes"), false, false),
		tool("set_description", "Change a project's one-line description. Keep it short: the circuit's purpose, power and build steps go into set_notes.",
			obj(map[string]any{"project": proj, "description": str("What the circuit is for"), "by": by}, "project", "description"), false, false),
		tool("list_versions", "Saved versions of a project (or of one file), oldest first: n, file, time, source, note.",
			obj(map[string]any{"project": proj, "file": str("Only this file")}, "project"), true, false),
		tool("diff_versions", "What changed between two versions of the sketch, in words, so an edit can be confirmed without a snapshot: parts added, removed, swapped, moved or changed in a property; nets joined or split per view; wires re-routed or recoloured; and the layout numbers (wires, bends, crossings, length, tidiness) and problems before and after. Defaults: to = the latest sketch version, from = the sketch version before it.",
			obj(map[string]any{"project": proj, "from": integer("Version to compare from (see list_versions, file sketch.fz)"), "to": integer("Version to compare to")}, "project"), true, false),
		tool("get_version", "Content of a file as saved in version n.",
			obj(map[string]any{"project": proj, "version": integer("")}, "project", "version"), true, false),
		tool("restore_version", "Write the content of version n back to its file (as a new version), then render.",
			obj(map[string]any{"project": proj, "version": integer(""), "render": doRender, "by": by}, "project", "version"), false, false),
	}
}

type args map[string]any

func (a args) str(k string) string {
	s, _ := a[k].(string)
	return s
}

func (a args) num(k string) (int, bool) {
	f, ok := a[k].(float64)
	return int(f), ok
}

func (a args) float(k string) (float64, bool) {
	f, ok := a[k].(float64)
	return f, ok
}

func (a args) boolean(k string, def bool) bool {
	if b, ok := a[k].(bool); ok {
		return b
	}
	return def
}

// via reads the waypoints list.
func (a args) via() []sketch.XY {
	var out []sketch.XY
	if l, ok := a["via"].([]any); ok {
		for _, v := range l {
			m, _ := v.(map[string]any)
			x, _ := args(m).float("x")
			y, _ := args(m).float("y")
			out = append(out, sketch.XY{X: x, Y: y})
		}
	}
	return out
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

func (a args) mode() app.RenderMode {
	if a.boolean("render", true) {
		return app.RenderSync
	}
	return app.NoRender
}

func (a args) strMap(k string) map[string]string {
	raw, _ := a[k].(map[string]any)
	if raw == nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for n, v := range raw {
		switch x := v.(type) {
		case string:
			out[n] = x
		case float64:
			out[n] = fmt.Sprint(x)
		case bool:
			out[n] = fmt.Sprint(x)
		}
	}
	return out
}

func optional(a args, k string) *float64 {
	if f, ok := a.float(k); ok {
		return &f
	}
	return nil
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
		out := map[string]any{"project": info, "last_render": summary(m.app.LastRender(p))}
		if sum, err := m.app.Describe(p); err == nil {
			out["sketch"] = sum
		} else {
			out["sketch_error"] = err.Error()
		}
		return []any{jsonText(out)}, nil
	case "describe_sketch":
		sum, err := m.app.Describe(p)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(sum)}, nil
	case "add_part":
		id := a.str("module_id")
		if id == "" {
			return nil, errors.New("module_id is required")
		}
		opts := sketch.AddOptions{Title: a.str("title"), Properties: a.strMap("properties")}
		opts.Rotation, _ = a.float("rotation")
		if pos, ok := a["positions"].(map[string]any); ok {
			opts.Position = map[string]sketch.XY{}
			for v, xy := range pos {
				if !parts.ValidView(v) {
					return nil, fmt.Errorf("positions: unknown view %q", v)
				}
				m, _ := xy.(map[string]any)
				x, _ := args(m).float("x")
				y, _ := args(m).float("y")
				opts.Position[v] = sketch.XY{X: x, Y: y}
			}
		}
		return m.edit(ctx, p, a, "added "+id, func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			title, err := s.AddPart(r, id, opts)
			return map[string]string{"title": title}, err
		})
	case "move_part":
		ref, view := a.str("part"), a.str("view")
		x, y, rot := optional(a, "x"), optional(a, "y"), optional(a, "rotation")
		if x == nil && y == nil && rot == nil {
			return nil, errors.New("give x, y and/or rotation")
		}
		grid := 0.0
		if view != parts.PCB {
			grid = 2.54
		}
		if g, ok := a.float("snap"); ok {
			grid = g
		}
		if x != nil {
			v := sketch.SnapMM(*x, grid)
			x = &v
		}
		if y != nil {
			v := sketch.SnapMM(*y, grid)
			y = &v
		}
		return m.edit(ctx, p, a, "moved "+ref, func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			if err := s.Move(r, ref, view, x, y, rot); err != nil {
				return nil, err
			}
			return layoutDetail(nil, s.CheckLayout(r, view)), nil
		})
	case "set_property":
		ref, n := a.str("part"), a.str("name")
		return m.edit(ctx, p, a, fmt.Sprintf("set %s.%s", ref, n), func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			return nil, s.SetProperty(ref, n, a.str("value"))
		})
	case "rename_part":
		ref := a.str("part")
		return m.edit(ctx, p, a, "renamed "+ref, func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			return nil, s.Rename(ref, a.str("title"))
		})
	case "remove":
		ref := a.str("part")
		return m.edit(ctx, p, a, "removed "+ref, func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			gone, err := s.RemoveOpt(ref, a.boolean("only_segment", false))
			return map[string]any{"removed": gone}, err
		})
	case "connect":
		from := sketch.Endpoint{Part: a.str("from_part"), Connector: a.str("from_connector")}
		to := sketch.Endpoint{Part: a.str("to_part"), Connector: a.str("to_connector")}
		view := a.str("view")
		return m.edit(ctx, p, a, fmt.Sprintf("%s: %s.%s - %s.%s", view, from.Part, from.Connector, to.Part, to.Connector), func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			titles, err := s.Connect(r, view, from, to, sketch.ConnectOptions{Side: a.str("side"), Color: a.str("color"), Via: a.via(), Style: a.str("style")})
			if err != nil {
				return nil, err
			}
			return layoutDetail(map[string]any{"wire": titles[0], "wires": titles}, s.CheckLayout(r, view)), nil
		})
	case "reroute_wire":
		ref, view := a.str("wire"), ""
		return m.edit(ctx, p, a, "rerouted "+ref, func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			titles, err := s.RerouteWire(r, ref, a.str("style"), a.via())
			if err != nil {
				return nil, err
			}
			if w, e := s.WireViewOf(titles[0]); e == nil {
				view = w
			}
			return layoutDetail(map[string]any{"wires": titles}, s.CheckLayout(r, view)), nil
		})
	case "align_parts":
		view := a.str("view")
		var titles []string
		if l, ok := a["parts"].([]any); ok {
			for _, t := range l {
				if str, ok := t.(string); ok {
					titles = append(titles, str)
				}
			}
		}
		return m.edit(ctx, p, a, "aligned "+strings.Join(titles, ","), func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			if err := s.AlignParts(r, view, titles, a.str("mode")); err != nil {
				return nil, err
			}
			return layoutDetail(nil, s.CheckLayout(r, view)), nil
		})
	case "place_near":
		view := a.str("view")
		gap, ok := a.float("gap")
		if !ok {
			gap = 5
		}
		return m.edit(ctx, p, a, "placed "+a.str("part")+" near "+a.str("anchor"), func(s *sketch.Sketch, r sketch.Resolver) (any, error) {
			if err := s.PlaceNear(r, view, a.str("part"), a.str("anchor"), a.str("side"), gap); err != nil {
				return nil, err
			}
			return layoutDetail(nil, s.CheckLayout(r, view)), nil
		})
	case "tidy_view":
		view := a.str("view")
		if a.boolean("dry_run", true) {
			res, err := m.app.Preview(p, func(s *sketch.Sketch, r sketch.Resolver) (any, error) { return s.TidyView(r, view) })
			if err != nil {
				return nil, err
			}
			return []any{jsonText(map[string]any{"dry_run": true, "result": res})}, nil
		}
		return m.edit(ctx, p, a, "tidied "+view, func(s *sketch.Sketch, r sketch.Resolver) (any, error) { return s.TidyView(r, view) })
	case "check_layout":
		rep, err := m.app.CheckLayout(p, a.str("view"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(rep)}, nil
	case "check_circuit":
		rep, err := m.app.CheckCircuit(p, a.str("view"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(rep)}, nil
	case "render":
		res, err := m.app.Render(ctx, p, a.by())
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
			views = parts.Views
		}
		w, _ := a.num("width")
		if w == 0 {
			w = 1000
		}
		w = min(w, 2400)
		out := []any{}
		if stale, rendered, changed := m.app.RenderStale(p); stale {
			when := "never rendered"
			if !rendered.IsZero() {
				when = "rendered " + rendered.Format("15:04:05")
			}
			out = append(out, text(fmt.Sprintf("WARNING: these pictures are out of date (%s, the sketch changed %s). Call render to refresh them.", when, changed.Format("15:04:05"))))
		}
		crop := app.Crop{Around: a.str("around")}
		crop.MarginMM, _ = a.float("margin_mm")
		if r, ok := a["region"].(map[string]any); ok {
			get := func(k string) float64 { f, _ := r[k].(float64); return f }
			crop.X, crop.Y, crop.W, crop.H = get("x"), get("y"), get("w"), get("h")
		}
		zoomed := crop.Around != "" || crop.W > 0
		if zoomed && len(views) == len(parts.Views) {
			views = []string{"breadboard"} // a zoom is for one view
		}
		for _, v := range views {
			var png []byte
			var err error
			if zoomed {
				png, err = m.app.SnapshotCrop(ctx, p, v, w, crop)
			} else {
				png, err = m.app.Snapshot(ctx, p, v, w)
			}
			if err != nil {
				return nil, err
			}
			out = append(out, text(v+" view:"), imageContent(png))
		}
		return out, nil
	case "search_parts":
		q := parts.Query{Text: a.str("text"), Family: a.str("family"), Properties: a.strMap("properties"), IncludeObsolete: a.boolean("include_obsolete", false), Source: a.str("source")}
		q.Limit, _ = a.num("limit")
		if q.Text == "" && q.Family == "" && len(q.Properties) == 0 && q.Source != "user" {
			return nil, errors.New("give text, family and/or properties")
		}
		hits, err := m.app.SearchParts(q)
		if err != nil {
			return nil, err
		}
		if hits == nil {
			hits = []parts.Hit{}
		}
		return []any{jsonText(hits)}, nil
	case "import_part":
		var data []byte
		if b := a.str("content_base64"); b != "" {
			var err error
			if data, err = base64.StdEncoding.DecodeString(b); err != nil {
				return nil, fmt.Errorf("content_base64: %w", err)
			}
		}
		res, err := m.app.ImportPart(ctx, data, a.str("url"), a.boolean("overwrite", false))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(res)}, nil
	case "delete_user_part":
		if err := m.app.DeleteImportedPart(a.str("module_id"), a.boolean("force", false)); err != nil {
			return nil, err
		}
		return []any{text("deleted " + a.str("module_id"))}, nil
	case "get_part":
		part, err := m.app.GetPart(a.str("module_id"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(part)}, nil
	case "part_image":
		view := a.str("view")
		if view == "" {
			view = parts.Breadboard
		}
		svg, err := m.app.PartSVG(a.str("module_id"), view)
		if err != nil {
			return nil, err
		}
		w, _ := a.num("width")
		if w == 0 {
			w = 500
		}
		png, err := m.app.Runner.PNG(ctx, svg, min(w, 2000))
		if err != nil {
			return nil, err
		}
		return []any{text(a.str("module_id") + " " + view + ":"), imageContent(png)}, nil
	case "list_families":
		n, _ := a.num("limit")
		fs, err := m.app.Families(a.str("text"), n)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(fs)}, nil
	case "property_values":
		n, _ := a.num("limit")
		vs, err := m.app.PropertyValues(a.str("name"), a.str("family"), n)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(vs)}, nil
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
	case "get_wiring":
		w, err := m.app.Wiring(p, a.str("view"))
		if err != nil {
			return nil, err
		}
		return []any{text(w.Markdown())}, nil
	case "set_notes":
		v, err := m.app.SetNotes(p, a.str("notes"), a.by())
		if err != nil {
			return nil, err
		}
		return []any{jsonText(map[string]any{"version": v.N, "saved": strings.TrimSpace(a.str("notes")) != ""})}, nil
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
	case "diff_versions":
		from, _ := a.num("from")
		to, _ := a.num("to")
		d, err := m.app.DiffVersions(p, from, to)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(d)}, nil
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
	case "restore_version":
		n, ok := a.num("version")
		if !ok {
			return nil, errors.New("version is required")
		}
		res, err := m.app.Restore(ctx, p, n, a.by(), a.mode())
		return saved(res, err)
	}
	return nil, unknownTool(name)
}

// edit runs a sketch operation and renders per the render argument.
func (m *Server) edit(ctx context.Context, p string, a args, note string, fn func(*sketch.Sketch, sketch.Resolver) (any, error)) ([]any, error) {
	res, err := m.app.Edit(ctx, p, a.by(), note, a.mode(), fn)
	return saved(res, err)
}

// summary is a render result without the raw log when the parsed lines say it all.
func summary(r *runner.Result) any {
	if r == nil {
		return nil
	}
	s := *r
	if s.OK {
		s.Log = ""
	}
	return s
}

func saved(res app.SaveResult, err error) ([]any, error) {
	if err != nil {
		return nil, err
	}
	out := map[string]any{"version": res.Version.N, "render": summary(res.Render)}
	if res.Detail != nil {
		out["result"] = res.Detail
	}
	return []any{jsonText(out)}, nil
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

// layoutDetail adds the layout findings of the changed view to a tool result, so a bad placement
// is reported the moment it is made. Nothing is added when the layout is clean.
func layoutDetail(d map[string]any, rep sketch.LayoutReport) any {
	if d == nil {
		d = map[string]any{}
	}
	if b := rep.Brief(); len(b) > 0 {
		d["layout"] = b
	}
	d["layout_stats"] = rep.Summary()
	d["layout_view"] = rep.View
	return d
}
