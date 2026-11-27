package llamacpp

import es "github.com/mudler/LocalAI/pkg/enginesettings"

// The editor sections of llama.cpp, in the order they are drawn.
const (
	GroupMemory    es.Group = "memory"
	GroupSpeed     es.Group = "speed"
	GroupServing   es.Group = "serving"
	GroupPlacement es.Group = "placement"
	GroupSampling  es.Group = "sampling"
	GroupTemplate  es.Group = "template"
	// The rarely used and risky settings, drawn collapsed.
	GroupSpeculative es.Group = "speculative"
	GroupAdvanced    es.Group = "engine_advanced"
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
		Doc: "Logical batch size: the most prompt tokens processed in one step. Larger values read long prompts faster and use more compute memory.",
	},
	{
		Key: "flash_attention", Target: es.TargetConfig, Path: "flash_attention",
		CLI: []string{"-fa", "--flash-attn"}, Type: es.TypeEnum, Enum: []string{"on", "off", "auto"},
		Default: "auto", Group: GroupSpeed,
		Doc:           "Faster attention that needs less compute memory. A quantized V cache requires it. Auto turns it on when the device supports it.",
		OptionalValue: true, BareValue: "on",
	},
	{
		Key: "cache_type_k", Target: es.TargetConfig, Path: "cache_type_k",
		CLI: []string{"-ctk", "--cache-type-k"}, Type: es.TypeEnum, Enum: cacheTypes,
		Default: "f16", Group: GroupMemory,
		Doc: "Data type of the K half of the KV cache. q8_0 holds about twice the tokens of f16 in the same memory, at a small cost in quality. " +
			"A quantized cache can be slower on some attention paths until llama.cpp has a fast kernel for them.",
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
		Default: "LocalAI default for the GPU", Group: GroupServing,
		Doc:           "Number of requests served at the same time (server slots). The slots share the context, so long requests leave less room for the others.",
		OptionAliases: []string{"n_parallel"},
	},
	{
		Key: "ubatch", Target: es.TargetOption, Path: "options", Option: "ubatch",
		CLI: []string{"-ub", "--ubatch-size"}, Type: es.TypeInt, Min: es.F64(1),
		Default: "same as the batch size", Group: GroupSpeed,
		Doc:           "Physical batch size: the most tokens one compute step handles. Larger values prefill faster and use more compute memory; smaller values use less.",
		OptionAliases: []string{"n_ubatch"},
	},
	// GPU sampling is read by LocalAI's llama.cpp backend as a plain option.
	// A backend without it ignores the entry. The editor draws it as the
	// backendSampling combined control: Off (no entry), Auto, On.
	{
		Key: "backend_sampling", Target: es.TargetOption, Path: "options", Option: "backend_sampling",
		Type: es.TypeEnum, Enum: []string{"off", "auto", "on"}, Default: "off", Group: GroupSpeed, LocalAIOnly: true,
		OptionAliases: []string{"gpu_sampling"},
		Doc: "Sample tokens on the GPU. off: on the CPU. auto: on the GPU when the model runs on a GPU with several parallel requests " +
			"and the request does not need grammar or logprobs. on: always on the GPU, turned off for a request with a grammar, a reasoning budget or logprobs.",
	},
	// Paged serving is a patch of LocalAI's llama.cpp backend, not a
	// llama-server flag. A build without it ignores these options. Its
	// defaults change when the backend makes it the default, so the docs
	// say what each does in a build that has it.
	{
		Key: "paged_kv", Target: es.TargetOption, Path: "options", Option: "paged_kv",
		Type: es.TypeBool, Default: "on in builds with paged serving", Group: GroupServing, LocalAIOnly: true,
		Doc: "Paged serving: start a request only when the shared KV cache has room for it, and pause the newest one when it runs out, " +
			"so many users share the cache without failing. It needs more than one slot.",
		OptionAliases: []string{"paged_serving"},
	},
	{
		Key: "paged_preempt", Target: es.TargetOption, Path: "options", Option: "paged_preempt",
		Type: es.TypeBool, Default: "on", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: pause the request that is cheapest to recompute when the KV cache is full, and resume it later. " +
			"Keep it on: with it off, a full cache fails every running request.",
	},
	{
		Key: "prefill_budget", Target: es.TargetOption, Path: "options", Option: "prefill_budget",
		Type: es.TypeString, Default: "256 in builds with paged serving", Group: GroupServing, LocalAIOnly: true,
		Doc: "Prompt tokens read per step while other requests generate, so a long prompt does not stall them. " +
			"A number, 0 to turn it off, or auto to follow the load.",
		OptionAliases: []string{"max_prefill_tokens"},
	},
	{
		Key: "prefill_long", Target: es.TargetOption, Path: "options", Option: "prefill_long",
		Type: es.TypeInt, Min: es.F64(0), Default: "1024", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: the prefill budget applies only to prompts longer than this many tokens; shorter prompts are read whole. 0 applies it to every prompt.",
	},
	{
		Key: "kv_pool_tokens", Target: es.TargetOption, Path: "options", Option: "kv_pool_tokens",
		Type: es.TypeInt, Min: es.F64(0), Default: "the context size", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: the tokens of the shared KV cache all requests draw from. It sets the context size.",
	},
	{
		Key: "kv_output_reserve", Target: es.TargetOption, Path: "options", Option: "kv_output_reserve",
		Type: es.TypeInt, Min: es.F64(0), Default: "256", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: free tokens a request needs beyond its prompt before it starts, capped at its max tokens.",
	},
	{
		Key: "slot_attach", Target: es.TargetOption, Path: "options", Option: "slot_attach",
		Type: es.TypeBool, Default: "on", Group: GroupServing, LocalAIOnly: true,
		Doc:           "With paged serving: a request whose prompt starts like one another request holds starts from that request's KV cache instead of reading the prompt again.",
		OptionAliases: []string{"prefix_attach"},
	},
	{
		Key: "admission_order", Target: es.TargetOption, Path: "options", Option: "admission_order",
		Type: es.TypeBool, Default: "on", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: waiting requests go by priority class, then by the fewest prompt tokens not in a cache, then by arrival. " +
			"Off serves them first come, first served.",
		OptionAliases: []string{"paged_order"},
	},
	{
		Key: "prefill_cap", Target: es.TargetOption, Path: "options", Option: "prefill_cap",
		Type: es.TypeInt, Min: es.F64(0), Default: "512", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: the most prompt tokens one request reads per step while other requests read prompts too. 0 turns it off.",
	},
	{
		Key: "lazy_idle_save", Target: es.TargetOption, Path: "options", Option: "lazy_idle_save",
		Type: es.TypeBool, Default: "off", Group: GroupServing, LocalAIOnly: true,
		Doc: "With paged serving: idle slots keep their KV cache until the cache needs room, instead of saving it to the RAM prompt cache after each new request.",
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
	// TODO(paged serving): the paged serving work also adds a load-adaptive
	// prefill budget beyond prefill_budget:auto, prefix sharing between
	// requests, load-adaptive drafting, n-gram drafting by default with an
	// opt-out, and admission ordering with an opt-out. Add a row for each
	// once its option name is final in grpc-server.cpp.
	{
		Key: "cache_idle_slots", Target: es.TargetOption, Path: "options", Option: "cache_idle_slots",
		CLI: []string{"--cache-idle-slots"}, CLINegated: []string{"--no-cache-idle-slots"}, Type: es.TypeBool,
		Default: "on", Group: GroupServing, OptionAliases: []string{"idle_slots_cache"},
		Doc: "Save the KV cache of an idle slot to the prompt cache, so a later request with the same start loads it instead of reading it again.",
	},
	{
		Key: "cache_reuse", Target: es.TargetOption, Path: "options", Option: "n_cache_reuse",
		CLI: []string{"--cache-reuse"}, Type: es.TypeInt, Min: es.F64(0),
		Default: "0", Group: GroupServing, OptionAliases: []string{"cache_reuse"},
		Doc: "Smallest chunk of a cached prompt to reuse by shifting the KV cache. 0 turns it off.",
	},
	{
		Key: "slot_prompt_similarity", Target: es.TargetOption, Path: "options", Option: "slot_prompt_similarity",
		CLI: []string{"-sps", "--slot-prompt-similarity"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1),
		Default: "0.1", Group: GroupServing, OptionAliases: []string{"sps"},
		Doc: "How much a prompt must match the prompt of a slot to use that slot and its cache. 0 turns the match off.",
	},
	{
		Key: "kv_unified_per_slot", Target: es.TargetOption, Path: "options", Option: "kv_unified_per_slot",
		CLI: []string{"--kv-unified-per-slot"}, Type: es.TypeInt, Min: es.F64(0),
		Default: "no limit", Group: GroupServing,
		Doc: "Most tokens one slot may use of the shared KV cache. Without a context size, the cache is sized to the slots times this value.",
	},
	{
		Key: "cache_ram", Target: es.TargetOption, Path: "options", Option: "cache_ram",
		CLI: []string{"-cram", "--cache-ram"}, Type: es.TypeInt, Min: es.F64(-1),
		Default: "8192", Group: GroupMemory,
		Doc: "System memory in MiB for the prompt cache that keeps the KV cache of earlier prompts. 0 turns it off, -1 sets no limit.",
	},
	{
		Key: "mmproj_use_gpu", Target: es.TargetOption, Path: "options", Option: "mmproj_use_gpu",
		CLI: []string{"--mmproj-offload"}, CLINegated: []string{"--no-mmproj-offload"}, Type: es.TypeBool,
		Default: "true", Group: GroupMemory, OptionAliases: []string{"mmproj_offload"},
		Doc: "Run the vision projector on the GPU. Off keeps it on the CPU: less GPU memory, slower images.",
	},
	{
		Key: "backend_sampling_min_slots", Target: es.TargetOption, Path: "options", Option: "backend_sampling_min_slots",
		Type: es.TypeInt, Min: es.F64(1), Default: "16", Group: GroupAdvanced, LocalAIOnly: true,
		Doc: "With GPU sampling on Auto: the fewest parallel requests that turn GPU sampling on.",
	},
	{
		Key: "context_shift", Target: es.TargetOption, Path: "options", Option: "context_shift",
		CLI: []string{"--context-shift"}, CLINegated: []string{"--no-context-shift"}, Type: es.TypeBool,
		Default: "false", Group: GroupAdvanced,
		Doc: "When the context is full, drop the oldest tokens and keep generating. It turns paged serving off.",
	},
	{
		Key: "cont_batching", Target: es.TargetOption, Path: "options", Option: "cont_batching",
		CLI: []string{"-cb", "--cont-batching"}, CLINegated: []string{"-nocb", "--no-cont-batching"}, Type: es.TypeBool,
		Default: "true", Group: GroupAdvanced, OptionAliases: []string{"continuous_batching"},
		Doc: "Serve several requests in one batch as they come. Off serves them one after the other and turns paged serving off.",
	},
	{
		Key: "kv_unified", Target: es.TargetOption, Path: "options", Option: "kv_unified",
		CLI: []string{"-kvu", "--kv-unified"}, CLINegated: []string{"-no-kvu", "--no-kv-unified"}, Type: es.TypeBool,
		Default: "true", Group: GroupAdvanced, OptionAliases: []string{"unified_kv"},
		Doc: "One KV cache shared by all slots. Off splits it per slot and turns off the idle slot cache and paged serving.",
	},
	{
		Key: "swa_full", Target: es.TargetOption, Path: "options", Option: "swa_full",
		CLI: []string{"--swa-full"}, Type: es.TypeBool, Default: "false", Group: GroupAdvanced,
		Doc: "Keep the whole context in the sliding-window layers too. Uses much more memory; helps prompt cache reuse.",
	},
	{
		Key: "fit_target", Target: es.TargetOption, Path: "options", Option: "fit_params_target",
		CLI: []string{"-fitt", "--fit-target"}, Type: es.TypeString, Default: "1024", Group: GroupAdvanced,
		OptionAliases: []string{"fit_target"},
		Doc:           "Memory in MiB that fitting leaves free on each device, one value or one per device separated by slashes.",
	},
	{
		Key: "fit_ctx", Target: es.TargetOption, Path: "options", Option: "fit_params_min_ctx",
		CLI: []string{"-fitc", "--fit-ctx"}, Type: es.TypeInt, Min: es.F64(0), Default: "4096", Group: GroupAdvanced,
		OptionAliases: []string{"fit_ctx"},
		Doc:           "Smallest context size that fitting may lower the context to.",
	},
	{
		Key: "n_cpu_ffn", Target: es.TargetOption, Path: "options", Option: "n_cpu_ffn",
		CLI: []string{"-ncffn", "--n-cpu-ffn"}, Type: es.TypeInt, Min: es.F64(0), Default: "0", Group: GroupAdvanced,
		Doc: "Keep the feed-forward weights of the first N layers of a dense model on the CPU.",
	},
	{
		Key: "moe_cache_cpu_share", Target: es.TargetOption, Path: "options", Option: "moe_cache_cpu_share",
		CLI: []string{"--moe-cache-cpu-share"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1), Default: "0", Group: GroupAdvanced,
		Doc: "With the MoE expert cache: share of cache misses the CPU computes instead of copying the experts to the GPU.",
	},
	{
		Key: "lazy_mode", Target: es.TargetOption, Path: "options", Option: "lazy_mode",
		CLI: []string{"-lzm", "--lazy-mode"}, Type: es.TypeEnum, Enum: []string{"auto", "on", "off"}, Default: "auto", Group: GroupAdvanced,
		OptionAliases: []string{"lazy-mode", "lzm"},
		Doc:           "Read large tensors such as per-layer embeddings when they are needed instead of at load. on needs mmap.",
	},
	{
		Key: "split_mode", Target: es.TargetOption, Path: "options", Option: "split_mode",
		CLI: []string{"-sm", "--split-mode"}, Type: es.TypeEnum, Enum: []string{"none", "layer", "row", "tensor"}, Default: "layer", Group: GroupAdvanced,
		OptionAliases: []string{"sm"},
		Doc:           "How to split the model over several GPUs: none, by layer, by row, or by tensor (experimental).",
	},
	{
		Key: "device", Target: es.TargetOption, Path: "options", Option: "device",
		CLI: []string{"-dev", "--device"}, Type: es.TypeString, Default: "all devices", Group: GroupAdvanced,
		OptionAliases: []string{"devices"},
		Doc:           "Devices to use, comma-separated, as llama-server --list-devices names them.",
	},
	{
		Key: "rpc_servers", Target: es.TargetOption, Path: "options", Option: "grpc_servers",
		CLI: []string{"--rpc"}, Type: es.TypeString, Default: "none", Group: GroupAdvanced,
		OptionAliases: []string{"rpc_servers"},
		Doc:           "Comma-separated llama.cpp RPC workers to spread the model over.",
	},
	{
		Key: "check_tensors", Target: es.TargetOption, Path: "options", Option: "check_tensors",
		CLI: []string{"--check-tensors"}, Type: es.TypeBool, Default: "false", Group: GroupAdvanced,
		Doc: "Check the model weights for invalid values while loading. Slower loads.",
	},
	{
		Key: "warmup", Target: es.TargetOption, Path: "options", Option: "warmup",
		CLI: []string{"--warmup"}, CLINegated: []string{"--no-warmup"}, Type: es.TypeBool, Default: "true", Group: GroupAdvanced,
		Doc: "Run an empty pass after loading, so the first request is not slower.",
	},
	{
		Key: "verbosity", Target: es.TargetOption, Path: "options", Option: "verbosity",
		CLI: []string{"-lv", "--verbosity", "--log-verbosity"}, Type: es.TypeInt, Default: "3", Group: GroupAdvanced,
		Doc: "How much llama.cpp logs. Higher logs more.",
	},
	{
		Key: "ctx_checkpoints", Target: es.TargetOption, Path: "options", Option: "n_ctx_checkpoints",
		CLI: []string{"-ctxcp", "--ctx-checkpoints", "--swa-checkpoints"}, Type: es.TypeInt, Min: es.F64(0), Default: "32", Group: GroupAdvanced,
		OptionAliases: []string{"ctx_checkpoints"},
		Doc:           "Most context checkpoints a slot keeps, to reuse part of a prompt on models that cannot rewind (sliding window, recurrent).",
	},
	{
		Key: "checkpoint_min_step", Target: es.TargetOption, Path: "options", Option: "checkpoint_min_step",
		CLI: []string{"-cms", "--checkpoint-min-step"}, Type: es.TypeInt, Min: es.F64(0), Default: "256", Group: GroupAdvanced,
		OptionAliases: []string{"checkpoint_min_spacing", "checkpoint_every_nt", "checkpoint_every_n_tokens"},
		Doc:           "Fewest tokens between two context checkpoints. 0 removes the limit.",
	},
	{
		Key: "n_rs_seq", Target: es.TargetOption, Path: "options", Option: "n_rs_seq",
		Type: es.TypeInt, Min: es.F64(0), Default: "0", Group: GroupAdvanced, LocalAIOnly: true,
		OptionAliases: []string{"rs_seq"},
		Doc:           "Recurrent state snapshots per request, so hybrid models can reuse a prompt cache that needs a rewind. Each costs the recurrent state again.",
	},
	{
		Key: "slot_save_path", Target: es.TargetOption, Path: "options", Option: "slot_save_path",
		CLI: []string{"--slot-save-path"}, Type: es.TypeString, Default: "off", Group: GroupAdvanced,
		Doc: "Folder where the slot save and restore API keeps prompt cache files. Save and restore are off without it.",
	},
	{
		Key: "yarn_orig_ctx", Target: es.TargetOption, Path: "options", Option: "yarn_orig_ctx",
		CLI: []string{"--yarn-orig-ctx"}, Type: es.TypeInt, Min: es.F64(0), Default: "the model value", Group: GroupAdvanced,
		Doc: "Context size the model was trained with, for YaRN scaling. 0 reads it from the model.",
	},
	{
		Key: "pooling", Target: es.TargetOption, Path: "options", Option: "pooling_type",
		CLI: []string{"--pooling"}, Type: es.TypeEnum, Enum: []string{"none", "mean", "cls", "last", "rank"}, Default: "from the model", Group: GroupAdvanced,
		OptionAliases: []string{"pooling"},
		Doc:           "How an embedding model pools the tokens into one vector. Rerank models use rank.",
	},
	{
		Key: "embd_normalize", Target: es.TargetOption, Path: "options", Option: "embd_normalize",
		CLI: []string{"--embd-normalize"}, Type: es.TypeInt, Min: es.F64(-1), Default: "2", Group: GroupAdvanced,
		OptionAliases: []string{"embedding_normalize"},
		Doc:           "Embedding normalization: -1 none, 0 max absolute, 1 taxicab, 2 Euclidean, above 2 p-norm.",
	},
	{
		Key: "image_min_tokens", Target: es.TargetOption, Path: "options", Option: "image_min_tokens",
		CLI: []string{"--image-min-tokens"}, Type: es.TypeInt, Min: es.F64(-1), Default: "-1", Group: GroupAdvanced,
		Doc: "Fewest vision tokens per image. -1 keeps the model value.",
	},
	{
		Key: "image_max_tokens", Target: es.TargetOption, Path: "options", Option: "image_max_tokens",
		CLI: []string{"--image-max-tokens"}, Type: es.TypeInt, Min: es.F64(-1), Default: "-1", Group: GroupAdvanced,
		Doc: "Most vision tokens per image. -1 keeps the model value.",
	},
	{
		Key: "reasoning_format", Target: es.TargetOption, Path: "options", Option: "reasoning_format",
		CLI: []string{"--reasoning-format"}, Type: es.TypeEnum, Enum: []string{"none", "auto", "deepseek", "deepseek-legacy"},
		Default: "deepseek", Group: GroupTemplate,
		Doc: "How the thinking of a reasoning model is split from the answer.",
	},
	{
		Key: "enable_reasoning", Target: es.TargetOption, Path: "options", Option: "enable_reasoning",
		CLI: []string{"-rea", "--reasoning"}, Type: es.TypeEnum, Enum: []string{"auto", "on", "off"}, Default: "auto", Group: GroupTemplate,
		Doc: "Thinking in the chat template: auto follows the template.",
	},
	{
		Key: "reasoning_budget", Target: es.TargetOption, Path: "options", Option: "reasoning_budget",
		CLI: []string{"--reasoning-budget"}, Type: es.TypeInt, Min: es.F64(-1), Default: "-1", Group: GroupTemplate,
		Doc: "Most thinking tokens before the answer starts. -1 sets no limit, 0 skips thinking.",
	},
	{
		Key: "prefill_assistant", Target: es.TargetOption, Path: "options", Option: "prefill_assistant",
		CLI: []string{"--prefill-assistant"}, CLINegated: []string{"--no-prefill-assistant"}, Type: es.TypeBool,
		Default: "true", Group: GroupTemplate,
		Doc: "Continue a last assistant message instead of starting a new one.",
	},
	// Speculative decoding: a draft model or n-grams propose tokens that the
	// model checks in one step.
	{
		Key: "draft_model", Target: es.TargetConfig, Path: "draft_model",
		CLI: []string{"-md", "--model-draft", "--spec-draft-model"}, Type: es.TypeString, Default: "none", Group: GroupSpeculative,
		Doc: "File of the draft model in the models directory: a small model with the same vocabulary that proposes tokens.",
	},
	{
		Key: "spec_type", Target: es.TargetOption, Path: "options", Option: "spec_type",
		CLI: []string{"--spec-type"}, Type: es.TypeString, Default: "none, or draft-simple with a draft model", Group: GroupSpeculative,
		OptionAliases: []string{"speculative_type"},
		Doc:           "Kind of speculative decoding, or several comma-separated: draft-simple, draft-mtp, draft-eagle3, ngram-simple, ngram-mod and others.",
	},
	{
		Key: "spec_max_streams", Target: es.TargetOption, Path: "options", Option: "spec_max_streams",
		Type: es.TypeInt, Min: es.F64(0), Default: "4", Group: GroupSpeculative, LocalAIOnly: true,
		Doc: "Drafting pauses while more than this many requests generate, and resumes when the count drops by a quarter. 0 never pauses.",
	},
	{
		Key: "spec_n_max", Target: es.TargetOption, Path: "options", Option: "spec_n_max",
		CLI: []string{"--spec-draft-n-max", "--draft", "--draft-n", "--draft-max"}, Type: es.TypeInt, Min: es.F64(0), Default: "3", Group: GroupSpeculative,
		OptionAliases: []string{"draft_max"},
		Doc:           "Most tokens drafted per step.",
	},
	{
		Key: "spec_n_min", Target: es.TargetOption, Path: "options", Option: "spec_n_min",
		CLI: []string{"--spec-draft-n-min", "--draft-min", "--draft-n-min"}, Type: es.TypeInt, Min: es.F64(0), Default: "0", Group: GroupSpeculative,
		OptionAliases: []string{"draft_min"},
		Doc:           "Fewest drafted tokens worth checking.",
	},
	{
		Key: "spec_p_min", Target: es.TargetOption, Path: "options", Option: "spec_p_min",
		CLI: []string{"--spec-draft-p-min", "--draft-p-min"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1), Default: "0", Group: GroupSpeculative,
		OptionAliases: []string{"draft_p_min"},
		Doc:           "Lowest draft probability to keep drafting.",
	},
	{
		Key: "spec_p_split", Target: es.TargetOption, Path: "options", Option: "spec_p_split",
		CLI: []string{"--spec-draft-p-split", "--draft-p-split"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1), Default: "0.1", Group: GroupSpeculative,
		Doc: "Probability at which a draft branches into a tree.",
	},
	{
		Key: "draft_gpu_layers", Target: es.TargetOption, Path: "options", Option: "draft_gpu_layers",
		CLI: []string{"--spec-draft-ngl", "-ngld", "--gpu-layers-draft", "--n-gpu-layers-draft"}, Type: es.TypeInt, Min: es.F64(-1), Default: "-1", Group: GroupSpeculative,
		Doc: "Layers of the draft model on the GPU. -1 keeps the default.",
	},
	{
		Key: "draft_threads", Target: es.TargetOption, Path: "options", Option: "draft_threads",
		CLI: []string{"--spec-draft-threads", "-td", "--threads-draft"}, Type: es.TypeInt, Default: "same as threads", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_threads"},
		Doc:           "CPU threads of the draft model. 0 or less uses every core.",
	},
	{
		Key: "draft_threads_batch", Target: es.TargetOption, Path: "options", Option: "draft_threads_batch",
		CLI: []string{"--spec-draft-threads-batch", "-tbd", "--threads-batch-draft"}, Type: es.TypeInt, Default: "same as draft threads", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_threads_batch"},
		Doc:           "CPU threads of the draft model while reading the prompt.",
	},
	{
		Key: "draft_cache_type_k", Target: es.TargetOption, Path: "options", Option: "draft_cache_type_k",
		CLI: []string{"--spec-draft-type-k", "-ctkd", "--cache-type-k-draft"}, Type: es.TypeEnum, Enum: cacheTypes, Default: "f16", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_cache_type_k", "spec_draft_type_k"},
		Doc:           "Data type of the K cache of the draft model.",
	},
	{
		Key: "draft_cache_type_v", Target: es.TargetOption, Path: "options", Option: "draft_cache_type_v",
		CLI: []string{"--spec-draft-type-v", "-ctvd", "--cache-type-v-draft"}, Type: es.TypeEnum, Enum: cacheTypes, Default: "f16", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_cache_type_v", "spec_draft_type_v"},
		Doc:           "Data type of the V cache of the draft model.",
	},
	{
		Key: "spec_draft_n_ubatch", Target: es.TargetOption, Path: "options", Option: "spec_draft_n_ubatch",
		CLI: []string{"--spec-draft-n-ubatch"}, Type: es.TypeInt, Min: es.F64(0), Default: "0", Group: GroupSpeculative,
		Doc: "Physical batch of the draft model. Smaller saves GPU memory; 0 uses the micro-batch size.",
	},
	{
		Key: "spec_draft_sampling", Target: es.TargetOption, Path: "options", Option: "spec_draft_sampling",
		CLI: []string{"--spec-draft-sampling"}, Type: es.TypeEnum, Enum: []string{"greedy", "probabilistic"}, Default: "greedy", Group: GroupSpeculative,
		Doc: "How the draft is sampled: greedy takes the most likely token, probabilistic samples and the model checks by rejection.",
	},
	{
		Key: "spec_draft_device", Target: es.TargetOption, Path: "options", Option: "spec_draft_device",
		CLI: []string{"--spec-draft-device", "-devd", "--device-draft"}, Type: es.TypeString, Default: "same as device", Group: GroupSpeculative,
		OptionAliases: []string{"draft_device"},
		Doc:           "Devices of the draft model, comma-separated. none keeps it off the GPU.",
	},
	{
		Key: "draft_cpu_moe", Target: es.TargetOption, Path: "options", Option: "draft_cpu_moe",
		CLI: []string{"--spec-draft-cpu-moe", "-cmoed", "--cpu-moe-draft"}, Type: es.TypeBool, Default: "false", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_cpu_moe"},
		Doc:           "Keep all MoE expert weights of the draft model on the CPU.",
	},
	{
		Key: "draft_n_cpu_moe", Target: es.TargetOption, Path: "options", Option: "draft_n_cpu_moe",
		CLI: []string{"--spec-draft-n-cpu-moe", "--spec-draft-ncmoe", "-ncmoed", "--n-cpu-moe-draft"}, Type: es.TypeInt, Min: es.F64(0), Default: "0", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_n_cpu_moe"},
		Doc:           "Keep the MoE expert weights of the first N draft model layers on the CPU.",
	},
	{
		Key: "draft_override_tensor", Target: es.TargetOption, Path: "options", Option: "draft_override_tensor",
		CLI: []string{"--spec-draft-override-tensor", "-otd", "--override-tensor-draft"}, Type: es.TypeString, Default: "no override", Group: GroupSpeculative,
		OptionAliases: []string{"spec_draft_override_tensor"},
		Doc:           "Place draft model tensors that match a pattern on a device, as pattern=buffer, comma-separated.",
	},
	{
		Key: "spec_ngram_size_n", Target: es.TargetOption, Path: "options", Option: "spec_ngram_size_n",
		CLI: []string{"--spec-ngram-size-n"}, Type: es.TypeInt, Min: es.F64(0), Default: "12", Group: GroupSpeculative,
		OptionAliases: []string{"ngram_size_n"},
		Doc:           "N-gram drafting: tokens looked up in the history.",
	},
	{
		Key: "spec_ngram_size_m", Target: es.TargetOption, Path: "options", Option: "spec_ngram_size_m",
		CLI: []string{"--spec-ngram-size-m"}, Type: es.TypeInt, Min: es.F64(0), Default: "48", Group: GroupSpeculative,
		OptionAliases: []string{"ngram_size_m"},
		Doc:           "N-gram drafting: tokens proposed after a match.",
	},
	{
		Key: "spec_ngram_min_hits", Target: es.TargetOption, Path: "options", Option: "spec_ngram_min_hits",
		CLI: []string{"--spec-ngram-min-hits"}, Type: es.TypeInt, Min: es.F64(0), Default: "1", Group: GroupSpeculative,
		OptionAliases: []string{"ngram_min_hits"},
		Doc:           "N-gram drafting: fewest matches before a proposal is used.",
	},
	{
		Key: "spec_ngram_mod_n_min", Target: es.TargetOption, Path: "options", Option: "spec_ngram_mod_n_min",
		CLI: []string{"--spec-ngram-mod-n-min"}, Type: es.TypeInt, Min: es.F64(0), Default: "48", Group: GroupSpeculative,
		Doc: "ngram-mod drafting: fewest n-gram tokens to use.",
	},
	{
		Key: "spec_ngram_mod_n_max", Target: es.TargetOption, Path: "options", Option: "spec_ngram_mod_n_max",
		CLI: []string{"--spec-ngram-mod-n-max"}, Type: es.TypeInt, Min: es.F64(0), Default: "64", Group: GroupSpeculative,
		Doc: "ngram-mod drafting: most n-gram tokens to use.",
	},
	{
		Key: "spec_ngram_mod_n_match", Target: es.TargetOption, Path: "options", Option: "spec_ngram_mod_n_match",
		CLI: []string{"--spec-ngram-mod-n-match"}, Type: es.TypeInt, Min: es.F64(0), Default: "24", Group: GroupSpeculative,
		Doc: "ngram-mod drafting: lookup length.",
	},
	{
		Key: "spec_ngram_map_k_size_n", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k_size_n",
		CLI: []string{"--spec-ngram-map-k-size-n"}, Type: es.TypeInt, Min: es.F64(0), Default: "12", Group: GroupSpeculative,
		Doc: "ngram-map-k drafting: tokens looked up.",
	},
	{
		Key: "spec_ngram_map_k_size_m", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k_size_m",
		CLI: []string{"--spec-ngram-map-k-size-m"}, Type: es.TypeInt, Min: es.F64(0), Default: "48", Group: GroupSpeculative,
		Doc: "ngram-map-k drafting: tokens proposed.",
	},
	{
		Key: "spec_ngram_map_k_min_hits", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k_min_hits",
		CLI: []string{"--spec-ngram-map-k-min-hits"}, Type: es.TypeInt, Min: es.F64(0), Default: "1", Group: GroupSpeculative,
		Doc: "ngram-map-k drafting: fewest matches before a proposal is used.",
	},
	{
		Key: "spec_ngram_map_k4v_size_n", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k4v_size_n",
		CLI: []string{"--spec-ngram-map-k4v-size-n"}, Type: es.TypeInt, Min: es.F64(0), Default: "12", Group: GroupSpeculative,
		Doc: "ngram-map-k4v drafting: tokens looked up.",
	},
	{
		Key: "spec_ngram_map_k4v_size_m", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k4v_size_m",
		CLI: []string{"--spec-ngram-map-k4v-size-m"}, Type: es.TypeInt, Min: es.F64(0), Default: "48", Group: GroupSpeculative,
		Doc: "ngram-map-k4v drafting: tokens proposed.",
	},
	{
		Key: "spec_ngram_map_k4v_min_hits", Target: es.TargetOption, Path: "options", Option: "spec_ngram_map_k4v_min_hits",
		CLI: []string{"--spec-ngram-map-k4v-min-hits"}, Type: es.TypeInt, Min: es.F64(0), Default: "1", Group: GroupSpeculative,
		Doc: "ngram-map-k4v drafting: fewest matches before a proposal is used.",
	},
	{
		Key: "lookup_cache_static", Target: es.TargetOption, Path: "options", Option: "spec_lookup_cache_static",
		CLI: []string{"-lcs", "--lookup-cache-static"}, Type: es.TypeString, Default: "none", Group: GroupSpeculative,
		OptionAliases: []string{"lookup_cache_static"},
		Doc:           "File of a fixed n-gram cache for ngram-cache drafting.",
	},
	{
		Key: "lookup_cache_dynamic", Target: es.TargetOption, Path: "options", Option: "spec_lookup_cache_dynamic",
		CLI: []string{"-lcd", "--lookup-cache-dynamic"}, Type: es.TypeString, Default: "none", Group: GroupSpeculative,
		OptionAliases: []string{"lookup_cache_dynamic"},
		Doc:           "File of an n-gram cache that generation updates, for ngram-cache drafting.",
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
	// grammar_engine is read by LocalAI's llama.cpp backend. A build without
	// llguidance uses GBNF whatever the value.
	{
		Key: "grammar_engine", Target: es.TargetOption, Path: "options", Option: "grammar_engine",
		Type: es.TypeEnum, Enum: []string{"auto", "gbnf", "llguidance"}, Default: "auto", Group: GroupSampling, LocalAIOnly: true,
		Doc: "Engine of the grammars LocalAI builds from a JSON schema (response_format and tool calls). " +
			"auto: llguidance when the backend build has it, GBNF otherwise. gbnf: always llama.cpp's GBNF sampler. " +
			"A grammar you write yourself always uses GBNF.",
	},
	{
		Key: "grammar_max_repetition", Target: es.TargetOption, Path: "options", Option: "grammar_max_repetition",
		Type: es.TypeInt, Min: es.F64(1), Default: "100000", Group: GroupSampling, LocalAIOnly: true,
		Doc: "Limit of the GBNF repetition check (llama.cpp's is 2000). The grammar of a large tool set goes over 2000 " +
			"and the request fails with \"exceeds sane defaults\"; a lower value bounds the work a grammar can ask for.",
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
