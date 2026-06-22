package openai

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

func TestRealtimeCompaction(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Realtime Compaction Suite")
}

var _ = Describe("resolveCompaction", func() {
	It("disables when the block is absent", func() {
		enabled, _, _, _ := resolveCompaction(&config.ModelConfig{}, 6)
		Expect(enabled).To(BeFalse())
	})

	It("defaults trigger to 2x max history and tokens to 512", func() {
		cfg := &config.ModelConfig{Pipeline: config.Pipeline{Compaction: &config.PipelineCompaction{Enabled: true}}}
		enabled, trigger, maxTok, _ := resolveCompaction(cfg, 6)
		Expect(enabled).To(BeTrue())
		Expect(trigger).To(Equal(12))
		Expect(maxTok).To(Equal(512))
	})

	It("clamps trigger to max history + 1 when misconfigured", func() {
		cfg := &config.ModelConfig{Pipeline: config.Pipeline{Compaction: &config.PipelineCompaction{Enabled: true, TriggerItems: 4}}}
		_, trigger, _, _ := resolveCompaction(cfg, 6)
		Expect(trigger).To(Equal(7))
	})

	It("honors explicit values", func() {
		cfg := &config.ModelConfig{Pipeline: config.Pipeline{Compaction: &config.PipelineCompaction{
			Enabled: true, TriggerItems: 20, MaxSummaryTokens: 256, SummaryModel: "tiny"}}}
		enabled, trigger, maxTok, model := resolveCompaction(cfg, 6)
		Expect(enabled).To(BeTrue())
		Expect(trigger).To(Equal(20))
		Expect(maxTok).To(Equal(256))
		Expect(model).To(Equal("tiny"))
	})
})
