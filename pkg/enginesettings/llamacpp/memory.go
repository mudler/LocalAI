package llamacpp

import es "github.com/mudler/LocalAI/pkg/enginesettings"

// This file is the decision table of the quick setup: what each profile
// writes, per memory kind of the host. It is the one place these values
// live: the editor reads them from the schema. The table in
// docs/content/models/engine-settings.md describes it; keep them in step.
//
// The rules:
//
//   - Unified memory (one pool for the GPU and the host) allows more slots:
//     the KV cache and the weights already share the system memory, so a
//     slot costs no scarce VRAM. What it does cost is host memory, so the
//     prompt cache (cache_ram) and the context checkpoints of each slot are
//     bounded. The load mode stays on llama.cpp's auto, which skips mmap on
//     CUDA, ROCm and Vulkan integrated GPUs (a mapped model would sit in
//     memory twice while it is copied to the GPU buffers).
//   - A discrete GPU caps the slots by the VRAM estimate (MinMemory, and the
//     editor refuses a profile whose estimate does not fit), uses an 8-bit KV
//     cache at 32 streams, and keeps the prompt cache in host RAM at the
//     default size, since host RAM is not the scarce memory there.
//   - No GPU (system memory) is treated like unified memory for the caches,
//     but 32 streams are not offered: a CPU decodes too slowly to share
//     itself 32 ways.
//
// The slot counts follow the paged serving measurements of the llama.cpp
// backend: with paged serving, 16 slots took a hybrid MoE on a GB10 from
// 81 to 153 tok/s at 16 requests in flight (against 4 slots); GPU sampling
// pays off from 16 streams (+7% at 16, +10% at 32); MTP drafting helps one
// stream but costs about 5% at 16 streams, so the larger tiers turn
// drafting off.

const gib = uint64(1) << 30

// Host memory bound for unified memory and the CPU: half the backend's 8192
// MiB prompt cache. The context checkpoints of a hybrid model are not set
// here: LocalAI's hardware defaults bound them from the model's recurrent
// state, the slots and this cache_ram (n_ctx_checkpoints = the smaller of
// cache_ram and a quarter of the host memory, divided by slots times state,
// between 4 and 32), which a fixed count per slot would override.
const hostPromptCacheMiB = 4096

var hostBounds = map[string]any{"cache_ram": hostPromptCacheMiB}

// Floors of the stream setups. The slots share one KV cache, so the slot
// count does not change the memory a model needs: Qwen3-8B Q4_K_M at a
// 32768-token context through LocalAI on a GB10 used 9.6, 9.6 and 9.7 GiB
// of device memory and 3.2, 3.0 and 2.7 GB of host RSS at 8, 16 and 32
// slots. So every setup has the floor of its memory kind; the estimate
// check of the editor still refuses a model that does not fit.
const (
	minUnified  = 16 * gib
	minDiscrete = 8 * gib
	minSystem   = 16 * gib
)

// merge returns the union of maps; later maps win.
func merge(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// memory describes the kinds and the advice that is not a profile value.
var memory = &es.Memory{
	Fallback:    es.MemoryDiscrete,
	FitFraction: 0.9,
	Kinds: []es.MemoryKindInfo{
		{
			Kind: es.MemoryUnified, Label: "Unified memory",
			Doc: "The GPU shares the system memory (DGX Spark, Jetson, Apple silicon, AMD APUs). More slots fit; the prompt cache and checkpoints are kept small because they use the same memory.",
		},
		{
			Kind: es.MemoryDiscrete, Label: "Discrete GPU",
			Doc: "The GPU has its own memory. Slots are capped by the VRAM estimate, 32 streams use an 8-bit KV cache, and the prompt cache stays in system memory.",
		},
		{
			Kind: es.MemorySystem, Label: "No GPU",
			Doc: "The model runs on the CPU in system memory. The caches are kept small, and 32 streams are not offered.",
		},
	},
	Advice: []es.Advice{
		{
			ID: "unifiedLoadMode", Control: "load_mode", Kind: es.MemoryUnified,
			Value: "default", Avoid: []string{"mmap", "mmap_mlock"},
			Doc: "On unified memory, a memory-mapped model is held twice while it is copied to the GPU. Default lets llama.cpp skip mmap on integrated GPUs.",
		},
	},
}

// profiles are the quick setups of the editor. The default set is the batch
// size; the concurrency set is the slot count. One profile of each set is
// current at a time. nil unsets a key, so Balanced and Default are the
// defaults of LocalAI and llama.cpp for the host. Nothing applies them
// unless the person picks one: they change memory needs.
var profiles = []es.Preset{
	{
		ID: "balanced", Name: "Balanced", Profile: true,
		Description: "The default batch sizes: LocalAI picks them for the GPU.",
		Settings:    map[string]any{"batch": nil, "ubatch": nil},
	},
	{
		ID: "longPrompts", Name: "Long prompts", Profile: true,
		Description: "A batch of 2048: long prompts are read much faster, and the compute buffers grow.",
		Settings:    map[string]any{"batch": 2048},
		Memory: map[es.MemoryKind]es.PresetMemory{
			// Measured on a GB10: MoE prefill saturates around a micro-batch of
			// 2048 (core/config BlackwellPhysicalBatch).
			es.MemoryUnified: {Settings: map[string]any{"ubatch": 2048}},
			// The compute buffer grows with the micro-batch times the context
			// and is not split across GPUs, so a discrete card gets half.
			// TODO(paged serving): confirm 1024 on a discrete card (#13528
			// measured 2048 on the GB10 only).
			es.MemoryDiscrete: {
				Settings: map[string]any{"ubatch": 1024},
				Note:     "The micro-batch is 1024 on a discrete GPU: its compute buffer must fit one card.",
			},
			// A CPU gains little from a larger micro-batch.
			es.MemorySystem: {Settings: map[string]any{"ubatch": 512}},
		},
	},
	{
		ID: "concurrencyDefault", Name: "Default", Profile: true, Set: es.ProfileSetConcurrency,
		Description: "LocalAI picks the slots for the GPU and the backend its serving defaults.",
		BestFor:     "One person, or a few.",
		Settings:    map[string]any{"parallel": nil, "paged_kv": nil, "paged_preempt": nil},
	},
	{
		ID: "streams8", Name: "8 streams", Profile: true, Set: es.ProfileSetConcurrency, Streams: 8,
		Description: "8 slots with paged serving: requests share the KV cache and wait instead of failing when it is full.",
		BestFor:     "A team chatting with one model.",
		Settings:    map[string]any{"parallel": 8, "paged_kv": true, "paged_preempt": true},
		Memory: map[es.MemoryKind]es.PresetMemory{
			es.MemoryUnified:  {Settings: hostBounds, MinMemory: minUnified},
			es.MemoryDiscrete: {MinMemory: minDiscrete},
			es.MemorySystem:   {Settings: hostBounds, MinMemory: minSystem},
		},
	},
	{
		ID: "streams16", Name: "16 streams", Profile: true, Set: es.ProfileSetConcurrency, Streams: 16,
		Description: "16 slots with GPU sampling on and no drafting.",
		BestFor:     "Agents and tools that send requests in parallel.",
		Settings: map[string]any{
			"parallel": 16, "paged_kv": true, "paged_preempt": true,
			// GPU sampling for every request pays off from about 16 streams. auto
			// mixes GPU- and CPU-sampled requests in one batch, which raised the
			// inter-token latency p99 from 110 to 363 ms at 16 slots. Drafting
			// costs under load.
			"backend_sampling": "on", "spec_type": "none", "draft_model": nil,
		},
		Memory: map[es.MemoryKind]es.PresetMemory{
			es.MemoryUnified:  {Settings: hostBounds, MinMemory: minUnified},
			es.MemoryDiscrete: {MinMemory: minDiscrete},
			es.MemorySystem: {
				Settings: merge(hostBounds, map[string]any{"backend_sampling": nil}), MinMemory: minSystem,
				Note: "No GPU: tokens are sampled on the CPU.",
			},
		},
	},
	{
		ID: "streams32", Name: "32+ streams", Profile: true, Set: es.ProfileSetConcurrency, Streams: 32,
		Description: "32 slots with GPU sampling always on and no drafting: the most tokens per second in total, each stream slower.",
		BestFor:     "Batch jobs and many users at once.",
		Settings: map[string]any{
			"parallel": 32, "paged_kv": true, "paged_preempt": true,
			"backend_sampling": "on", "spec_type": "none", "draft_model": nil,
		},
		Memory: map[es.MemoryKind]es.PresetMemory{
			// f16 keeps the fastest attention path; q8_0 only when f16 does not
			// fit. A quantized V cache needs flash attention.
			es.MemoryUnified: {
				Settings:  hostBounds,
				Tight:     map[string]any{"cache_type_k": "q8_0", "cache_type_v": "q8_0", "flash_attention": "on"},
				MinMemory: minUnified,
			},
			es.MemoryDiscrete: {
				Settings:  map[string]any{"cache_type_k": "q8_0", "cache_type_v": "q8_0", "flash_attention": "on"},
				MinMemory: minDiscrete,
				Note:      "8-bit KV cache: about twice the tokens fit, at a small cost in quality and speed.",
			},
			es.MemorySystem: {Unsupported: true, Note: "Not offered without a GPU: a CPU shared by 32 streams makes each one too slow."},
		},
	},
}
