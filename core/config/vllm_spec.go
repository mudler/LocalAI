package config

// Speculative-decoding auto-defaults for the vllm-cpp backend, the safetensors
// counterpart of the GGUF/llama.cpp hook in mtp.go.
//
// The two engines detect and spell the same feature differently. llama.cpp
// reads `<arch>.nextn_predict_layers` out of the GGUF header and takes
// `spec_type:draft-mtp` in `options:`; vllm.cpp reads `mtp_num_hidden_layers`
// out of the checkpoint's config.json and takes vLLM's own
// `--speculative-config` JSON, which LocalAI carries in `engine_args`. The
// engine resolves the draft depth and the default k itself, so the config only
// has to name the method.

import (
	"encoding/json"

	"github.com/mudler/xlog"
)

// hfSpecConfig is the subset of a HuggingFace config.json that decides whether
// speculative decoding can be auto-enabled.
type hfSpecConfig struct {
	ModelType string `json:"model_type"`
	// MtpNumHiddenLayers is the MTP head depth (upstream speculative.py reads
	// it as n_predict for the qwen3_5 / qwen3_5_moe families).
	MtpNumHiddenLayers uint32 `json:"mtp_num_hidden_layers"`
	// DFlashConfig marks a z-lab DFlash DRAFT checkpoint (mask_token_id +
	// target_layer_ids). Its presence means this repo is a draft, not a
	// servable target.
	DFlashConfig json.RawMessage `json:"dflash_config"`
	// TextConfig is where multimodal checkpoints nest the language-model
	// config, and therefore the MTP depth.
	TextConfig *hfSpecConfig `json:"text_config"`
}

// parseHFSpecConfig decodes the speculative-relevant subset of a config.json.
// A document that does not parse yields nothing rather than an error: detection
// is best-effort and must never break an import.
func parseHFSpecConfig(configJSON []byte) (hfSpecConfig, bool) {
	if len(configJSON) == 0 {
		return hfSpecConfig{}, false
	}
	var c hfSpecConfig
	if err := json.Unmarshal(configJSON, &c); err != nil {
		xlog.Debug("[vllm-spec] config.json did not parse; skipping detection", "error", err)
		return hfSpecConfig{}, false
	}
	return c, true
}

// IsDFlashDraftConfig reports whether a HuggingFace config.json describes a
// DFlash DRAFT checkpoint. Unlike MTP - whose head ships inside the target
// checkpoint's `mtp.*` tensors - a DFlash draft is its own repo that can only
// run paired with a target it verifies against, so it must never be configured
// as a standalone model.
func IsDFlashDraftConfig(configJSON []byte) bool {
	c, ok := parseHFSpecConfig(configJSON)
	if !ok {
		return false
	}
	return len(c.DFlashConfig) > 0 ||
		(c.TextConfig != nil && len(c.TextConfig.DFlashConfig) > 0)
}

// HasSafetensorsMTPHead reports whether a HuggingFace config.json declares a
// self-speculating Multi-Token Prediction head, returning its depth. The depth
// is informational: vllm.cpp resolves n_predict and the default
// num_speculative_tokens from the checkpoint itself.
//
// DFlash drafts are excluded for the same reason `gemma4-assistant` GGUFs are
// excluded from the llama.cpp hook: they carry head metadata but cannot
// self-speculate.
//
// NOTE this is a safetensors-only signal. vllm.cpp rejects an MTP config over a
// GGUF source, because the `mtp.*` draft tensors only exist in the safetensors
// checkpoint - so the GGUF import path must not use this.
func HasSafetensorsMTPHead(configJSON []byte) (uint32, bool) {
	c, ok := parseHFSpecConfig(configJSON)
	if !ok {
		return 0, false
	}
	if IsDFlashDraftConfig(configJSON) {
		return 0, false
	}
	n := c.MtpNumHiddenLayers
	if n == 0 && c.TextConfig != nil {
		n = c.TextConfig.MtpNumHiddenLayers
	}
	return n, n > 0
}

// ApplyVLLMSpeculativeDefaults enables MTP speculative decoding in cfg's
// engine_args when nothing is configured there yet. It is a no-op when the user
// already set a speculative_config, so an explicit choice (a different method,
// an explicit k, a DFlash draft) is never clobbered.
//
// `layers` is the detected head depth and is only used for the diagnostic log
// line - the engine derives the real k from the checkpoint.
func ApplyVLLMSpeculativeDefaults(cfg *ModelConfig, layers uint32) {
	if cfg == nil {
		return
	}
	if _, set := cfg.EngineArgs["speculative_config"]; set {
		xlog.Debug("[vllm-spec] MTP head detected but speculative_config already configured; leaving user choice intact",
			"name", cfg.Name, "mtp_num_hidden_layers", layers)
		return
	}
	if cfg.EngineArgs == nil {
		cfg.EngineArgs = map[string]any{}
	}
	// Only the method: vllm.cpp defaults num_speculative_tokens to the
	// checkpoint's own n_predict (speculative.py:865-875), which is the right
	// value far more reliably than anything guessable here.
	cfg.EngineArgs["speculative_config"] = map[string]any{"method": "mtp"}
	xlog.Info("[vllm-spec] MTP head detected; enabling mtp speculative decoding",
		"name", cfg.Name, "mtp_num_hidden_layers", layers)
}
