package llamacpp

import es "github.com/mudler/LocalAI/pkg/enginesettings"

// The editor sections of llama.cpp, in the order they are drawn.
const (
	GroupMemory    es.Group = "memory"
	GroupSpeed     es.Group = "speed"
	GroupPlacement es.Group = "placement"
	GroupSampling  es.Group = "sampling"
	GroupTemplate  es.Group = "template"
)

var cacheTypes = []string{"f32", "f16", "bf16", "q8_0", "q4_0", "q4_1", "iq4_nl", "q5_0", "q5_1"}

// flags is the table of supported llama-server flags. Its order is the order
// of a built command and of the known entries in "options".
//
// The aliases of the -ot entry are other names the backend reads for the
// same value; the editor reads them so an older file shows its placement.
var flags = []es.Flag{
	{
		Key: "context_size", Target: es.TargetConfig, Path: "context_size",
		CLI: []string{"--ctx-size", "-c"}, Type: es.TypeInt, Min: es.F64(0),
		Default: "sized from the model and the free memory", Group: GroupMemory,
		Doc: "Context window in tokens. 0 uses the size stored in the model file.",
	},
	{
		Key: "gpu_layers", Target: es.TargetConfig, Path: "gpu_layers",
		CLI: []string{"-ngl", "--gpu-layers", "--n-gpu-layers"}, Type: es.TypeInt, Min: es.F64(-1),
		Default: "all layers", Group: GroupMemory,
		Doc: "Number of layers to offload to the GPU. A large value such as 99 offloads every layer.",
	},
	{
		Key: "threads", Target: es.TargetConfig, Path: "threads",
		CLI: []string{"-t", "--threads"}, Type: es.TypeInt, Min: es.F64(-1),
		Default: "backend default", Group: GroupSpeed,
		Doc: "CPU threads used while generating tokens.",
	},
	{
		Key: "batch", Target: es.TargetConfig, Path: "parameters.batch",
		CLI: []string{"-b", "--batch-size"}, Type: es.TypeInt, Min: es.F64(1),
		Default: "backend default", Group: GroupSpeed,
		Doc: "Logical batch size: the most prompt tokens processed in one step.",
	},
	{
		Key: "flash_attention", Target: es.TargetConfig, Path: "flash_attention",
		CLI: []string{"-fa", "--flash-attn"}, Type: es.TypeEnum, Enum: []string{"on", "off", "auto"},
		Default: "auto", Group: GroupSpeed,
		Doc:           "Flash attention. A bare flag means on.",
		OptionalValue: true, BareValue: "on",
	},
	{
		Key: "cache_type_k", Target: es.TargetConfig, Path: "cache_type_k",
		CLI: []string{"-ctk", "--cache-type-k"}, Type: es.TypeEnum, Enum: cacheTypes,
		Default: "f16", Group: GroupMemory,
		Doc: "Data type of the K half of the KV cache. Smaller types save memory at some cost in quality.",
	},
	{
		Key: "cache_type_v", Target: es.TargetConfig, Path: "cache_type_v",
		CLI: []string{"-ctv", "--cache-type-v"}, Type: es.TypeEnum, Enum: cacheTypes,
		Default: "f16", Group: GroupMemory,
		Doc: "Data type of the V half of the KV cache. A quantized V cache needs flash attention.",
	},
	{
		Key: "mmap", Target: es.TargetConfig, Path: "mmap",
		CLI: []string{"--mmap"}, CLINegated: []string{"--no-mmap"}, Type: es.TypeBool,
		Default: "false", Group: GroupMemory,
		Doc: "Map the model file into memory instead of reading it. With mmap off and no mlock, the load mode is none.",
	},
	{
		Key: "mmlock", Target: es.TargetConfig, Path: "mmlock",
		CLI: []string{"--mlock"}, CLINegated: []string{"--no-mlock"}, Type: es.TypeBool,
		Default: "false", Group: GroupMemory,
		Doc: "Lock the model in RAM so the system cannot swap it out.",
	},
	{
		Key: "use_jinja", Target: es.TargetOption, Path: "options", Option: "use_jinja",
		CLI: []string{"--jinja"}, CLINegated: []string{"--no-jinja"}, Type: es.TypeBool,
		Default: "backend default", Group: GroupTemplate,
		Doc:           "Render the chat template with the Jinja engine of llama.cpp.",
		OptionAliases: []string{"jinja"},
	},
	{
		Key: "parallel", Target: es.TargetOption, Path: "options", Option: "parallel",
		CLI: []string{"-np", "--parallel"}, Type: es.TypeInt, Min: es.F64(-1),
		Default: "1", Group: GroupSpeed,
		Doc:           "Number of requests served at the same time (server slots). The context is shared between the slots.",
		OptionAliases: []string{"n_parallel"},
	},
	{
		Key: "ubatch", Target: es.TargetOption, Path: "options", Option: "ubatch",
		CLI: []string{"-ub", "--ubatch-size"}, Type: es.TypeInt, Min: es.F64(1),
		Default: "same as the batch size", Group: GroupSpeed,
		Doc:           "Physical batch size: the most tokens one compute step handles. Smaller values use less memory.",
		OptionAliases: []string{"n_ubatch"},
	},
	{
		Key: "threads_batch", Target: es.TargetOption, Path: "options", Option: "threads_batch",
		CLI: []string{"-tb", "--threads-batch"}, Type: es.TypeInt, Min: es.F64(-1),
		Default: "same as threads", Group: GroupSpeed,
		Doc:           "CPU threads used while processing the prompt.",
		OptionAliases: []string{"n_threads_batch"},
	},
	{
		Key: "fit", Target: es.TargetOption, Path: "options", Option: "fit",
		CLI: []string{"-fit", "--fit"}, Type: es.TypeEnum, Enum: []string{"on", "off"},
		Default: "on", Group: GroupMemory,
		Doc:           "Let llama.cpp lower the context size and the offload so the model fits in device memory.",
		OptionAliases: []string{"fit_params"},
		OptionalValue: true, BareValue: "on",
	},
	{
		Key: "no_op_offload", Target: es.TargetOption, Path: "options", Option: "no_op_offload",
		CLI: []string{"--no-op-offload"}, CLINegated: []string{"--op-offload"}, Type: es.TypeBool,
		Default: "false", Group: GroupSpeed,
		Doc:        "Do not offload host tensor operations to the GPU.",
		BareOption: true,
	},
	{
		Key: "direct_io", Target: es.TargetOption, Path: "options", Option: "direct_io",
		CLI: []string{"-dio", "--direct-io"}, CLINegated: []string{"--no-direct-io"}, Type: es.TypeBool,
		Default: "false", Group: GroupMemory,
		Doc:           "Read the model file with direct I/O, bypassing the page cache. Takes priority over mmap and mlock.",
		OptionAliases: []string{"use_direct_io"},
	},
	{
		Key: "temperature", Target: es.TargetConfig, Path: "parameters.temperature",
		CLI: []string{"--temp", "--temperature"}, Type: es.TypeFloat, Min: es.F64(0),
		Default: "request value, then model default", Group: GroupSampling,
		Doc: "Sampling temperature. Requests that set their own value override it.",
	},
	{
		Key: "top_p", Target: es.TargetConfig, Path: "parameters.top_p",
		CLI: []string{"--top-p"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1),
		Default: "request value, then model default", Group: GroupSampling,
		Doc: "Nucleus sampling: keep the smallest set of tokens whose probability adds up to this value.",
	},
	{
		Key: "top_k", Target: es.TargetConfig, Path: "parameters.top_k",
		CLI: []string{"--top-k"}, Type: es.TypeInt, Min: es.F64(0),
		Default: "request value, then model default", Group: GroupSampling,
		Doc: "Keep only the K most likely tokens. 0 turns the filter off.",
	},
	{
		Key: "min_p", Target: es.TargetConfig, Path: "parameters.min_p",
		CLI: []string{"--min-p"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1),
		Default: "request value, then model default", Group: GroupSampling,
		Doc: "Drop tokens whose probability is below this fraction of the top token. 0 turns the filter off.",
	},
	{
		Key: "override_tensor", Target: es.TargetOption, Path: "options", Option: "-ot",
		CLI: []string{"-ot", "--override-tensor"}, Type: es.TypeString,
		Default: "no override", Group: GroupPlacement, Repeatable: true,
		Doc: "Place the tensors that match a regular expression on a given device, as pattern=buffer. " +
			"Opt-in: nothing is placed this way unless you set it. Use the placement helper to keep MoE expert tensors on the CPU.",
		Raw: true, OptionAliases: []string{"--override-tensor", "override_tensor", "tensor_buft_overrides"},
	},
}
