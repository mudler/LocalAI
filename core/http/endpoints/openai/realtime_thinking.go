package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/reasoning"
)

// applyPipelineThinking forces the LLM's reasoning/thinking off when the realtime
// pipeline sets disable_thinking, mapping to the enable_thinking=false backend
// metadata via ReasoningConfig.DisableReasoning. The LLM config passed in is the
// per-session copy returned by the config loader, so this does not affect other
// users of the same model. When the pipeline does not set disable_thinking the
// LLM config is left untouched.
func applyPipelineThinking(llm *config.ModelConfig, pipeline config.Pipeline) {
	if llm == nil || !pipeline.ThinkingDisabled() {
		return
	}
	disable := true
	llm.ReasoningConfig.DisableReasoning = &disable
}

// spokenReasoningConfig adapts a model's reasoning config for stripping reasoning
// OUT of realtime spoken output. ReasoningConfig.DisableReasoning is overloaded:
// the backend reads it as the "enable_thinking=false" hint (which pipeline
// disable_thinking sets via applyPipelineThinking), but the reasoning extractor
// reads it as "skip stripping, assume there is no reasoning". Honouring the latter
// when extracting for speech would leak raw <think>…</think> whenever the model
// ignores the suppression hint. Spoken output must never contain reasoning, so we
// always strip: clear DisableReasoning while keeping custom tokens/tag pairs.
func spokenReasoningConfig(cfg reasoning.Config) reasoning.Config {
	cfg.DisableReasoning = nil
	return cfg
}
