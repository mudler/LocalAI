package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
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

var _ = Describe("truncateAssistantText", func() {
	It("clears the text of the assistant content part at the index", func() {
		items := []*types.MessageItemUnion{{Assistant: &types.MessageItemAssistant{
			ID:      "a1",
			Content: []types.MessageContentOutput{{Type: types.MessageContentTypeText, Text: "hello world"}},
		}}}
		ok := truncateAssistantText(items, "a1", 0)
		Expect(ok).To(BeTrue())
		Expect(items[0].Assistant.Content[0].Text).To(Equal(""))
	})

	// Realtime assistant *audio* turns store the spoken words in .Transcript, not
	// .Text, so a barge-in truncate must clear .Transcript too or it would no-op.
	It("clears the transcript of an assistant audio content part", func() {
		items := []*types.MessageItemUnion{{Assistant: &types.MessageItemAssistant{
			ID:      "a1",
			Content: []types.MessageContentOutput{{Type: types.MessageContentTypeAudio, Transcript: "hello world"}},
		}}}
		ok := truncateAssistantText(items, "a1", 0)
		Expect(ok).To(BeTrue())
		Expect(items[0].Assistant.Content[0].Transcript).To(Equal(""))
	})

	It("returns false for an unknown id", func() {
		Expect(truncateAssistantText(nil, "nope", 0)).To(BeFalse())
	})
})

var _ = Describe("compactionCut", func() {
	user := func(id string) *types.MessageItemUnion {
		return &types.MessageItemUnion{User: &types.MessageItemUser{ID: id}}
	}
	call := func(id string) *types.MessageItemUnion {
		return &types.MessageItemUnion{FunctionCall: &types.MessageItemFunctionCall{ID: id}}
	}
	out := func(id string) *types.MessageItemUnion {
		return &types.MessageItemUnion{FunctionCallOutput: &types.MessageItemFunctionCallOutput{ID: id}}
	}

	It("cuts exactly len-keep when no pairs straddle the boundary", func() {
		items := []*types.MessageItemUnion{user("1"), user("2"), user("3"), user("4")}
		Expect(compactionCut(items, 2)).To(Equal(2))
	})

	It("returns 0 when nothing to cut", func() {
		Expect(compactionCut([]*types.MessageItemUnion{user("1")}, 2)).To(Equal(0))
	})

	It("returns 0 (cuts nothing) when keep is 0 — the unlimited-window sentinel", func() {
		items := []*types.MessageItemUnion{user("1"), user("2"), user("3")}
		Expect(compactionCut(items, 0)).To(Equal(0))
	})

	It("moves the boundary so a call/output pair is not split", func() {
		// keep=2 -> naive cut=2, but items[2] is the output of items[1]'s call;
		// pull the cut right so the whole pair stays in the kept tail.
		items := []*types.MessageItemUnion{user("1"), call("c"), out("c"), user("4")}
		Expect(compactionCut(items, 2)).To(Equal(1))
	})
})

var _ = Describe("withMemory", func() {
	It("inserts a memory system message when memory is non-empty", func() {
		base := schema.Messages{{Role: "system", StringContent: "instructions"}}
		out := withMemory(base, "user is Bob; wants pizza")
		Expect(len(out)).To(Equal(2))
		Expect(out[1].Role).To(Equal("system"))
		Expect(out[1].StringContent).To(ContainSubstring("user is Bob"))
		Expect(out[1].StringContent).To(ContainSubstring("Summary of earlier conversation"))
	})

	It("is a no-op when memory is empty", func() {
		base := schema.Messages{{Role: "system", StringContent: "instructions"}}
		Expect(withMemory(base, "")).To(HaveLen(1))
	})
})

var _ = Describe("renderItemsTranscript", func() {
	It("renders user and assistant text turns", func() {
		items := []*types.MessageItemUnion{
			{User: &types.MessageItemUser{Content: []types.MessageContentInput{{Type: types.MessageContentTypeInputText, Text: "hi"}}}},
			{Assistant: &types.MessageItemAssistant{Content: []types.MessageContentOutput{{Type: types.MessageContentTypeText, Text: "hello"}}}},
		}
		out := renderItemsTranscript(items)
		Expect(out).To(ContainSubstring("user: hi"))
		Expect(out).To(ContainSubstring("assistant: hello"))
	})

	// Realtime assistant *audio* turns store the spoken words in .Transcript, not
	// .Text, so the transcript builder must emit .Transcript too or spoken turns
	// would be dropped from the summary.
	It("renders an assistant audio turn from its transcript", func() {
		items := []*types.MessageItemUnion{
			{Assistant: &types.MessageItemAssistant{Content: []types.MessageContentOutput{{Type: types.MessageContentTypeAudio, Transcript: "spoken words"}}}},
		}
		Expect(renderItemsTranscript(items)).To(ContainSubstring("assistant: spoken words"))
	})
})

var _ = Describe("buildSummaryMessages", func() {
	It("includes prior memory and the new transcript", func() {
		msgs := buildSummaryMessages("prior facts", "user: hi", 512)
		Expect(len(msgs)).To(Equal(2))
		Expect(msgs[0].Role).To(Equal("system"))
		Expect(msgs[1].StringContent).To(ContainSubstring("prior facts"))
		Expect(msgs[1].StringContent).To(ContainSubstring("user: hi"))
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
