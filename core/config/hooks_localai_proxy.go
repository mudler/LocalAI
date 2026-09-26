package config

func init() {
	RegisterBackendHook("localai-proxy", localAIProxyDefaults)
}

// localAIProxyDefaults makes chat requests reach the upstream as structured
// messages. Without the tokenizer template core renders the prompt itself
// with no template, and the proxy can only send that text to
// /v1/completions, bypassing the upstream model's chat template, tool
// handling and reasoning parsing.
//
// Only configs that declare the chat usecase get it: usecase guessing reads
// the tokenizer template as "this model chats", so setting it on a
// transcription or embedding proxy would offer that model to chat pickers
// and default-model selection. A config that brings its own templates keeps
// them: the operator chose local templating.
func localAIProxyDefaults(cfg *ModelConfig, _ string) {
	t := cfg.TemplateConfig
	if t.UseTokenizerTemplate || t.Chat != "" || t.ChatMessage != "" || t.Completion != "" || t.Edit != "" {
		return
	}
	declared := GetUsecasesFromYAML(cfg.KnownUsecaseStrings)
	if declared == nil || *declared&FLAG_CHAT != FLAG_CHAT {
		return
	}
	cfg.TemplateConfig.UseTokenizerTemplate = true
}
