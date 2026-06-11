package meta

// DefaultRegistry returns enrichment overrides for the ~30 most commonly used
// config fields. Fields not listed here still appear with auto-generated
// labels and type-inferred components.
func DefaultRegistry() map[string]FieldMetaOverride {
	f64 := func(v float64) *float64 { return &v }

	return map[string]FieldMetaOverride{
		// --- General ---
		"name": {
			Section:     "general",
			Label:       "Model Name",
			Description: "Unique identifier for this model configuration",
			Component:   "input",
			Order:       0,
		},
		"backend": {
			Section:              "general",
			Label:                "Backend",
			Description:          "The inference backend to use (e.g. llama-cpp, vllm, diffusers)",
			Component:            "select",
			AutocompleteProvider: ProviderBackends,
			Order:                1,
		},
		"description": {
			Section:     "general",
			Label:       "Description",
			Description: "Human-readable description of what this model does",
			Component:   "textarea",
			Order:       2,
		},
		"usage": {
			Section:     "general",
			Label:       "Usage",
			Description: "Usage instructions or notes",
			Component:   "textarea",
			Advanced:    true,
			Order:       3,
		},
		"cuda": {
			Section:     "general",
			Label:       "CUDA",
			Description: "Explicitly enable CUDA acceleration",
			Order:       5,
		},
		"known_usecases": {
			Section:     "general",
			Label:       "Known Use Cases",
			Description: "Capabilities this model supports",
			Component:   "string-list",
			Options:     UsecaseOptions,
			Order:       6,
		},

		// --- LLM ---
		"context_size": {
			Section:     "llm",
			Label:       "Context Size",
			Description: "Maximum context window in tokens",
			Component:   "number",
			VRAMImpact:  true,
			Order:       10,
		},
		"gpu_layers": {
			Section:     "llm",
			Label:       "GPU Layers",
			Description: "Number of layers to offload to GPU (-1 = all)",
			Component:   "number",
			Min:         f64(-1),
			VRAMImpact:  true,
			Order:       11,
		},
		"threads": {
			Section:     "llm",
			Label:       "Threads",
			Description: "Number of CPU threads for inference",
			Component:   "number",
			Min:         f64(1),
			Order:       12,
		},
		"f16": {
			Section:     "llm",
			Label:       "F16",
			Description: "Use 16-bit floating point for key/value cache",
			Order:       13,
		},
		"mmap": {
			Section:     "llm",
			Label:       "Memory Map",
			Description: "Use memory-mapped files for model loading",
			Order:       14,
		},
		"mmlock": {
			Section:     "llm",
			Label:       "Memory Lock",
			Description: "Lock model memory to prevent swapping",
			Advanced:    true,
			Order:       15,
		},
		"low_vram": {
			Section:     "llm",
			Label:       "Low VRAM",
			Description: "Optimize for systems with limited GPU memory",
			VRAMImpact:  true,
			Order:       16,
		},
		"embeddings": {
			Section:     "llm",
			Label:       "Embeddings",
			Description: "Enable embedding generation mode",
			Order:       17,
		},
		"quantization": {
			Section:     "llm",
			Label:       "Quantization",
			Description: "Quantization method (e.g. q4_0, q5_1, q8_0)",
			Component:   "select",
			Options:     QuantizationOptions,
			Advanced:    true,
			Order:       20,
		},
		"flash_attention": {
			Section:     "llm",
			Label:       "Flash Attention",
			Description: "Enable flash attention for faster inference",
			Component:   "input",
			Advanced:    true,
			Order:       21,
		},
		"reasoning_effort": {
			Section:     "llm",
			Label:       "Reasoning Effort",
			Description: "Default reasoning effort, forwarded to the backend as the reasoning_effort chat_template_kwarg (jinja models like gpt-oss / LFM2.5 honor it). A per-request reasoning_effort overrides it. 'none' also turns thinking off.",
			Component:   "select",
			Options: []FieldOption{
				{Value: "", Label: "Unset (model default)"},
				{Value: "none", Label: "none (disable thinking)"},
				{Value: "minimal", Label: "minimal"},
				{Value: "low", Label: "low"},
				{Value: "medium", Label: "medium"},
				{Value: "high", Label: "high"},
			},
			Advanced: true,
			Order:    22,
		},
		"cache_type_k": {
			Section:     "llm",
			Label:       "KV Cache Type (K)",
			Description: "Quantization type for key cache (e.g. f16, q8_0, q4_0)",
			Component:   "select",
			Options:     CacheTypeOptions,
			VRAMImpact:  true,
			Advanced:    true,
			Order:       22,
		},
		"cache_type_v": {
			Section:     "llm",
			Label:       "KV Cache Type (V)",
			Description: "Quantization type for value cache",
			Component:   "select",
			Options:     CacheTypeOptions,
			VRAMImpact:  true,
			Advanced:    true,
			Order:       23,
		},

		// --- Parameters ---
		"parameters.temperature": {
			Section:     "parameters",
			Label:       "Temperature",
			Description: "Sampling temperature (higher = more creative, lower = more deterministic)",
			Component:   "slider",
			Min:         f64(0),
			Max:         f64(2),
			Step:        f64(0.05),
			Order:       30,
		},
		"parameters.top_p": {
			Section:     "parameters",
			Label:       "Top P",
			Description: "Nucleus sampling threshold",
			Component:   "slider",
			Min:         f64(0),
			Max:         f64(1),
			Step:        f64(0.01),
			Order:       31,
		},
		"parameters.top_k": {
			Section:     "parameters",
			Label:       "Top K",
			Description: "Top-K sampling: consider only the K most likely tokens",
			Component:   "number",
			Min:         f64(0),
			Order:       32,
		},
		"parameters.max_tokens": {
			Section:     "parameters",
			Label:       "Max Tokens",
			Description: "Maximum number of tokens to generate (0 = unlimited)",
			Component:   "number",
			Min:         f64(0),
			Order:       33,
		},
		"parameters.repeat_penalty": {
			Section:     "parameters",
			Label:       "Repeat Penalty",
			Description: "Penalize repeated tokens (1.0 = no penalty)",
			Component:   "number",
			Min:         f64(0),
			Advanced:    true,
			Order:       34,
		},
		"parameters.seed": {
			Section:     "parameters",
			Label:       "Seed",
			Description: "Random seed (-1 = random)",
			Component:   "number",
			Advanced:    true,
			Order:       35,
		},

		// --- Templates ---
		"template.chat": {
			Section:     "templates",
			Label:       "Chat Template",
			Description: "Go template for chat completion requests",
			Component:   "code-editor",
			Order:       40,
		},
		"template.chat_message": {
			Section:     "templates",
			Label:       "Chat Message Template",
			Description: "Go template for individual chat messages",
			Component:   "code-editor",
			Order:       41,
		},
		"template.completion": {
			Section:     "templates",
			Label:       "Completion Template",
			Description: "Go template for completion requests",
			Component:   "code-editor",
			Order:       42,
		},
		"template.use_tokenizer_template": {
			Section:     "templates",
			Label:       "Use Tokenizer Template",
			Description: "Use the chat template from the model's tokenizer config",
			Order:       43,
		},
		// Router section template — kept in the templates UI section
		// (rather than the router section under "other") so operators
		// editing prompt shapes find all template-typed fields in one
		// place, mirroring how chat / chat_message are grouped.
		"router.classifier_system_template": {
			Section:     "templates",
			Label:       "Router Classifier System Prompt",
			Description: "Go text/template (with sprig functions) for the routing system prompt the score classifier feeds to its classifier_model. Executed with `.Policies` ([]{Label, Description}). Empty falls back to the built-in Arch-Router-shaped prompt (route-listing block + JSON output schema). Override when the classifier model was trained on a different schema or you need the routing instructions in a different language. The candidate format scored against the model is fixed at `{\"route\": \"<label>\"}` — keep your override's output schema instruction matching that.",
			Component:   "code-editor",
			Order:       44,
		},

		// --- Pipeline ---
		"pipeline.llm": {
			Section:              "pipeline",
			Label:                "LLM Model",
			Description:          "Model to use for LLM inference in the pipeline",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsChat,
			Order:                60,
		},
		"pipeline.tts": {
			Section:              "pipeline",
			Label:                "TTS Model",
			Description:          "Model to use for text-to-speech in the pipeline",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsTTS,
			Order:                61,
		},
		"pipeline.transcription": {
			Section:              "pipeline",
			Label:                "Transcription Model",
			Description:          "Model to use for speech-to-text in the pipeline",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsTranscript,
			Order:                62,
		},
		"pipeline.vad": {
			Section:              "pipeline",
			Label:                "VAD Model",
			Description:          "Model to use for voice activity detection in the pipeline",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsVAD,
			Order:                63,
		},
		"pipeline.reasoning_effort": {
			Section:     "pipeline",
			Label:       "Reasoning Effort",
			Description: "Reasoning effort for the pipeline's LLM, forwarded to the backend as the reasoning_effort chat_template_kwarg (jinja models like gpt-oss / LFM2.5 honor it). Overrides the LLM model's own reasoning_effort. 'none' also turns thinking off.",
			Component:   "select",
			Options: []FieldOption{
				{Value: "", Label: "Default (model config)"},
				{Value: "none", Label: "none (disable thinking)"},
				{Value: "minimal", Label: "minimal"},
				{Value: "low", Label: "low"},
				{Value: "medium", Label: "medium"},
				{Value: "high", Label: "high"},
			},
			Order: 64,
		},
		"pipeline.disable_thinking": {
			Section:     "pipeline",
			Label:       "Disable Thinking",
			Description: "Suppress reasoning/thinking output from the pipeline LLM (sets enable_thinking=false on the underlying model). Use for models that emit <think> blocks you don't want spoken or streamed back to the realtime client.",
			Component:   "toggle",
			Order:       65,
		},
		"pipeline.streaming.llm": {
			Section:     "pipeline",
			Label:       "Stream LLM",
			Description: "Stream LLM tokens to the realtime client as they are generated instead of waiting for the full response. Emits incremental response.output_audio_transcript.delta / text deltas.",
			Component:   "toggle",
			Order:       66,
		},
		"pipeline.streaming.tts": {
			Section:     "pipeline",
			Label:       "Stream TTS",
			Description: "Stream synthesized audio chunks to the realtime client as they are produced (requires a TTS backend that implements TTSStream). Falls back to unary synthesis otherwise.",
			Component:   "toggle",
			Order:       67,
		},
		"pipeline.streaming.transcription": {
			Section:     "pipeline",
			Label:       "Stream Transcription",
			Description: "Stream partial transcription text to the realtime client as the STT backend produces it (requires a transcription backend that implements AudioTranscriptionStream). Falls back to unary transcription otherwise.",
			Component:   "toggle",
			Order:       68,
		},
		"pipeline.streaming.clause_chunking": {
			Section:     "pipeline",
			Label:       "Clause Chunking",
			Description: "Split the streamed reply into speakable clauses and synthesize each as soon as it completes, instead of buffering the whole message before TTS — lower time-to-first-audio. Script-aware (handles CJK 。！？ and Thai/Lao spaces), so it does not whitespace-split. Requires Stream LLM; off buffers the whole message.",
			Component:   "toggle",
			Order:       69,
		},

		// --- Functions ---
		"function.grammar.parallel_calls": {
			Section:     "functions",
			Label:       "Parallel Calls",
			Description: "Allow the LLM to return multiple function calls in one response",
			Order:       70,
		},
		"function.grammar.mixed_mode": {
			Section:     "functions",
			Label:       "Mixed Mode",
			Description: "Allow the LLM to return both text and function calls",
			Order:       71,
		},
		"function.grammar.disable": {
			Section:     "functions",
			Label:       "Disable Grammar",
			Description: "Disable grammar-constrained generation for function calls",
			Advanced:    true,
			Order:       72,
		},

		// --- TTS ---
		"tts.voice": {
			Section:     "tts",
			Label:       "Voice",
			Description: "Default voice for TTS output",
			Component:   "input",
			Order:       90,
		},

		// --- Diffusers ---
		"diffusers.pipeline_type": {
			Section:     "diffusers",
			Label:       "Pipeline Type",
			Description: "Diffusers pipeline type (e.g. StableDiffusionPipeline)",
			Component:   "select",
			Options:     DiffusersPipelineOptions,
			Order:       80,
		},
		"diffusers.scheduler_type": {
			Section:     "diffusers",
			Label:       "Scheduler Type",
			Description: "Noise scheduler type",
			Component:   "select",
			Options:     DiffusersSchedulerOptions,
			Order:       81,
		},
		"diffusers.cuda": {
			Section:     "diffusers",
			Label:       "CUDA",
			Description: "Enable CUDA for diffusers",
			Order:       82,
		},

		// --- PII filtering (per-model) ---
		"pii.enabled": {
			Section:     "other",
			Label:       "PII Filtering Enabled",
			Description: "Enable PII redaction middleware for this model. Unset means use the default (off for local backends, on for proxy-* / cloud-hosted backends).",
			Component:   "toggle",
			Order:       200,
		},
		"pii.patterns": {
			Section:     "other",
			Label:       "PII Pattern Overrides",
			Description: "Override the global default action for specific patterns on this model. Patterns not listed here inherit the global action (Settings → Middleware → Filtering).",
			Component:   "pii-pattern-list",
			Order:       201,
		},

		// --- Cloud passthrough proxy ---
		// These only have an effect when Backend is set to
		// "cloud-proxy". When the upstream URL is empty, the model
		// fails closed — the chat handler does NOT silently fall back
		// to the local gRPC pipeline.
		"proxy.mode": {
			Section:     "other",
			Label:       "Proxy Mode",
			Description: "passthrough forwards the client's OpenAI body verbatim — point upstream_url at an OpenAI-compatible endpoint (incl. Anthropic's /v1/chat/completions compat layer). translate converts OpenAI ↔ Anthropic Messages so you can target a native API (/v1/messages); tool_calls and usage tokens survive the round-trip.",
			Component:   "select",
			Options: []FieldOption{
				{Value: "passthrough", Label: "passthrough (raw forward)"},
				{Value: "translate", Label: "translate (OpenAI ↔ native)"},
			},
			Default: "passthrough",
			Order:   208,
		},
		"proxy.provider": {
			Section:     "other",
			Label:       "Proxy Provider",
			Description: "Upstream API family. Drives auth header shape (Bearer vs x-api-key + anthropic-version) and, in translate mode, which request/response codec is used.",
			Component:   "select",
			Options: []FieldOption{
				{Value: "openai", Label: "OpenAI"},
				{Value: "anthropic", Label: "Anthropic"},
			},
			Default: "openai",
			Order:   209,
		},
		"proxy.upstream_url": {
			Section:     "other",
			Label:       "Proxy Upstream URL",
			Description: "Full POST endpoint of the upstream provider (e.g. https://api.openai.com/v1/chat/completions). Only used when Backend is cloud-proxy.",
			Component:   "input",
			Order:       210,
		},
		"proxy.api_key_env": {
			Section:     "other",
			Label:       "Proxy API Key Env Var",
			Description: "Name of the environment variable holding the upstream API key. Reading from env keeps the secret out of the YAML and the admin UI.",
			Component:   "input",
			Order:       211,
		},
		"proxy.upstream_model": {
			Section:     "other",
			Label:       "Proxy Upstream Model",
			Description: "Model name sent to the upstream. Leave empty to forward the client's model field unchanged. Useful when the LocalAI alias differs from the upstream's canonical name.",
			Component:   "input",
			Order:       212,
		},
		"proxy.request_timeout_seconds": {
			Section:     "other",
			Label:       "Proxy Request Timeout (seconds)",
			Description: "Caps the upstream HTTP request duration. 0 disables the deadline; the request still ends when the client disconnects.",
			Component:   "number",
			Min:         f64(0),
			Order:       213,
		},

		// --- MITM intercept hosts ---
		// Each host listed here is claimed by this model config; the
		// cloudproxy MITM listener (see Middleware → MITM Proxy) uses
		// THIS config's pii: settings to filter the intercepted traffic.
		// A host claimed by two configs is a critical error — the
		// listener refuses to start until resolved.
		"mitm.hosts": {
			Section:     "other",
			Label:       "MITM Intercept Hosts",
			Description: "Hostnames the cloudproxy MITM proxy terminates TLS for on behalf of this model config. PII filtering and pattern overrides flow from this model when the host is intercepted. Each host must be unique across all configs.",
			Component:   "string-list",
			Order:       220,
		},

		// --- Router ---
		// Routing turns this model config into a dispatcher: the
		// classifier scores every policy label as a continuation of
		// the routing prompt and picks the first candidate whose
		// labels are a superset of the active set. The Routing tab of
		// the middleware admin page surfaces every model with a router
		// block.
		"router.classifier": {
			Section:     "other",
			Label:       "Classifier",
			Description: "Picks a candidate by scoring every policy label against the prompt. Only \"score\" is shipped today; it asks the classifier_model to rank each label and reads off the softmax. Empty defaults to \"score\".",
			Component:   "select",
			Options: []FieldOption{
				{Value: "score", Label: "Score (Arch-Router-style)"},
			},
			Order: 230,
		},
		"router.classifier_model": {
			Section:              "other",
			Label:                "Classifier Model",
			Description:          "Loaded LocalAI model the score classifier asks to rank each policy label as a continuation. Must support the Score gRPC primitive (today: llama-cpp, vLLM) and use the ChatML template. Arch-Router-1.5B Q4_K_M is the canonical choice; any small ChatML instruct model also works at a higher activation_threshold.",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsChat,
			Order:                231,
		},
		"router.fallback": {
			Section:              "other",
			Label:                "Fallback Model",
			Description:          "Model used when no candidate's labels cover the classifier's active label set, or when the classifier errors. Empty means router failures bubble up as HTTP 500 — fail-fast, not silent-bypass.",
			Component:            "model-select",
			AutocompleteProvider: ProviderModelsChat,
			Order:                232,
		},
		"router.activation_threshold": {
			Section:     "other",
			Label:       "Activation Threshold",
			Description: "Softmax-probability floor a policy must clear to join the active label set for a request. Higher → single-label dominant routes; lower → more multi-label activations. 0 picks the package default (0.15). On Arch-Router-1.5B a value around 0.40 keeps the dominant label clean without losing genuine compound activations.",
			Component:   "slider",
			Min:         f64(0),
			Max:         f64(1),
			Step:        f64(0.05),
			Order:       233,
		},
		"router.classifier_cache_size": {
			Section:     "other",
			Label:       "Classifier L1 Cache Size",
			Description: "Bounded LRU keyed on (case-folded, whitespace-trimmed) prompt — amortises the classifier round-trip across verbatim repeats common in agent loops. 0 here means \"use the default\" (1024); the cache cannot be disabled from YAML.",
			Component:   "number",
			Min:         f64(0),
			Order:       234,
		},
		"router.policies": {
			Section:     "other",
			Label:       "Policies",
			Description: "Label vocabulary the classifier scores over. Each policy has a label and a short natural-language description fed verbatim to the classifier model. Short action-oriented sentences work best (\"writing or debugging code\"; \"small talk\").",
			Component:   "router-policies",
			Order:       235,
		},
		"router.candidates": {
			Section:     "other",
			Label:       "Candidates",
			Description: "Routing table: each entry binds a downstream model to a set of policy labels it can serve. Order matters — the middleware picks the FIRST candidate whose labels are a superset of the active set, so list candidates smallest → largest.",
			Component:   "router-candidates",
			Order:       236,
		},
		"router.score_normalization": {
			Section:     "other",
			Label:       "Score Normalization",
			Description: "How the score classifier collapses per-candidate joint log-probs into the softmax input. \"raw\" (default) feeds joint log-prob as-is — on-distribution for Arch-Router (the route the model would actually emit if decoded freely). \"mean\" divides by candidate token count — fairer to long labels but off-distribution for models trained to emit fixed-format outputs.",
			Component:   "select",
			Options: []FieldOption{
				{Value: "", Label: "Raw (default)"},
				{Value: "raw", Label: "Raw"},
				{Value: "mean", Label: "Mean (length-normalised)"},
			},
			Order: 240,
		},
		"router.embedding_cache.embedding_model": {
			Section:              "other",
			Label:                "L2 Cache: Embedding Model",
			Description:          "Embedding model used by the L2 decision cache. Embeds incoming probes and looks them up in the per-router local-store collection. Empty disables the cache entirely. nomic-embed-text-v1.5 is the recommended default.",
			Component:            "model-select",
			AutocompleteProvider: ProviderModels,
			Order:                237,
		},
		"router.embedding_cache.similarity_threshold": {
			Section:     "other",
			Label:       "L2 Cache: Similarity Threshold",
			Description: "Cosine-similarity floor a cache candidate must clear to count as a hit. 0 picks the package default (0.80). Re-tune per embedding model — the histogram on the Routing tab shows where the cosine distribution actually sits.",
			Component:   "slider",
			Min:         f64(0),
			Max:         f64(1),
			Step:        f64(0.01),
			Order:       238,
		},
		"router.embedding_cache.confidence_threshold": {
			Section:     "other",
			Label:       "L2 Cache: Confidence Threshold",
			Description: "Minimum top-label probability a classifier decision must have to be inserted into the cache. 0 picks the package default (0.60). Uncertain decisions are skipped so they can't poison future paraphrases.",
			Component:   "slider",
			Min:         f64(0),
			Max:         f64(1),
			Step:        f64(0.05),
			Order:       239,
		},
		"router.embedding_cache.store_name": {
			Section:     "other",
			Label:       "L2 Cache: Store Name",
			Description: "Optional override for the local-store collection used by this router's cache. Empty defaults to \"router-cache-<router-model-name>\". Two routers sharing a store_name share their cache (rare).",
			Component:   "input",
			Order:       240,
		},
	}
}
