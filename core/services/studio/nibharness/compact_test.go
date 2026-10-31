package nibharness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

type resumeDoc struct {
	History []openai.ChatCompletionMessage `json:"history"`
	Context []openai.ChatCompletionMessage `json:"context"`
}

var _ = Describe("compaction on request", func() {
	It("summarises the older messages and the exported state is the compacted one", func() {
		llm := &recordingLLM{}
		hs := start(llm, session.HarnessEnv{})
		for i := range 8 {
			send(hs, fmt.Sprintf("message %d %s", i, strings.Repeat("words ", 40)))
		}
		comp, ok := hs.(session.Compactor)
		Expect(ok).To(BeTrue(), "the chat session compacts on request")
		before, after, err := comp.Compact(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(BeNumerically("<", before), "before %d after %d", before, after)

		raw, err := hs.Export()
		Expect(err).NotTo(HaveOccurred())
		var st resumeDoc
		Expect(json.Unmarshal(raw, &st)).To(Succeed())
		Expect(len(st.Context)).To(BeNumerically("<", 16), "the context holds a summary and the last messages")
		var all strings.Builder
		for _, m := range st.Context {
			all.WriteString(m.Content)
		}
		Expect(all.String()).NotTo(ContainSubstring("message 0 "), "the oldest message is in the summary, not verbatim")
		Expect(all.String()).To(ContainSubstring("message 7 "), "the last messages stay")
	})

	It("finds nothing to compact in a short conversation", func() {
		llm := &recordingLLM{}
		hs := start(llm, session.HarnessEnv{})
		send(hs, "hi")
		before, after, err := hs.(session.Compactor).Compact(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})
})

var _ = Describe("TranscriptState", func() {
	It("starts after the last cleared marker and skips the markers", func() {
		entries := []session.Entry{
			{Role: session.RoleUser, Text: "old question"},
			{Role: session.RoleAssistant, Text: "old answer"},
			{Role: session.RoleCleared},
			{Role: session.RoleUser, Text: "new question"},
			{Role: session.RoleCompacted, Text: session.CompactedText(10, 5)},
			{Role: session.RoleAssistant, Text: "new answer"},
		}
		raw, err := nibharness.TranscriptState(entries)
		Expect(err).NotTo(HaveOccurred())
		var st resumeDoc
		Expect(json.Unmarshal(raw, &st)).To(Succeed())
		Expect(st.History).To(HaveLen(2))
		Expect(st.History[0].Content).To(Equal("new question"))
		Expect(st.History[1].Content).To(Equal("new answer"))

		raw, err = nibharness.TranscriptState(entries[:3])
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(BeNil(), "nothing after the marker: no state")
	})
})
