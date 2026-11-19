package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testapp"
	"github.com/johnDorian/tinkerbench/orcaslicer/internal/testkit"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(testapp.NewApp(t), "http://orcaslicer.test", "255x255 mm, 260 mm high"))
	t.Cleanup(srv.Close)
	return srv
}

func rpc(t *testing.T, srv *httptest.Server, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	res, err := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Result map[string]any `json:"result"`
		Error  *rpcErr        `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error != nil {
		t.Fatalf("%s: rpc error %d %s", method, out.Error.Code, out.Error.Message)
	}
	return out.Result
}

// callTool returns the text of the tool result and whether it is an error.
func callTool(t *testing.T, srv *httptest.Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	res := rpc(t, srv, "tools/call", map[string]any{"name": name, "arguments": args})
	var sb strings.Builder
	for _, c := range res["content"].([]any) {
		sb.WriteString(c.(map[string]any)["text"].(string))
	}
	isErr, _ := res["isError"].(bool)
	return sb.String(), isErr
}

func TestToolsListAndInitialize(t *testing.T) {
	srv := testServer(t)
	init := rpc(t, srv, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if !strings.Contains(init["instructions"].(string), "http://orcaslicer.test") {
		t.Fatalf("instructions do not name the UI: %v", init["instructions"])
	}
	list := rpc(t, srv, "tools/list", map[string]any{})
	names := map[string]bool{}
	for _, tl := range list["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"list_profiles", "get_profile", "list_openscad_projects", "import_openscad", "list_models",
		"upload_model", "list_plates", "get_plate", "create_plate", "update_plate", "add_object", "move_object", "remove_object",
		"arrange", "slice", "printer_status", "print_plate", "pause_print", "resume_print", "cancel_print"} {
		if !names[want] {
			t.Errorf("tool %s missing", want)
		}
	}
}

func TestPlateToolsAndConfirmations(t *testing.T) {
	srv := testServer(t)
	stl := base64.StdEncoding.EncodeToString(testkit.Cube([3]float64{}, 10))
	if txt, isErr := callTool(t, srv, "upload_model", map[string]any{"name": "cube", "data": stl}); isErr {
		t.Fatal(txt)
	}
	created, isErr := callTool(t, srv, "create_plate", map[string]any{"name": "Case"})
	if isErr {
		t.Fatal(created)
	}
	var plate struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(created), &plate)
	models, _ := callTool(t, srv, "list_models", map[string]any{})
	var ms []struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(models), &ms)
	if len(ms) != 1 {
		t.Fatalf("models = %s", models)
	}
	if txt, isErr := callTool(t, srv, "add_object", map[string]any{"plate": plate.ID, "model": ms[0].ID, "rotation": []any{0, 0, 45}}); isErr {
		t.Fatal(txt)
	}
	if txt, isErr := callTool(t, srv, "update_plate", map[string]any{"plate": plate.ID, "overrides": map[string]any{"wall_loops": "3"}}); isErr {
		t.Fatal(txt)
	}
	// printer actions need confirm=true
	if txt, isErr := callTool(t, srv, "print_plate", map[string]any{"plate": plate.ID}); !isErr || !strings.Contains(txt, "confirm") {
		t.Fatalf("print without confirm: %q %v", txt, isErr)
	}
	if _, isErr := callTool(t, srv, "cancel_print", map[string]any{"confirm": true}); !isErr {
		t.Fatal("cancel with printing disabled should be a tool error")
	}
	// unknown tool is a protocol error
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "nope"}})
	res, _ := http.Post(srv.URL, "application/json", bytes.NewReader(body))
	var out struct {
		Error *rpcErr `json:"error"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if out.Error == nil || out.Error.Code != -32602 {
		t.Fatalf("unknown tool error = %+v", out.Error)
	}
}
