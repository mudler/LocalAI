package openai

import "github.com/mudler/LocalAI/core/config"

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
