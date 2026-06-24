package config

import "os"

// ApplyGenericDefaults fills the generic fallback values applied after the
// higher-priority tiers (ApplyInferenceDefaults for the model family,
// ApplyHardwareDefaults for the device, ApplyServingDefaults for serving
// policy): sampling parameters and a few runtime flags. Like the other tiers it
// only fills values still left unset, so model-family / explicit config wins.
func ApplyGenericDefaults(cfg *ModelConfig) {
	if cfg == nil {
		return
	}

	// https://github.com/ggerganov/llama.cpp/blob/75cd4c77292034ecec587ecb401366f57338f7c0/common/sampling.h#L22
	defaultTopP := 0.95
	defaultTopK := 40
	defaultMinP := 0.0
	defaultTemp := 0.9
	// https://github.com/mudler/LocalAI/issues/2780
	defaultMirostat := 0
	defaultMirostatTAU := 5.0
	defaultMirostatETA := 0.1
	defaultTypicalP := 1.0
	defaultTFZ := 1.0
	defaultZero := 0

	trueV := true
	falseV := false

	if cfg.Seed == nil {
		//  random number generator seed
		defaultSeed := RAND_SEED
		cfg.Seed = &defaultSeed
	}

	// top_k=40 is llama.cpp's sampling default and is wrong for backends whose
	// native default differs (issue #6632). Only inject it for the llama.cpp
	// family and the empty/auto backend; leave TopK nil for known non-llama
	// backends (e.g. mlx, whose intended default is top_k=0) so the wire value
	// is 0 rather than a silently-changed 40.
	if cfg.TopK == nil && UsesLlamaSamplerDefaults(cfg.Backend) {
		cfg.TopK = &defaultTopK
	}

	if cfg.MinP == nil {
		cfg.MinP = &defaultMinP
	}

	if cfg.TypicalP == nil {
		cfg.TypicalP = &defaultTypicalP
	}

	if cfg.TFZ == nil {
		cfg.TFZ = &defaultTFZ
	}

	if cfg.MMap == nil {
		// MMap is enabled by default

		// Only exception is for Intel GPUs
		if os.Getenv("XPU") != "" {
			cfg.MMap = &falseV
		} else {
			cfg.MMap = &trueV
		}
	}

	if cfg.MMlock == nil {
		// MMlock is disabled by default
		cfg.MMlock = &falseV
	}

	if cfg.TopP == nil {
		cfg.TopP = &defaultTopP
	}
	if cfg.Temperature == nil {
		cfg.Temperature = &defaultTemp
	}

	if cfg.Maxtokens == nil {
		cfg.Maxtokens = &defaultZero
	}

	if cfg.Mirostat == nil {
		cfg.Mirostat = &defaultMirostat
	}

	if cfg.MirostatETA == nil {
		cfg.MirostatETA = &defaultMirostatETA
	}

	if cfg.MirostatTAU == nil {
		cfg.MirostatTAU = &defaultMirostatTAU
	}

	if cfg.LowVRAM == nil {
		cfg.LowVRAM = &falseV
	}

	if cfg.Embeddings == nil {
		cfg.Embeddings = &falseV
	}

	if cfg.Reranking == nil {
		cfg.Reranking = &falseV
	}

	if cfg.PromptCacheAll == nil {
		// Match upstream llama.cpp's default (common/common.h: cache_prompt = true)
		// and let cache_idle_slots / kv_unified actually do useful work; users can
		// opt out with an explicit `prompt_cache_all: false` in the model YAML.
		cfg.PromptCacheAll = &trueV
	}
}
