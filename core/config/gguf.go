package config

import (
	"context"

	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/reasoning"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/gpustack/gguf-parser-go/util/ptr"
)

// reservedNonChatModel reports whether the operator reserved this model for an
// internal primitive — the router score classifier or the PII NER
// token_classify tier. Such a model has no chat template and must not be
// given the generative-chat defaults the GGUF importer otherwise applies
// (FLAG_CHAT, jinja templating): surfacing it in chat pickers defeats the
// reservation. Operators who do want a combined model declare both usecases
// explicitly — the combination is valid.
func reservedNonChatModel(cfg *ModelConfig) bool {
	return cfg.KnownUsecases != nil &&
		(*cfg.KnownUsecases&(FLAG_SCORE|FLAG_TOKEN_CLASSIFY)) != 0
}

func guessGGUFFromFile(cfg *ModelConfig, f *gguf.GGUFFile, defaultCtx int) {
	// Explicit opt-in: a negative context_size (canonically -1) means "use the
	// model's full trained context (n_ctx_train) from GGUF metadata". Unlike the
	// silent unset path below, this overrides an already-present value and warns
	// when the resolved window will not fit detected VRAM.
	if cfg.ContextSize != nil && *cfg.ContextSize < 0 {
		if maxCtx := int(f.Architecture().MaximumContextLength); maxCtx > 0 {
			cfg.ContextSize = &maxCtx
			warnIfContextExceedsVRAM(f, maxCtx, f.Metadata().Name)
		} else {
			// No usable trained max in metadata: degrade to the safe default
			// rather than leak a negative n_ctx downstream.
			d := DefaultContextSize
			cfg.ContextSize = &d
			xlog.Warn("[gguf] context_size=-1 requested but GGUF exposes no trained max; using default",
				"default", d, "model", f.Metadata().Name)
		}
	}

	if defaultCtx == 0 && cfg.ContextSize == nil {
		// trainedMax is the model's full trained context window (n_ctx_train).
		// Defaulting a model to it unbounded is what OOMs long-context models at
		// load: a 128k / 256k / 1M KV cache cannot fit a consumer GPU and the
		// backend aborts (exitCode=-1). autoContextSize instead caps to a modest
		// default and only steps below it when detected per-device VRAM demands.
		trainedMax := int(f.EstimateLLaMACppRun().ContextSize)
		if trainedMax > 0 {
			cSize := autoContextSize(f, trainedMax)
			cfg.ContextSize = &cSize
		} else {
			defaultCtx = DefaultContextSize
			cfg.ContextSize = &defaultCtx
		}
	}

	// GPU options
	if cfg.Options == nil {
		if xsysinfo.HasGPU("nvidia") || xsysinfo.HasGPU("amd") {
			cfg.Options = []string{"gpu"}
		}
	}

	if cfg.NGPULayers == nil {
		// we assume we want to offload all layers
		defaultHigh := DefaultNGPULayers
		cfg.NGPULayers = &defaultHigh
	}

	xlog.Debug("[gguf] guessDefaultsFromFile: NGPULayers set", "NGPULayers", cfg.NGPULayers, "modelName", f.Metadata().Name)

	// identify from well known templates first, otherwise use the raw jinja template
	chatTemplate, found := f.Header.MetadataKV.Get("tokenizer.chat_template")
	if found {
		// fill jinja template
		cfg.modelTemplate = chatTemplate.ValueString()
	}

	// Auto-enable Multi-Token Prediction (ggml-org/llama.cpp#22673) when the
	// GGUF carries an embedded MTP head. Skipped silently for non-MTP models
	// and when the user already configured a spec_type.
	if n, ok := HasEmbeddedMTPHead(f); ok {
		ApplyMTPDefaults(cfg, n)
	}

	// Thinking support detection is done after model load via DetectThinkingSupportFromBackend

	// template estimations
	if cfg.HasTemplate() {
		// nothing to guess here
		xlog.Debug("[gguf] guessDefaultsFromFile: template already set", "name", cfg.Name, "modelName", f.Metadata().Name)
		return
	}

	xlog.Debug("[gguf] Model file loaded", "file", cfg.ModelFileName(), "eosTokenID", f.Tokenizer().EOSTokenID, "bosTokenID", f.Tokenizer().BOSTokenID, "modelName", f.Metadata().Name, "architecture", f.Architecture().Architecture)

	// guess the name
	if cfg.Name == "" {
		cfg.Name = f.Metadata().Name
	}

	// A model the operator reserved for an internal primitive (the router
	// score classifier, or the PII NER token_classify tier) is not a chat
	// model: it carries no chat template and must not be painted with the
	// generative-chat defaults — appending FLAG_CHAT here would fold chat
	// into KnownUsecases on the next sync and surface the model in every
	// chat picker. Respect the declaration.
	if !reservedNonChatModel(cfg) {
		// Instruct to use template from llama.cpp
		cfg.TemplateConfig.UseTokenizerTemplate = true
		cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
		cfg.Options = append(cfg.Options, "use_jinja:true")
		cfg.KnownUsecaseStrings = append(cfg.KnownUsecaseStrings, "FLAG_CHAT")
	}

	// Apply per-model-family inference parameter defaults (temperature, top_p, etc.)
	ApplyInferenceDefaults(cfg, f.Metadata().Name)
}

// DetectThinkingSupportFromBackend calls the ModelMetadata gRPC method to detect
// if the model supports thinking mode and if the template ends with a thinking start token.
// This should be called after the model is loaded.
// The results are stored in cfg.SupportsThinking and cfg.ThinkingForcedOpen.
// The backend-reported multimodal marker is also captured into cfg.MediaMarker.
func DetectThinkingSupportFromBackend(ctx context.Context, cfg *ModelConfig, backendClient grpc.Backend, modelOptions *pb.ModelOptions) {
	if backendClient == nil {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: backend client is nil, skipping detection")
		return
	}

	if modelOptions == nil {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: model options is nil, skipping detection")
		return
	}

	// Only llama-cpp exposes ModelMetadata today. Other backends will either error
	// or return an empty response — both are fine, we just bail before calling.
	// The check must cover every llama.cpp build, not just the "llama-cpp" meta
	// name: a config pinning a concrete variant ("vulkan-llama-cpp", ...) runs the
	// same server and needs the same probe (#10945).
	if !IsLlamaCppBackend(cfg.Backend) {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: skipping detection", "backend", cfg.Backend)
		return
	}

	metadata, err := backendClient.ModelMetadata(ctx, modelOptions)
	if err != nil {
		xlog.Warn("[gguf] DetectThinkingSupportFromBackend: failed to get model metadata", "error", err)
		return
	}

	if metadata != nil {
		// The multimodal media marker is backend-controlled (llama.cpp may pick a
		// random per-server string). Empty means "no mtmd context" — Go falls back
		// to templates.DefaultMultiMediaMarker at render time.
		if metadata.MediaMarker != "" {
			cfg.MediaMarker = metadata.MediaMarker
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: media marker captured", "marker", metadata.MediaMarker)
		}

		// Thinking / tool-format detection only applies when we rely on the
		// backend-side tokenizer template — otherwise the rendered-template based
		// heuristics below aren't meaningful.
		if !cfg.TemplateConfig.UseTokenizerTemplate {
			return
		}

		applyDetectedThinkingConfig(cfg, metadata)

		// Extract tool format markers from autoparser analysis
		if tf := metadata.GetToolFormat(); tf != nil && tf.FormatType != "" {
			cfg.FunctionsConfig.ToolFormatMarkers = &functions.ToolFormatMarkers{
				FormatType:        tf.FormatType,
				SectionStart:      tf.SectionStart,
				SectionEnd:        tf.SectionEnd,
				PerCallStart:      tf.PerCallStart,
				PerCallEnd:        tf.PerCallEnd,
				FuncNamePrefix:    tf.FuncNamePrefix,
				FuncNameSuffix:    tf.FuncNameSuffix,
				FuncClose:         tf.FuncClose,
				ArgNamePrefix:     tf.ArgNamePrefix,
				ArgNameSuffix:     tf.ArgNameSuffix,
				ArgValuePrefix:    tf.ArgValuePrefix,
				ArgValueSuffix:    tf.ArgValueSuffix,
				ArgSeparator:      tf.ArgSeparator,
				ArgsStart:         tf.ArgsStart,
				ArgsEnd:           tf.ArgsEnd,
				NameField:         tf.NameField,
				ArgsField:         tf.ArgsField,
				IDField:           tf.IdField,
				FunNameIsKey:      tf.FunNameIsKey,
				ToolsArrayWrapped: tf.ToolsArrayWrapped,
				FunctionField:     tf.FunctionField,
				ParameterOrder:    tf.ParameterOrder,
				GenIDField:        tf.GenIdField,
				CallIDPosition:    tf.CallIdPosition,
				CallIDPrefix:      tf.CallIdPrefix,
				CallIDSuffix:      tf.CallIdSuffix,
				ReasoningStart:    tf.ReasoningStart,
				ReasoningEnd:      tf.ReasoningEnd,
				ContentStart:      tf.ContentStart,
				ContentEnd:        tf.ContentEnd,
			}
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: tool format markers detected",
				"format_type", tf.FormatType,
				"section_start", tf.SectionStart,
				"func_name_prefix", tf.FuncNamePrefix)
		}
	}
}

func applyDetectedThinkingConfig(cfg *ModelConfig, metadata *pb.ModelMetadataResponse) {
	if cfg == nil || metadata == nil {
		return
	}

	// Respect explicit YAML/user config. Backend probing should only fill defaults
	// when the reasoning mode has not already been set.
	if cfg.ReasoningConfig.DisableReasoning == nil {
		cfg.ReasoningConfig.DisableReasoning = ptr.To(!metadata.SupportsThinking)
	}

	// Respect explicit prefill config for the same reason. Only infer the
	// default prefill behavior when the user did not set it.
	if cfg.ReasoningConfig.DisableReasoningTagPrefill == nil {
		// Use the rendered template to detect if thinking token is at the end.
		// This reuses the existing DetectThinkingStartToken function.
		if metadata.RenderedTemplate != "" {
			thinkingStartToken := reasoning.DetectThinkingStartToken(metadata.RenderedTemplate, &cfg.ReasoningConfig)
			thinkingForcedOpen := thinkingStartToken != ""
			cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(!thinkingForcedOpen)
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: thinking support detected", "supports_thinking", metadata.SupportsThinking, "thinking_forced_open", thinkingForcedOpen, "thinking_start_token", thinkingStartToken)
		} else {
			cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: thinking support detected", "supports_thinking", metadata.SupportsThinking, "thinking_forced_open", false)
		}
		return
	}

	xlog.Debug("[gguf] DetectThinkingSupportFromBackend: preserving explicit reasoning config", "supports_thinking", metadata.SupportsThinking, "disable_reasoning", *cfg.ReasoningConfig.DisableReasoning, "disable_reasoning_tag_prefill", *cfg.ReasoningConfig.DisableReasoningTagPrefill)
}

// warnIfContextExceedsVRAM logs a best-effort warning when running the model at
// the given context would not fit detected VRAM. It never blocks load: any
// detection or estimation gap (no GPU, unknown VRAM, estimate failure) silently
// skips the warning. Used by the context_size=-1 auto-max path, where the raw
// trained max can be far larger than a consumer card holds.
func warnIfContextExceedsVRAM(f *gguf.GGUFFile, ctx int, name string) {
	defer func() { _ = recover() }() // the run estimate can panic on unusual headers

	if !xsysinfo.HasGPU("nvidia") && !xsysinfo.HasGPU("amd") {
		return // no VRAM to compare against
	}
	vram, err := xsysinfo.TotalAvailableVRAM()
	if err != nil || vram == 0 {
		return
	}

	sum := f.EstimateLLaMACppRun(gguf.WithLLaMACppContextSize(int32(ctx))).Summarize(true, 0, 0)
	if len(sum.Items) == 0 {
		return
	}
	var used uint64
	for _, v := range sum.Items[0].VRAMs {
		used += uint64(v.NonUMA)
	}
	if used == 0 || used <= vram {
		return
	}
	xlog.Warn("[gguf] context_size=-1 resolved to the model's trained max; estimated VRAM may exceed available - expect OOM, or set an explicit context_size",
		"model", name, "context", ctx,
		"estimated_vram_gib", used>>30, "available_vram_gib", vram>>30)
}
