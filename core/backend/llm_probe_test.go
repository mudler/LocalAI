package backend

import (
	"os"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/gpustack/gguf-parser-go/util/ptr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("thinking probe gating", func() {
	It("probes tokenizer-template models when any reasoning default is still unset", func() {
		cfg := &config.ModelConfig{
			TemplateConfig: config.TemplateConfig{UseTokenizerTemplate: true},
		}
		Expect(needsThinkingProbe(cfg)).To(BeTrue())

		cfg.ReasoningConfig.DisableReasoning = ptr.To(true)
		Expect(needsThinkingProbe(cfg)).To(BeTrue())

		cfg.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)
		Expect(needsThinkingProbe(cfg)).To(BeFalse())
	})

	It("does not probe when tokenizer templates are disabled", func() {
		cfg := &config.ModelConfig{}
		Expect(needsThinkingProbe(cfg)).To(BeFalse())
	})
})

var _ = Describe("needsMediaMarkerProbe", func() {
	It("probes when the media marker slot is still empty", func() {
		Expect(needsMediaMarkerProbe("", true)).To(BeTrue())
		Expect(needsMediaMarkerProbe("", false)).To(BeTrue())
	})

	It("skips probing a cached marker when the backend is already resident", func() {
		Expect(needsMediaMarkerProbe("<__media_cached__>", true)).To(BeFalse())
	})

	It("re-probes a cached marker after a cold Load (stale process-scoped marker)", func() {
		// llama.cpp picks a new random media marker per server launch. A value
		// left on the model config from a previous process must not suppress
		// the probe when the backend was just (re)started (#12246).
		Expect(needsMediaMarkerProbe("<__media_stale_from_previous_process__>", false)).To(BeTrue())
	})
})

var _ = Describe("persistProbedReasoning", func() {
	const modelName = "probe-test"

	// newLoaderWithConfig seeds a ModelConfigLoader with a single model config
	// parsed from yamlBody, mirroring how the loader is populated from disk.
	newLoaderWithConfig := func(yamlBody string) *config.ModelConfigLoader {
		tmp, err := os.CreateTemp("", "persist-probed-reasoning-*.yaml")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = os.Remove(tmp.Name()) }()

		_, err = tmp.WriteString(yamlBody)
		Expect(err).ToNot(HaveOccurred())
		Expect(tmp.Close()).To(Succeed())

		cl := config.NewModelConfigLoader("")
		Expect(cl.ReadModelConfig(tmp.Name())).To(Succeed())
		return cl
	}

	It("persists a reasoning slot the probe was allowed to fill (was nil beforehand)", func() {
		cl := newLoaderWithConfig("name: probe-test\nbackend: llama-cpp\n")

		probed := &config.ModelConfig{}
		probed.Name = modelName
		probed.ReasoningConfig.DisableReasoning = ptr.To(false) // backend detected: supports thinking
		probed.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)

		persistProbedReasoning(cl, modelName, probed, true, true)

		cfg, ok := cl.GetModelConfig(modelName)
		Expect(ok).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeFalse())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeTrue())
	})

	It("does not persist a slot that already carried a request-scoped value before the probe ran", func() {
		cl := newLoaderWithConfig("name: probe-test\nbackend: llama-cpp\n")

		probed := &config.ModelConfig{}
		probed.Name = modelName
		// Simulates ApplyReasoningEffort("none") having set this on the
		// request-scoped copy before the probe ran - not a genuine backend
		// detection, so it must never reach the persisted config (#10622).
		probed.ReasoningConfig.DisableReasoning = ptr.To(true)

		persistProbedReasoning(cl, modelName, probed, false, false)

		cfg, ok := cl.GetModelConfig(modelName)
		Expect(ok).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoning).To(BeNil())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeNil())
	})

	It("preserves an operator's explicit persisted disable when the guard is false", func() {
		cl := newLoaderWithConfig("name: probe-test\nbackend: llama-cpp\nreasoning:\n  disable: true\n")

		probed := &config.ModelConfig{}
		probed.Name = modelName
		// Even if the request-scoped copy ends up holding a different value,
		// persistDisableReasoning=false must keep the operator's own setting.
		probed.ReasoningConfig.DisableReasoning = ptr.To(false)

		persistProbedReasoning(cl, modelName, probed, false, false)

		cfg, ok := cl.GetModelConfig(modelName)
		Expect(ok).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeTrue())
	})

	It("persists the media marker regardless of the reasoning guards", func() {
		cl := newLoaderWithConfig("name: probe-test\nbackend: llama-cpp\n")

		probed := &config.ModelConfig{}
		probed.Name = modelName
		probed.MediaMarker = "<__media__>"

		persistProbedReasoning(cl, modelName, probed, false, false)

		cfg, ok := cl.GetModelConfig(modelName)
		Expect(ok).To(BeTrue())
		Expect(cfg.MediaMarker).To(Equal("<__media__>"))
	})
})

var _ = Describe("metadataProbeDue", func() {
	llama := func(marker string, tokenizerTemplate bool) *config.ModelConfig {
		cfg := &config.ModelConfig{Backend: "llama-cpp", MediaMarker: marker}
		cfg.TemplateConfig.UseTokenizerTemplate = tokenizerTemplate
		return cfg
	}
	probed := func() *model.Model {
		m := model.NewModel("m", "127.0.0.1:1", nil)
		m.MarkMetadataProbed()
		return m
	}

	It("never probes a backend that is not llama.cpp", func() {
		for _, backend := range []string{"vllm", "transformers", "ik-llama-cpp", "mock-backend"} {
			cfg := &config.ModelConfig{Backend: backend}
			cfg.TemplateConfig.UseTokenizerTemplate = true
			Expect(metadataProbeDue(cfg, nil)).To(BeFalse(), backend)
		}
	})

	It("probes an empty media marker until the running instance answers", func() {
		Expect(metadataProbeDue(llama("", false), nil)).To(BeTrue())
		Expect(metadataProbeDue(llama("", false), model.NewModel("m", "127.0.0.1:1", nil))).To(BeTrue())
		Expect(metadataProbeDue(llama("", false), probed())).To(BeFalse())
	})

	It("keeps the cold-load and cached-marker rules", func() {
		Expect(metadataProbeDue(llama("<__media_x__>", false), nil)).To(BeTrue())
		Expect(metadataProbeDue(llama("<__media_x__>", false), model.NewModel("m", "127.0.0.1:1", nil))).To(BeFalse())
	})

	It("probes empty reasoning slots until they are filled, even on an instance that answered", func() {
		Expect(metadataProbeDue(llama("", true), probed())).To(BeTrue())
		filled := llama("", true)
		filled.ReasoningConfig.DisableReasoning = ptr.To(false)
		filled.ReasoningConfig.DisableReasoningTagPrefill = ptr.To(true)
		Expect(metadataProbeDue(filled, probed())).To(BeFalse())
	})
})
