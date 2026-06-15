package openai

import "github.com/mudler/LocalAI/core/config"

// applyPipelineReasoning sets the reasoning effort for a realtime pipeline's LLM
// from the pipeline config, without editing the underlying LLM model config. The
// pipeline value overrides the LLM's own reasoning_effort; when the pipeline does
// not set it, the LLM model config's reasoning_effort (if any) is used. The LLM
// config passed in is the per-session copy returned by the config loader, so this
// does not affect other users of the same model.
func applyPipelineReasoning(llm *config.ModelConfig, pipeline config.Pipeline) {
	if llm == nil {
		return
	}
	llm.ApplyReasoningEffort(pipeline.ReasoningEffort)
}
