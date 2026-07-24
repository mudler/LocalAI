package model_test

import (
	"github.com/mudler/LocalAI/pkg/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func init() {
	// The real LLM-capability table lives in core/config, which pkg/model must
	// not import (cycle). Register a fake predicate so the selection algorithm
	// is exercised here independent of the capability table (#9287).
	model.RegisterLLMCapableBackendFunc(func(name string) bool {
		return name == "llama-cpp" || name == "vllm"
	})
}

var _ = Describe("SelectAutoLoadBackends (#9287)", func() {
	Describe("GGUF model auto-detection", func() {
		It("excludes incompatible audio/codec backends (e.g. opus) for a .gguf model", func() {
			// Regression for #9287: installing an unrelated audio backend like
			// "opus" must never win the GGUF auto-detect trial loop.
			got := model.SelectAutoLoadBackends([]string{"opus", "llama-cpp"}, "Qwen3.5-9b.gguf")
			Expect(got).NotTo(ContainElement("opus"))
			Expect(got).To(ContainElement("llama-cpp"))
		})

		It("places llama-cpp first for a .gguf model", func() {
			got := model.SelectAutoLoadBackends([]string{"vllm", "opus", "llama-cpp"}, "model.gguf")
			Expect(got).NotTo(BeEmpty())
			Expect(got[0]).To(Equal("llama-cpp"))
		})

		It("is deterministic regardless of input ordering", func() {
			a := model.SelectAutoLoadBackends([]string{"opus", "vllm", "llama-cpp", "whisper"}, "m.gguf")
			b := model.SelectAutoLoadBackends([]string{"whisper", "llama-cpp", "vllm", "opus"}, "m.gguf")
			Expect(a).To(Equal(b))
		})

		It("falls back to the full sorted set when filtering leaves no candidate", func() {
			// No LLM-capable backend installed: never make a previously-loadable
			// model unloadable, return the original set (sorted).
			got := model.SelectAutoLoadBackends([]string{"opus"}, "model.gguf")
			Expect(got).To(Equal([]string{"opus"}))
		})
	})

	Describe("non-GGUF model auto-detection", func() {
		It("returns a deterministic (sorted) set without filtering", func() {
			got := model.SelectAutoLoadBackends([]string{"opus", "llama-cpp", "diffusers"}, "model-dir")
			Expect(got).To(Equal([]string{"diffusers", "llama-cpp", "opus"}))
		})
	})
})
