package openresponses

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression for mudler/LocalAI#10039. convertORInputToMessages must populate
// both Content and StringContent: the templating fallback path reads
// StringContent, while the UseTokenizerTemplate path serialises Content via
// Messages.ToProto(). Leaving Content nil produced an empty prompt on any model
// without a Go-side template.chat_message block (the default for imported GGUFs).
var _ = Describe("convertORInputToMessages", func() {
	cfg := &config.ModelConfig{}

	It("populates both Content and StringContent for plain string input", func() {
		msgs, err := convertORInputToMessages("Hello", cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs[0].StringContent).To(Equal("Hello"))
		Expect(msgs[0].Content).To(Equal("Hello"))
	})

	It("accepts a bare {role, content} item without a type discriminator", func() {
		// The OpenAI Python SDK helper client.responses.create(input=[{...}])
		// sends message items with no "type" field. They must not be dropped.
		input := []any{
			map[string]any{"role": "user", "content": "Hi there"},
		}
		msgs, err := convertORInputToMessages(input, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Role).To(Equal("user"))
		Expect(msgs[0].StringContent).To(Equal("Hi there"))
		Expect(msgs[0].Content).To(Equal("Hi there"))
	})

	It("still populates both fields for an explicit type:message item", func() {
		input := []any{
			map[string]any{"type": "message", "role": "user", "content": "Typed"},
		}
		msgs, err := convertORInputToMessages(input, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].StringContent).To(Equal("Typed"))
		Expect(msgs[0].Content).To(Equal("Typed"))
	})

	It("does not treat a non-message item (no content key) as a message", func() {
		// An item with neither a known type nor a {role, content} shape must
		// keep falling through unchanged — no behaviour change for such inputs.
		input := []any{
			map[string]any{"role": "user"},
		}
		msgs, err := convertORInputToMessages(input, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(msgs).To(BeEmpty())
	})
})
