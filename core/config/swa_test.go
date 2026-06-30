package config_test

import (
	. "github.com/mudler/LocalAI/core/config"

	gguf "github.com/gpustack/gguf-parser-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ggufWithSlidingWindow fabricates a minimal in-memory GGUF carrying the given
// `general.architecture` and `<arch>.attention.sliding_window` so the SWA
// detection can be exercised without a real model file. A window of 0 omits the
// key, modelling a dense (non-SWA) model.
func ggufWithSlidingWindow(arch string, window uint32) *gguf.GGUFFile {
	kvs := gguf.GGUFMetadataKVs{
		{
			Key:       "general.architecture",
			ValueType: gguf.GGUFMetadataValueTypeString,
			Value:     arch,
		},
	}
	if window > 0 {
		kvs = append(kvs, gguf.GGUFMetadataKV{
			Key:       arch + ".attention.sliding_window",
			ValueType: gguf.GGUFMetadataValueTypeUint32,
			Value:     window,
		})
	}
	return &gguf.GGUFFile{
		Header: gguf.GGUFHeader{MetadataKV: kvs},
	}
}

var _ = Describe("SWA full-cache auto-default", func() {
	Context("HasSlidingWindowAttention", func() {
		It("returns false on a nil GGUF file", func() {
			w, ok := HasSlidingWindowAttention(nil)
			Expect(ok).To(BeFalse())
			Expect(w).To(BeZero())
		})

		It("detects a sliding-window model (Gemma 3 style)", func() {
			w, ok := HasSlidingWindowAttention(ggufWithSlidingWindow("gemma3", 1024))
			Expect(ok).To(BeTrue())
			Expect(w).To(Equal(uint64(1024)))
		})

		It("detects Gemma 2 even without an explicit key (family default window)", func() {
			// gguf-parser applies llama.cpp's family rules: gemma2 defaults the
			// sliding window to 4096 when the metadata key is absent.
			w, ok := HasSlidingWindowAttention(ggufWithSlidingWindow("gemma2", 0))
			Expect(ok).To(BeTrue())
			Expect(w).To(Equal(uint64(4096)))
		})

		It("reports a dense model as non-SWA", func() {
			w, ok := HasSlidingWindowAttention(ggufWithSlidingWindow("llama", 0))
			Expect(ok).To(BeFalse())
			Expect(w).To(BeZero())
		})

		It("treats Phi-3 as non-SWA even when the key is present", func() {
			// Phi-3 carries attention.sliding_window but does not actually run
			// SWA; gguf-parser normalizes it to 0 to match llama.cpp.
			w, ok := HasSlidingWindowAttention(ggufWithSlidingWindow("phi3", 2048))
			Expect(ok).To(BeFalse())
			Expect(w).To(BeZero())
		})
	})

	Context("ApplySWAFullDefault", func() {
		It("enables swa_full for a sliding-window model when unset", func() {
			cfg := &ModelConfig{Name: "gemma3"}
			ApplySWAFullDefault(cfg, 1024)
			Expect(cfg.Options).To(ContainElement("swa_full:true"))
		})

		It("is a no-op for a dense model (window 0)", func() {
			cfg := &ModelConfig{Name: "llama"}
			ApplySWAFullDefault(cfg, 0)
			Expect(cfg.Options).To(BeEmpty())
		})

		It("preserves an explicit swa_full:false", func() {
			cfg := &ModelConfig{Name: "gemma3", Options: []string{"swa_full:false"}}
			ApplySWAFullDefault(cfg, 1024)
			Expect(cfg.Options).To(Equal([]string{"swa_full:false"}))
		})

		It("preserves an explicit swa_full:true without duplicating it", func() {
			cfg := &ModelConfig{Name: "gemma3", Options: []string{"swa_full:true"}}
			ApplySWAFullDefault(cfg, 1024)
			Expect(cfg.Options).To(Equal([]string{"swa_full:true"}))
		})

		It("respects the n_swa alias", func() {
			cfg := &ModelConfig{Name: "gemma3", Options: []string{"n_swa:512"}}
			ApplySWAFullDefault(cfg, 1024)
			Expect(cfg.Options).To(Equal([]string{"n_swa:512"}))
		})

		It("preserves unrelated options already on the config", func() {
			cfg := &ModelConfig{
				Name:    "gemma3",
				Options: []string{"use_jinja:true", "cache_reuse:256"},
			}
			ApplySWAFullDefault(cfg, 1024)
			Expect(cfg.Options).To(Equal([]string{
				"use_jinja:true",
				"cache_reuse:256",
				"swa_full:true",
			}))
		})

		It("tolerates a nil config", func() {
			Expect(func() { ApplySWAFullDefault(nil, 1024) }).ToNot(Panic())
		})
	})
})
