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
	})
})
