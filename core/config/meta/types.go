package meta

// FieldMeta describes a single configuration field for UI rendering and agent discovery.
type FieldMeta struct {
	Path        string        `json:"path"`                  // dot-path: "context_size", "function.grammar.parallel_calls"
	YAMLKey     string        `json:"yaml_key"`              // leaf yaml key
	GoType      string        `json:"go_type"`               // "*int", "string", "[]string"
	UIType      string        `json:"ui_type"`               // "string", "int", "float", "bool", "[]string", "map", "object"
	Pointer     bool          `json:"pointer,omitempty"`     // true = nil means "not set"
	Section     string        `json:"section"`               // "general", "llm", "templates", etc.
	Label       string        `json:"label"`                 // human-readable label
	Description string        `json:"description,omitempty"` // help text
	Component   string        `json:"component"`             // "input", "number", "toggle", "select", "slider", etc.
	Placeholder string        `json:"placeholder,omitempty"`
	Default     any           `json:"default,omitempty"`
	Min         *float64      `json:"min,omitempty"`
	Max         *float64      `json:"max,omitempty"`
	Step        *float64      `json:"step,omitempty"`
	Options     []FieldOption `json:"options,omitempty"`

	AutocompleteProvider string `json:"autocomplete_provider,omitempty"` // "backends", "models:chat", etc.
	VRAMImpact           bool   `json:"vram_impact,omitempty"`
	Advanced             bool   `json:"advanced,omitempty"`
	Order                int    `json:"order"`
}

// FieldOption represents a choice in a select/enum field.
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Section groups related fields in the UI.
type Section struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Icon  string `json:"icon,omitempty"`
	Order int    `json:"order"`
}

// ConfigMetadata is the top-level response for the metadata API.
type ConfigMetadata struct {
	Sections []Section   `json:"sections"`
	Fields   []FieldMeta `json:"fields"`
}

// FieldMetaOverride holds registry overrides that are merged on top of
// the reflection-discovered defaults. Only non-zero fields override.
type FieldMetaOverride struct {
	Section              string
	Label                string
	Description          string
	Component            string
	Placeholder          string
	Default              any
	Min                  *float64
	Max                  *float64
	Step                 *float64
	Options              []FieldOption
	AutocompleteProvider string
	VRAMImpact           bool
	Advanced             bool
	Order                int
}

// DefaultSections defines the well-known config sections in display order.
func DefaultSections() []Section {
	return []Section{
		{ID: "general", Label: "General", Icon: "settings", Order: 0},
		{ID: "llm", Label: "LLM", Icon: "cpu", Order: 10},
		{ID: "parameters", Label: "Parameters", Icon: "sliders", Order: 20},
		{ID: "templates", Label: "Templates", Icon: "file-text", Order: 30},
		{ID: "functions", Label: "Functions / Tools", Icon: "tool", Order: 40},
		{ID: "reasoning", Label: "Reasoning", Icon: "brain", Order: 45},
		{ID: "diffusers", Label: "Diffusers", Icon: "image", Order: 50},
		{ID: "tts", Label: "TTS", Icon: "volume-2", Order: 55},
		{ID: "pipeline", Label: "Pipeline", Icon: "git-merge", Order: 60},
		{ID: "grpc", Label: "gRPC", Icon: "server", Order: 65},
		{ID: "agent", Label: "Agent", Icon: "bot", Order: 70},
		{ID: "mcp", Label: "MCP", Icon: "plug", Order: 75},
		{ID: "other", Label: "Other", Icon: "more-horizontal", Order: 100},
	}
}
