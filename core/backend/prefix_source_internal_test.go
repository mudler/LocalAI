package backend

import (
	"strings"

	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("messagesPrefixSource", func() {
	mk := func(role, content string) schema.Message {
		return schema.Message{Role: role, StringContent: content}
	}

	It("serializes messages head-first in turn order", func() {
		got := messagesPrefixSource(schema.Messages{
			mk("system", "You are helpful."),
			mk("user", "Hi"),
		})
		Expect(got).To(Equal("system\nYou are helpful.\nuser\nHi\n"))
	})

	It("is deterministic across calls for the same conversation", func() {
		conv := schema.Messages{mk("system", "S"), mk("user", "U")}
		Expect(messagesPrefixSource(conv)).To(Equal(messagesPrefixSource(conv)))
	})

	It("shares a leading byte prefix when the system prompt is shared", func() {
		shared := "system\nShared system prompt.\nuser\n"
		a := messagesPrefixSource(schema.Messages{mk("system", "Shared system prompt."), mk("user", "Question A")})
		b := messagesPrefixSource(schema.Messages{mk("system", "Shared system prompt."), mk("user", "Question B")})
		Expect(strings.HasPrefix(a, shared)).To(BeTrue())
		Expect(strings.HasPrefix(b, shared)).To(BeTrue())
	})

	It("does NOT share a prefix when the system prompt differs", func() {
		a := messagesPrefixSource(schema.Messages{mk("system", "Prompt A"), mk("user", "Q")})
		b := messagesPrefixSource(schema.Messages{mk("system", "Prompt B"), mk("user", "Q")})
		Expect(strings.HasPrefix(a, "system\nPrompt A")).To(BeTrue())
		Expect(strings.HasPrefix(b, "system\nPrompt B")).To(BeTrue())
	})

	It("returns empty for no messages", func() {
		Expect(messagesPrefixSource(nil)).To(Equal(""))
	})

	It("falls back to Content when StringContent is empty", func() {
		got := messagesPrefixSource(schema.Messages{{Role: "user", Content: "plain"}})
		Expect(got).To(Equal("user\nplain\n"))
	})
})
