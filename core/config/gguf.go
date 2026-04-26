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

const (
	defaultContextSize = 1024
	defaultNGPULayers  = 99999999
)

func guessGGUFFromFile(cfg *ModelConfig, f *gguf.GGUFFile, defaultCtx int) {

	if defaultCtx == 0 && cfg.ContextSize == nil {
		ctxSize := f.EstimateLLaMACppRun().ContextSize
		if ctxSize > 0 {
			cSize := int(ctxSize)
			cfg.ContextSize = &cSize
		} else {
			defaultCtx = defaultContextSize
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
		defaultHigh := defaultNGPULayers
		cfg.NGPULayers = &defaultHigh
	}

	xlog.Debug("[gguf] guessDefaultsFromFile: NGPULayers set", "NGPULayers", cfg.NGPULayers, "modelName", f.Metadata().Name)

	// identify from well known templates first, otherwise use the raw jinja template
	chatTemplate, found := f.Header.MetadataKV.Get("tokenizer.chat_template")
	if found {
		// fill jinja template
		cfg.modelTemplate = chatTemplate.ValueString()
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

	// Instruct to use template from llama.cpp
	cfg.TemplateConfig.UseTokenizerTemplate = true
	cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
	cfg.Options = append(cfg.Options, "use_jinja:true")
	cfg.KnownUsecaseStrings = append(cfg.KnownUsecaseStrings, "FLAG_CHAT")

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
	if cfg.Backend != "llama-cpp" {
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
