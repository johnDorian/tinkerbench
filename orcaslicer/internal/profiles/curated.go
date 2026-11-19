package profiles

// Setting is one of the few OrcaSlicer settings the UI shows directly. Everything else goes
// through the API or MCP as an override key.
type Setting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"` // number, int, percent, select or bool ("0"/"1")
	Unit    string   `json:"unit,omitempty"`
	Options []string `json:"options,omitempty"`
	Source  Kind     `json:"source"`  // which preset the key lives in: process or filament
	Default string   `json:"default"` // the value in the default preset, filled in by the app
}

// Curated lists the settings in the order the UI shows them.
var Curated = []Setting{
	{Key: "layer_height", Label: "Layer height", Kind: "number", Unit: "mm", Source: Process},
	{Key: "sparse_infill_density", Label: "Infill", Kind: "percent", Unit: "%", Source: Process},
	{Key: "sparse_infill_pattern", Label: "Infill pattern", Kind: "select", Source: Process,
		Options: []string{"grid", "gyroid", "cubic", "triangles", "honeycomb", "lightning", "adaptivecubic"}},
	{Key: "wall_loops", Label: "Walls", Kind: "int", Source: Process},
	{Key: "enable_support", Label: "Supports", Kind: "bool", Source: Process},
	{Key: "support_type", Label: "Support type", Kind: "select", Source: Process,
		Options: []string{"normal(auto)", "tree(auto)"}},
	{Key: "brim_type", Label: "Brim", Kind: "select", Source: Process,
		Options: []string{"auto_brim", "no_brim", "outer_only"}},
	{Key: "brim_width", Label: "Brim width", Kind: "number", Unit: "mm", Source: Process},
	{Key: "nozzle_temperature", Label: "Nozzle temperature", Kind: "number", Unit: "°C", Source: Filament},
	{Key: "hot_plate_temp", Label: "Bed temperature", Kind: "number", Unit: "°C", Source: Filament},
}
