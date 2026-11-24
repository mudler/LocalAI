package llamacppsettings

// Target says where a flag lands in the model config.
type Target string

const (
	// TargetField is a top-level model YAML key.
	TargetField Target = "field"
	// TargetParameter is a key under "parameters".
	TargetParameter Target = "parameter"
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

// Group is the section a setting belongs to in the editor.
type Group string

const (
	GroupMemory    Group = "memory"
	GroupSpeed     Group = "speed"
	GroupSampling  Group = "sampling"
	GroupTemplate  Group = "template"
	GroupPlacement Group = "placement"
)

// Flag describes one supported llama-server flag and the model config key it
// maps to. The Flags table is the only place that knows this mapping.
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
	// CLI lists the accepted command-line spellings. The first one is used
	// when a command is built.
	CLI []string `json:"cli"`
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

	optionAliases []string // other option names the C++ backend accepts
	bareOption    bool     // a true bool is written as the bare option name
	optionalValue bool     // the flag may appear without a value
	bareValue     string   // value implied by a bare appearance
	raw           bool     // option is a raw "-flag:value" passthrough entry

	intPtr   func(*Settings) **int
	floatPtr func(*Settings) **float64
	boolPtr  func(*Settings) **bool
	strPtr   func(*Settings) *string
	strPP    func(*Settings) **string
}

func f64(v float64) *float64 { return &v }

var cacheTypes = []string{"f32", "f16", "bf16", "q8_0", "q4_0", "q4_1", "iq4_nl", "q5_0", "q5_1"}

// Flags is the table of supported llama-server flags. Its order is the order
// of a built command and of the known entries in "options".
var Flags = []Flag{
	{
		Key: "context_size", Target: TargetField, Path: "context_size",
		CLI: []string{"--ctx-size", "-c"}, Type: TypeInt, Min: f64(0),
		Default: "sized from the model and the free memory", Group: GroupMemory,
		Doc:    "Context window in tokens. 0 uses the size stored in the model file.",
		intPtr: func(s *Settings) **int { return &s.ContextSize },
	},
	{
		Key: "gpu_layers", Target: TargetField, Path: "gpu_layers",
		CLI: []string{"-ngl", "--gpu-layers", "--n-gpu-layers"}, Type: TypeInt, Min: f64(-1),
		Default: "all layers", Group: GroupMemory,
		Doc:    "Number of layers to offload to the GPU. A large value such as 99 offloads every layer.",
		intPtr: func(s *Settings) **int { return &s.GPULayers },
	},
	{
		Key: "threads", Target: TargetField, Path: "threads",
		CLI: []string{"-t", "--threads"}, Type: TypeInt, Min: f64(-1),
		Default: "backend default", Group: GroupSpeed,
		Doc:    "CPU threads used while generating tokens.",
		intPtr: func(s *Settings) **int { return &s.Threads },
	},
	{
		Key: "batch", Target: TargetParameter, Path: "parameters.batch",
		CLI: []string{"-b", "--batch-size"}, Type: TypeInt, Min: f64(1),
		Default: "backend default", Group: GroupSpeed,
		Doc:    "Logical batch size: the most prompt tokens processed in one step.",
		intPtr: func(s *Settings) **int { return &s.params().Batch },
	},
	{
		Key: "flash_attention", Target: TargetField, Path: "flash_attention",
		CLI: []string{"-fa", "--flash-attn"}, Type: TypeEnum, Enum: []string{"on", "off", "auto"},
		Default: "auto", Group: GroupSpeed,
		Doc:           "Flash attention. A bare flag means on.",
		optionalValue: true, bareValue: "on",
		strPP: func(s *Settings) **string { return &s.FlashAttention },
	},
	{
		Key: "cache_type_k", Target: TargetField, Path: "cache_type_k",
		CLI: []string{"-ctk", "--cache-type-k"}, Type: TypeEnum, Enum: cacheTypes,
		Default: "f16", Group: GroupMemory,
		Doc:    "Data type of the K half of the KV cache. Smaller types save memory at some cost in quality.",
		strPtr: func(s *Settings) *string { return &s.CacheTypeK },
	},
	{
		Key: "cache_type_v", Target: TargetField, Path: "cache_type_v",
		CLI: []string{"-ctv", "--cache-type-v"}, Type: TypeEnum, Enum: cacheTypes,
		Default: "f16", Group: GroupMemory,
		Doc:    "Data type of the V half of the KV cache. A quantized V cache needs flash attention.",
		strPtr: func(s *Settings) *string { return &s.CacheTypeV },
	},
	{
		Key: "mmap", Target: TargetField, Path: "mmap",
		CLI: []string{"--mmap"}, CLINegated: []string{"--no-mmap"}, Type: TypeBool,
		Default: "false", Group: GroupMemory,
		Doc:     "Map the model file into memory instead of reading it. With mmap off and no mlock, the load mode is none.",
		boolPtr: func(s *Settings) **bool { return &s.MMap },
	},
	{
		Key: "mmlock", Target: TargetField, Path: "mmlock",
		CLI: []string{"--mlock"}, CLINegated: []string{"--no-mlock"}, Type: TypeBool,
		Default: "false", Group: GroupMemory,
		Doc:     "Lock the model in RAM so the system cannot swap it out.",
		boolPtr: func(s *Settings) **bool { return &s.MMlock },
	},
	{
		Key: "use_jinja", Target: TargetOption, Path: "options", Option: "use_jinja",
		CLI: []string{"--jinja"}, CLINegated: []string{"--no-jinja"}, Type: TypeBool,
		Default: "backend default", Group: GroupTemplate,
		Doc:           "Render the chat template with the Jinja engine of llama.cpp.",
		optionAliases: []string{"jinja"},
	},
	{
		Key: "parallel", Target: TargetOption, Path: "options", Option: "parallel",
		CLI: []string{"-np", "--parallel"}, Type: TypeInt, Min: f64(-1),
		Default: "1", Group: GroupSpeed,
		Doc:           "Number of requests served at the same time (server slots). The context is shared between the slots.",
		optionAliases: []string{"n_parallel"},
	},
	{
		Key: "ubatch", Target: TargetOption, Path: "options", Option: "ubatch",
		CLI: []string{"-ub", "--ubatch-size"}, Type: TypeInt, Min: f64(1),
		Default: "same as the batch size", Group: GroupSpeed,
		Doc:           "Physical batch size: the most tokens one compute step handles. Smaller values use less memory.",
		optionAliases: []string{"n_ubatch"},
	},
	{
		Key: "threads_batch", Target: TargetOption, Path: "options", Option: "threads_batch",
		CLI: []string{"-tb", "--threads-batch"}, Type: TypeInt, Min: f64(-1),
		Default: "same as threads", Group: GroupSpeed,
		Doc:           "CPU threads used while processing the prompt.",
		optionAliases: []string{"n_threads_batch"},
	},
	{
		Key: "fit", Target: TargetOption, Path: "options", Option: "fit",
		CLI: []string{"-fit", "--fit"}, Type: TypeEnum, Enum: []string{"on", "off"},
		Default: "on", Group: GroupMemory,
		Doc:           "Let llama.cpp lower the context size and the offload so the model fits in device memory.",
		optionAliases: []string{"fit_params"},
		optionalValue: true, bareValue: "on",
	},
	{
		Key: "no_op_offload", Target: TargetOption, Path: "options", Option: "no_op_offload",
		CLI: []string{"--no-op-offload"}, CLINegated: []string{"--op-offload"}, Type: TypeBool,
		Default: "false", Group: GroupSpeed,
		Doc:        "Do not offload host tensor operations to the GPU.",
		bareOption: true,
	},
	{
		Key: "direct_io", Target: TargetOption, Path: "options", Option: "direct_io",
		CLI: []string{"-dio", "--direct-io"}, CLINegated: []string{"--no-direct-io"}, Type: TypeBool,
		Default: "false", Group: GroupMemory,
		Doc:           "Read the model file with direct I/O, bypassing the page cache. Takes priority over mmap and mlock.",
		optionAliases: []string{"use_direct_io"},
	},
	{
		Key: "temperature", Target: TargetParameter, Path: "parameters.temperature",
		CLI: []string{"--temp", "--temperature"}, Type: TypeFloat, Min: f64(0),
		Default: "request value, then model default", Group: GroupSampling,
		Doc:      "Sampling temperature. Requests that set their own value override it.",
		floatPtr: func(s *Settings) **float64 { return &s.params().Temperature },
	},
	{
		Key: "top_p", Target: TargetParameter, Path: "parameters.top_p",
		CLI: []string{"--top-p"}, Type: TypeFloat, Min: f64(0), Max: f64(1),
		Default: "request value, then model default", Group: GroupSampling,
		Doc:      "Nucleus sampling: keep the smallest set of tokens whose probability adds up to this value.",
		floatPtr: func(s *Settings) **float64 { return &s.params().TopP },
	},
	{
		Key: "top_k", Target: TargetParameter, Path: "parameters.top_k",
		CLI: []string{"--top-k"}, Type: TypeInt, Min: f64(0),
		Default: "request value, then model default", Group: GroupSampling,
		Doc:    "Keep only the K most likely tokens. 0 turns the filter off.",
		intPtr: func(s *Settings) **int { return &s.params().TopK },
	},
	{
		Key: "min_p", Target: TargetParameter, Path: "parameters.min_p",
		CLI: []string{"--min-p"}, Type: TypeFloat, Min: f64(0), Max: f64(1),
		Default: "request value, then model default", Group: GroupSampling,
		Doc:      "Drop tokens whose probability is below this fraction of the top token. 0 turns the filter off.",
		floatPtr: func(s *Settings) **float64 { return &s.params().MinP },
	},
	{
		Key: "override_tensor", Target: TargetOption, Path: "options", Option: "-ot",
		CLI: []string{"-ot", "--override-tensor"}, Type: TypeString,
		Default: "no override", Group: GroupPlacement, Repeatable: true,
		Doc: "Place the tensors that match a regular expression on a given device, as pattern=buffer. " +
			"Opt-in: nothing is placed this way unless you set it. Use the placement helper to keep MoE expert tensors on the CPU.",
		raw: true,
	},
}

// modelFlag and loadModeFlag are flags with a custom meaning. They are not in
// the table because they do not map to one config key.
var (
	modelCLI    = []string{"-m", "--model"}
	loadModeCLI = "--load-mode"
	loadModes   = []string{"none", "mmap", "mlock", "mmap_mlock", "direct_io", "auto"}
)

// ignoredFlags are flags LocalAI sets itself or that would stop the server.
// They are reported and never forwarded.
var ignoredFlags = map[string]string{
	"--host":         "LocalAI starts and addresses the backend process itself",
	"--port":         "LocalAI starts and addresses the backend process itself",
	"--api-key":      "LocalAI handles API keys itself",
	"--api-key-file": "LocalAI handles API keys itself",
	"-a":             "the model name is the name of the LocalAI model config",
	"--alias":        "the model name is the name of the LocalAI model config",
	"-hf":            "download the model with the gallery or the model import instead",
	"--hf-repo":      "download the model with the gallery or the model import instead",
	"-hfr":           "download the model with the gallery or the model import instead",
	"-mu":            "download the model with the gallery or the model import instead",
	"--model-url":    "download the model with the gallery or the model import instead",
	"-h":             "prints help and exits",
	"--help":         "prints help and exits",
	"--usage":        "prints help and exits",
	"--version":      "prints the version and exits",
	"--license":      "prints the license and exits",
	"--list-devices": "prints the devices and exits",
	"-cl":            "prints the cache and exits",
	"--cache-list":   "prints the cache and exits",
}

func flagIndex() map[string]*Flag {
	idx := map[string]*Flag{}
	for i := range Flags {
		for _, c := range Flags[i].CLI {
			idx[c] = &Flags[i]
		}
	}
	return idx
}

func negatedIndex() map[string]*Flag {
	idx := map[string]*Flag{}
	for i := range Flags {
		for _, c := range Flags[i].CLINegated {
			idx[c] = &Flags[i]
		}
	}
	return idx
}

// optionFlag finds the table entry for an option name or one of its aliases.
func optionFlag(name string) *Flag {
	for i := range Flags {
		f := &Flags[i]
		if f.Target != TargetOption || f.raw {
			continue
		}
		if f.Option == name {
			return f
		}
		for _, a := range f.optionAliases {
			if a == name {
				return f
			}
		}
	}
	return nil
}

// LoadModes lists the values accepted by --load-mode.
func LoadModes() []string { return append([]string(nil), loadModes...) }
