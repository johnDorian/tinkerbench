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
	"time"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/app"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/slicer"
)

var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// sliceWait is how long the slice tool waits for the result before returning the state.
var sliceWait = 10 * time.Minute

const instructionsTmpl = `OrcaSlicer front end for the Anycubic Kobra 3. A person watches the web UI (%s): the bed view, the plates and the printer status update live as you work.
Units are millimetres. The bed is %s; the origin is the front-left corner, X runs right, Y away from the viewer, Z is up. Objects always sit on the bed (their lowest point is at z=0); x and y are the centre of the object's footprint.
Workflow: import_openscad (or upload_model with a base64 STL) -> create_plate (presets default to the printer's) -> add_object (position is automatic unless x/y given) -> move_object / arrange -> slice (waits for the result: time, filament, layers and warnings) -> printer_status -> print_plate with confirm=true.
Rotation is degrees about X, Y and Z, applied in that order around the model's centre. scale is a uniform factor.
Overrides are OrcaSlicer setting keys with string values, applied on top of the presets (update_plate with overrides; a null value removes one). Use get_profile to see the keys and values of a preset. Common ones: layer_height, sparse_infill_density ("20%%"), wall_loops, enable_support ("1"/"0"), brim_type. Leave temperatures to the filament profile.
A slice that is stale (the plate changed after it) must be sliced again before printing. print_plate, pause_print, resume_print and cancel_print act on the real printer: they need confirm=true, and only do something when the printer is idle (print) or printing or paused (pause, cancel).`

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
func New(a *app.App, uiURL, bed string) *Server {
	return &Server{app: a, instructions: fmt.Sprintf(instructionsTmpl, uiURL, bed)}
}

func (m *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
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
			continue // notification
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
			"serverInfo":      map[string]any{"name": "orcaslicer", "version": "1.0.0"},
			"instructions":    m.instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
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
		content, err := m.call(ctx, p.Name, args(p.Arguments))
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

type args map[string]any

func (a args) str(k string) string {
	s, _ := a[k].(string)
	return s
}

func (a args) num(k string) (float64, bool) {
	f, ok := a[k].(float64)
	return f, ok
}

func (a args) ptr(k string) *float64 {
	if f, ok := a.num(k); ok {
		return &f
	}
	return nil
}

func (a args) rotation() (*[3]float64, error) {
	raw, ok := a["rotation"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) != 3 {
		return nil, errors.New("rotation must be [rx, ry, rz] in degrees")
	}
	var r [3]float64
	for i, v := range list {
		f, ok := v.(float64)
		if !ok {
			return nil, errors.New("rotation must be [rx, ry, rz] in degrees")
		}
		r[i] = f
	}
	return &r, nil
}

func (a args) confirmed() error {
	if c, _ := a["confirm"].(bool); !c {
		return errors.New("this acts on the real printer: call again with confirm: true")
	}
	return nil
}

func tools() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	num := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	plate := str("Plate id")
	object := str("Object id on the plate (o1, o2, ...)")
	model := str("Model id")
	preset := str("Preset name, as list_profiles returns it")
	confirm := map[string]any{"type": "boolean", "description": "Must be true: this acts on the real printer"}
	rot := map[string]any{"type": "array", "items": map[string]any{"type": "number"}, "minItems": 3, "maxItems": 3,
		"description": "Rotation in degrees about X, Y and Z, applied in that order around the model's centre"}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	tool := func(name, desc string, schema map[string]any, readOnly, destructive bool) map[string]any {
		return map[string]any{"name": name, "description": desc, "inputSchema": schema,
			"annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive}}
	}
	return []map[string]any{
		tool("list_profiles", "Machines, and the processes and filaments compatible with a machine (default: the Kobra 3): name, layer height, filament type and vendor.",
			obj(map[string]any{"machine": preset}), true, false),
		tool("get_profile", "All settings of one preset (inheritance flattened). Values are strings or lists of strings; use these keys as overrides.",
			obj(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"machine", "process", "filament"}}, "name": preset}, "kind", "name"), true, false),
		tool("list_openscad_projects", "Projects of the OpenSCAD workbench, with whether each has a rendered model to import.",
			obj(map[string]any{}), true, false),
		tool("import_openscad", "Import the last rendered model of an OpenSCAD project as a model (re-importing replaces the model's geometry in place, so plates using it get stale slices). With plate, also place it there.",
			obj(map[string]any{"project": str("OpenSCAD project name"), "plate": plate}, "project"), false, false),
		tool("list_models", "All models (STL files) in the library: id, name, source, size in mm and triangles.",
			obj(map[string]any{}), true, false),
		tool("upload_model", "Add an STL (ASCII or binary) from base64 data.",
			obj(map[string]any{"name": str("Model name"), "data": str("The STL file, base64 encoded")}, "data"), false, false),
		tool("list_plates", "All plates, most recently changed first.", obj(map[string]any{}), true, false),
		tool("get_plate", "A plate: presets, overrides and objects (position, rotation, scale, bounds and transform in mm).",
			obj(map[string]any{"plate": plate}, "plate"), true, false),
		tool("create_plate", "Create a plate. Presets default to the printer's defaults. The bed type and bed temperature follow the printer and filament profiles; there is nothing to set for them.",
			obj(map[string]any{"name": str("Plate name"), "machine": preset, "process": preset, "filament": preset}, "name"), false, false),
		tool("update_plate", "Change a plate's name, presets or overrides. overrides are merged into the existing ones; a null value removes that key. Do not override bed temperatures or the bed type; they follow the printer and filament profiles.",
			obj(map[string]any{"plate": plate, "name": str("Plate name"), "machine": preset, "process": preset, "filament": preset,
				"overrides": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": []string{"string", "null"}}}}, "plate"), false, false),
		tool("add_object", "Put a model on a plate. Without x and y it goes to free space near the middle of the bed.",
			obj(map[string]any{"plate": plate, "model": model, "x": num("Centre of the footprint, mm from the left"), "y": num("Centre of the footprint, mm from the front"),
				"rotation": rot, "scale": num("Uniform scale, default 1")}, "plate", "model"), false, false),
		tool("move_object", "Change an object's position, rotation or scale. Only the given fields change.",
			obj(map[string]any{"plate": plate, "object": object, "x": num("mm"), "y": num("mm"), "rotation": rot, "scale": num("Uniform scale")}, "plate", "object"), false, false),
		tool("remove_object", "Take an object off a plate (the model stays in the library).",
			obj(map[string]any{"plate": plate, "object": object}, "plate", "object"), false, true),
		tool("arrange", "Pack all objects of a plate onto the bed in rows, centred, keeping their rotations and scales.",
			obj(map[string]any{"plate": plate, "spacing": num("Gap between objects in mm, default 6")}, "plate"), false, false),
		tool("slice", "Slice a plate and wait (up to 10 minutes) for the result: status, estimated time, filament, layers, G-code name and warnings. Object outside the bed or too tall stops the slice.",
			obj(map[string]any{"plate": plate}, "plate"), false, false),
		tool("printer_status", "The Kobra 3: online, state (standby, printing, paused, complete, cancelled, error, offline), file, progress, time left and temperatures.",
			obj(map[string]any{}), true, false),
		tool("print_plate", "Upload a sliced plate's G-code to the printer and start it. Refused if the plate is not sliced, is stale, or the printer is busy or offline.",
			obj(map[string]any{"plate": plate, "confirm": confirm}, "plate", "confirm"), false, false),
		tool("pause_print", "Pause the current print.", obj(map[string]any{"confirm": confirm}, "confirm"), false, false),
		tool("resume_print", "Resume a paused print.", obj(map[string]any{"confirm": confirm}, "confirm"), false, false),
		tool("cancel_print", "Cancel the current print. It can't be resumed.", obj(map[string]any{"confirm": confirm}, "confirm"), false, true),
	}
}

func (m *Server) call(ctx context.Context, name string, a args) ([]any, error) {
	switch name {
	case "list_profiles":
		l, err := m.app.ListProfiles(a.str("machine"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(l)}, nil

	case "get_profile":
		p, err := m.app.Profile(a.str("kind"), a.str("name"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "list_openscad_projects":
		list, err := m.app.OpenSCADProjects(ctx)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(list)}, nil

	case "import_openscad":
		mod, err := m.app.ImportOpenSCAD(ctx, a.str("project"), a.str("plate"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(mod)}, nil

	case "list_models":
		list, err := m.app.ListModels()
		if err != nil {
			return nil, err
		}
		return []any{jsonText(list)}, nil

	case "upload_model":
		raw, ok := a["data"].(string)
		if !ok {
			return nil, errors.New("data is required (the STL, base64 encoded)")
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("data is not valid base64: %w", err)
		}
		mod, err := m.app.UploadModel(a.str("name"), b)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(mod)}, nil

	case "list_plates":
		list, err := m.app.ListPlates()
		if err != nil {
			return nil, err
		}
		return []any{jsonText(list)}, nil

	case "get_plate":
		p, err := m.app.GetPlate(a.str("plate"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "create_plate":
		p, err := m.app.CreatePlate(a.str("name"), a.str("machine"), a.str("process"), a.str("filament"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "update_plate":
		patch := app.PlatePatch{}
		if v, ok := a["name"].(string); ok {
			patch.Name = &v
		}
		for k, dst := range map[string]**string{"machine": &patch.Machine, "process": &patch.Process, "filament": &patch.Filament} {
			if v, ok := a[k].(string); ok {
				*dst = &v
			}
		}
		if ov, ok := a["overrides"].(map[string]any); ok {
			patch.Overrides = map[string]*string{}
			for k, v := range ov {
				if v == nil {
					patch.Overrides[k] = nil
					continue
				}
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("override %q must be a string", k)
				}
				patch.Overrides[k] = &s
			}
		}
		p, err := m.app.UpdatePlate(a.str("plate"), patch)
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "add_object":
		rot, err := a.rotation()
		if err != nil {
			return nil, err
		}
		p, err := m.app.AddObject(a.str("plate"), app.ObjectSpec{
			Model: a.str("model"), X: a.ptr("x"), Y: a.ptr("y"), Rotation: rot, Scale: a.ptr("scale"),
		})
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "move_object":
		rot, err := a.rotation()
		if err != nil {
			return nil, err
		}
		p, err := m.app.PatchObject(a.str("plate"), a.str("object"), app.ObjectPatch{
			X: a.ptr("x"), Y: a.ptr("y"), Rotation: rot, Scale: a.ptr("scale"),
		})
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "remove_object":
		p, err := m.app.RemoveObject(a.str("plate"), a.str("object"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "arrange":
		p, err := m.app.Arrange(a.str("plate"), a.ptr("spacing"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(p)}, nil

	case "slice":
		res, err := m.slice(ctx, a.str("plate"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(res)}, nil

	case "printer_status":
		return []any{jsonText(m.app.PrinterStatus(ctx))}, nil

	case "print_plate":
		if err := a.confirmed(); err != nil {
			return nil, err
		}
		filename, err := m.app.PrintPlate(ctx, a.str("plate"))
		if err != nil {
			return nil, err
		}
		return []any{jsonText(map[string]string{"filename": filename, "state": "uploaded and started"})}, nil

	case "pause_print", "resume_print", "cancel_print":
		if err := a.confirmed(); err != nil {
			return nil, err
		}
		action := strings.TrimSuffix(name, "_print")
		if err := m.app.PrinterAction(ctx, action); err != nil {
			return nil, err
		}
		return []any{jsonText(m.app.PrinterStatus(ctx))}, nil
	}
	return nil, unknownTool(name)
}

// slice queues a slice and waits for it to finish, up to sliceWait.
func (m *Server) slice(ctx context.Context, plateID string) (slicer.Result, error) {
	res, err := m.app.Slice(plateID)
	if err != nil {
		return slicer.Result{}, err
	}
	deadline := time.NewTimer(sliceWait)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for res.Status == slicer.StatusQueued || res.Status == slicer.StatusSlicing {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-deadline.C:
			res.Warnings = append(res.Warnings, "still slicing after 10 minutes; call slice again or get the status later")
			return res, nil
		case <-tick.C:
		}
		if res, err = m.app.SliceStatus(plateID); err != nil {
			return res, err
		}
	}
	return res, nil
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

func jsonText(v any) map[string]any {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return text(strings.TrimSuffix(sb.String(), "\n"))
}
