package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ApplyGenericDefaults (generic fallback tier)", func() {
	It("fills sampling + runtime fallbacks when unset", func() {
		cfg := &ModelConfig{} // empty backend uses the llama sampler defaults
		ApplyGenericDefaults(cfg)
		Expect(cfg.TopP).ToNot(BeNil())
		Expect(*cfg.TopP).To(Equal(0.95))
		Expect(*cfg.TopK).To(Equal(40))
		Expect(*cfg.Temperature).To(Equal(0.9))
		// mmap stays unset so the backend can pick the load mode.
		Expect(cfg.MMap).To(BeNil())
		Expect(*cfg.MMlock).To(BeFalse())
		Expect(*cfg.PromptCacheAll).To(BeTrue())
	})

	It("never overrides explicit values", func() {
		tk := 7
		tp := 0.5
		cfg := &ModelConfig{}
		cfg.TopK = &tk
		cfg.TopP = &tp
		ApplyGenericDefaults(cfg)
		Expect(*cfg.TopK).To(Equal(7))
		Expect(*cfg.TopP).To(Equal(0.5))
	})

	It("disables mmap for Intel GPUs when unset", func() {
		GinkgoT().Setenv("XPU", "1")
		cfg := &ModelConfig{}
		ApplyGenericDefaults(cfg)
		Expect(cfg.MMap).To(HaveValue(BeFalse()))
	})

	It("keeps an explicit mmap value", func() {
		for _, xpu := range []string{"", "1"} {
			GinkgoT().Setenv("XPU", xpu)
			for _, v := range []bool{true, false} {
				mmap := v
				cfg := &ModelConfig{}
				cfg.MMap = &mmap
				ApplyGenericDefaults(cfg)
				Expect(cfg.MMap).To(HaveValue(Equal(v)))
			}
		}
	})

	It("no-ops on nil", func() {
		Expect(func() { ApplyGenericDefaults(nil) }).ToNot(Panic())
	})
})
