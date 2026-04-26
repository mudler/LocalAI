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
	}
}
