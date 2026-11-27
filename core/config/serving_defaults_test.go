package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Serving-policy config defaults", func() {
	Describe("ApplyServingDefaults (cross-request prefix cache)", func() {
		It("enables cache_reuse when unset", func() {
			cfg := &ModelConfig{}
			ApplyServingDefaults(cfg)
			Expect(cfg.Options).To(ContainElement("cache_reuse:256"))
		})
		It("never overrides an explicit cache_reuse", func() {
			cfg := &ModelConfig{Options: []string{"cache_reuse:0"}}
			ApplyServingDefaults(cfg)
			Expect(cfg.Options).To(Equal([]string{"cache_reuse:0"}))
		})
		It("recognizes the n_cache_reuse alias", func() {
			cfg := &ModelConfig{Options: []string{"n_cache_reuse:512"}}
			ApplyServingDefaults(cfg)
			Expect(cfg.Options).To(Equal([]string{"n_cache_reuse:512"}))
		})
		It("no-ops on nil", func() {
			Expect(func() { ApplyServingDefaults(nil) }).ToNot(Panic())
		})
		It("does not enable cache_reuse for a non-llama backend", func() {
			// cache_reuse is a llama.cpp server option (n_cache_reuse). Backends
			// that strictly validate their options (e.g. longcat-video) reject it
			// with "unknown model option(s)". Only the llama.cpp path gets it.
			cfg := &ModelConfig{}
			cfg.Backend = "longcat-video"
			ApplyServingDefaults(cfg)
			Expect(cfg.Options).ToNot(ContainElement(ContainSubstring("cache_reuse")))
		})
		It("still enables cache_reuse for an explicit llama-cpp backend", func() {
			cfg := &ModelConfig{}
			cfg.Backend = "llama-cpp"
			ApplyServingDefaults(cfg)
			Expect(cfg.Options).To(ContainElement("cache_reuse:256"))
		})
		It("enables cache_reuse for a pinned llama.cpp variant", func() {
			// The gallery ships one concrete backend per hardware capability
			// behind the "llama-cpp" meta name, and an operator may pin any of
			// them in a model config. They all run the same server, so a pinned
			// name must get the same serving defaults as the meta name - the
			// same family rule IsLlamaCppBackend already applies elsewhere.
			for _, backend := range []string{"cpu-llama-cpp", "cuda12-llama-cpp", "metal-llama-cpp", "vulkan-llama-cpp", "rocm-llama-cpp", "cuda13-nvidia-l4t-arm64-llama-cpp", "llama-cpp-development"} {
				cfg := &ModelConfig{Backend: backend}
				ApplyServingDefaults(cfg)
				Expect(cfg.Options).To(ContainElement("cache_reuse:256"), "backend %q", backend)
			}
		})
		It("does not enable cache_reuse for the llama.cpp quantizer channel", func() {
			// "llama-cpp-quantization" shares llama.cpp's name but ships the
			// quantizer CLI, not the serving server: it takes neither
			// cache_reuse nor parallel.
			for _, backend := range []string{"llama-cpp-quantization", "cpu-llama-cpp-quantization", "metal-darwin-arm64-llama-cpp-quantization"} {
				cfg := &ModelConfig{Backend: backend}
				ApplyServingDefaults(cfg)
				Expect(cfg.Options).ToNot(ContainElement(ContainSubstring("cache_reuse")), "backend %q", backend)
			}
		})
	})
})
