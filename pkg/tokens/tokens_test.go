// SPDX-License-Identifier: MIT

package tokens_test

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/tokens"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Token counting", func() {
	Describe("EncodingFor", func() {
		It("uses tiktoken's exact model mapping", func() {
			Expect(tokens.EncodingFor("text-davinci-003")).To(Equal("p50k_base"))
		})

		It("uses tiktoken's model prefix mapping", func() {
			Expect(tokens.EncodingFor("gpt-4o-2024-08-06")).To(Equal("o200k_base"))
		})

		It("falls back conservatively for unknown models", func() {
			Expect(tokens.EncodingFor("local-model")).To(Equal("cl100k_base"))
		})
	})

	Describe("CountText", func() {
		It("returns the exact encoded token count", func() {
			count, err := tokens.CountText("hello world", "gpt-4")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(2))
		})

		It("treats literal special-token strings as ordinary text", func() {
			var count int
			Expect(func() {
				var err error
				count, err = tokens.CountText("<|endoftext|>", "gpt-4")
				Expect(err).NotTo(HaveOccurred())
			}).NotTo(Panic())
			Expect(count).To(BeNumerically(">", 0))
		})
	})

	Describe("Count", func() {
		It("sums string message content", func() {
			count, err := tokens.Count([]schema.Message{
				{Content: "hello"},
				{Content: "world"},
			}, "gpt-4")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(2))
		})

		It("extracts text from multimodal content and ignores recognized media", func() {
			count, err := tokens.Count([]schema.Message{{
				Content: []any{
					map[string]any{"type": "text", "text": "hello world"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "image.png"}},
				},
			}}, "gpt-4")
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(2))
		})

		DescribeTable("rejects unsupported non-empty content",
			func(content any) {
				_, err := tokens.Count([]schema.Message{{Content: content}}, "gpt-4")
				Expect(err).To(MatchError(ContainSubstring("unsupported")))
			},
			Entry("top-level object", map[string]any{"text": "hello"}),
			Entry("arbitrary multimodal map", []any{map[string]any{"foo": "bar"}}),
			Entry("malformed text part", []any{map[string]any{"type": "text", "text": 42}}),
			Entry("unknown typed part", []any{map[string]any{"type": "custom", "custom": "value"}}),
			Entry("non-object multimodal part", []any{"hello"}),
		)
	})
})
