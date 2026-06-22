package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

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

var _ = Describe("deleteItem", func() {
	mk := func(ids ...string) []*types.MessageItemUnion {
		out := make([]*types.MessageItemUnion, len(ids))
		for i, id := range ids {
			out[i] = &types.MessageItemUnion{User: &types.MessageItemUser{ID: id}}
		}
		return out
	}

	It("removes the item with the given id", func() {
		items, ok := deleteItem(mk("a", "b", "c"), "b")
		Expect(ok).To(BeTrue())
		Expect(len(items)).To(Equal(2))
		Expect(itemID(items[0])).To(Equal("a"))
		Expect(itemID(items[1])).To(Equal("c"))
	})

	It("reports not found for an unknown id", func() {
		_, ok := deleteItem(mk("a"), "zzz")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("clearInputAudio", func() {
	It("resets the pending PCM and buffered Opus frames", func() {
		s := &Session{InputAudioBuffer: []byte{1, 2, 3}, OpusFrames: [][]byte{{9}}}
		clearInputAudio(s)
		Expect(s.InputAudioBuffer).To(BeNil())
		Expect(s.OpusFrames).To(BeNil())
	})
})

var _ = Describe("itemID", func() {
	It("returns the id for each variant and empty for nil", func() {
		Expect(itemID(nil)).To(Equal(""))
		Expect(itemID(&types.MessageItemUnion{User: &types.MessageItemUser{ID: "u1"}})).To(Equal("u1"))
		Expect(itemID(&types.MessageItemUnion{Assistant: &types.MessageItemAssistant{ID: "a1"}})).To(Equal("a1"))
		Expect(itemID(&types.MessageItemUnion{System: &types.MessageItemSystem{ID: "s1"}})).To(Equal("s1"))
		Expect(itemID(&types.MessageItemUnion{FunctionCall: &types.MessageItemFunctionCall{ID: "f1"}})).To(Equal("f1"))
		Expect(itemID(&types.MessageItemUnion{FunctionCallOutput: &types.MessageItemFunctionCallOutput{ID: "o1"}})).To(Equal("o1"))
	})
})
