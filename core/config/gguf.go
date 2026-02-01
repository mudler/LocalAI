package config

import (
	"context"

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

}

// DetectThinkingSupportFromBackend calls the ModelMetadata gRPC method to detect
// if the model supports thinking mode and if the template ends with a thinking start token.
// This should be called after the model is loaded.
// The results are stored in cfg.SupportsThinking and cfg.ThinkingForcedOpen.
func DetectThinkingSupportFromBackend(ctx context.Context, cfg *ModelConfig, backendClient grpc.Backend, modelOptions *pb.ModelOptions) {
	if backendClient == nil {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: backend client is nil, skipping detection")
		return
	}

	if modelOptions == nil {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: model options is nil, skipping detection")
		return
	}

	// Only detect for llama-cpp backend when using tokenizer templates
	if cfg.Backend != "llama-cpp" || !cfg.TemplateConfig.UseTokenizerTemplate {
		xlog.Debug("[gguf] DetectThinkingSupportFromBackend: skipping detection", "backend", cfg.Backend, "useTokenizerTemplate", cfg.TemplateConfig.UseTokenizerTemplate)
		return
	}

	metadata, err := backendClient.ModelMetadata(ctx, modelOptions)
	if err != nil {
		xlog.Warn("[gguf] DetectThinkingSupportFromBackend: failed to get model metadata", "error", err)
		return
	}

	if metadata != nil {
		cfg.ReasoningConfig.DisableReasoning = ptr.To(!metadata.SupportsThinking)

		// Use the rendered template to detect if thinking token is at the end
		// This reuses the existing DetectThinkingStartToken function
		if metadata.RenderedTemplate != "" {
			thinkingStartToken := reasoning.DetectThinkingStartToken(metadata.RenderedTemplate, &cfg.ReasoningConfig)
			thinkingForcedOpen := thinkingStartToken != ""
			cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(!thinkingForcedOpen)
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: thinking support detected", "supports_thinking", metadata.SupportsThinking, "thinking_forced_open", thinkingForcedOpen, "thinking_start_token", thinkingStartToken)
		} else {
			cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)
			xlog.Debug("[gguf] DetectThinkingSupportFromBackend: thinking support detected", "supports_thinking", metadata.SupportsThinking, "thinking_forced_open", false)
		}
	}
}
