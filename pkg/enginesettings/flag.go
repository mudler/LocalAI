package enginesettings

// Target says where a flag lands in the model config.
type Target string

const (
	// TargetConfig is a model config key. Path is its YAML path, for
	// example "context_size" or "parameters.temperature".
	TargetConfig Target = "config"
	// TargetOption is an entry of the "options" list.
	TargetOption Target = "option"
)

// ValueType is the type of a flag value.
type ValueType string

const (
	TypeInt    ValueType = "int"
	TypeFloat  ValueType = "float"
	TypeBool   ValueType = "bool"
	TypeEnum   ValueType = "enum"
	TypeString ValueType = "string"
)

// Group is the section a setting belongs to in the editor. Each engine names
// its own groups.
type Group string

// Flag describes one setting of an engine and the model config key it maps
// to. The flag table of an engine is the only place that knows this mapping.
type Flag struct {
	// Key is a stable identifier, unique in the table.
	Key string `json:"key"`
	// Target says where the value lands in the model config.
	Target Target `json:"target"`
	// Path is the YAML path of the setting, for example "context_size",
	// "parameters.temperature" or "options".
	Path string `json:"path"`
	// Option is the option name for TargetOption flags. For raw passthrough
	// flags it is the flag itself (for example "-ot").
	Option string `json:"option,omitempty"`
	// OptionAliases are other option names the backend reads for the same
	// setting. The editor reads them and writes Option.
	OptionAliases []string `json:"option_aliases,omitempty"`
	// CLI lists the accepted command-line spellings. The first one is used
	// when a command is built. Empty for an engine with no command line.
	CLI []string `json:"cli,omitempty"`
	// CLINegated lists spellings that set a bool flag to false.
	CLINegated []string  `json:"cli_negated,omitempty"`
	Type       ValueType `json:"type"`
	Enum       []string  `json:"enum,omitempty"`
	Min        *float64  `json:"min,omitempty"`
	Max        *float64  `json:"max,omitempty"`
	// Default describes what applies when the setting is not set.
	Default string `json:"default,omitempty"`
	Doc     string `json:"doc"`
	Group   Group  `json:"group"`
	// Repeatable marks flags that may appear more than once.
	Repeatable bool `json:"repeatable,omitempty"`

	// BareOption writes a true bool as the bare option name ("no_op_offload").
	BareOption bool `json:"-"`
	// OptionalValue marks a flag that may appear without a value; BareValue
	// is the value a bare appearance means.
	OptionalValue bool   `json:"-"`
	BareValue     string `json:"-"`
	// Raw marks a passthrough flag: its options entry is "<flag>:<value>",
	// kept in the order it appeared.
	Raw bool `json:"-"`
}

// F64 returns a pointer to v, for the Min and Max of a flag.
func F64(v float64) *float64 { return &v }
