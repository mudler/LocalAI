package studio_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the files of a chat and the prompt prefix", func() {
	// The system prompt comes before the whole conversation. When it changes,
	// a backend with a prompt cache must read the whole conversation again.
	// The files of a chat change at almost every turn that writes one, so
	// their list reaches the model with the next message instead, only when
	// it changed.
	It("keeps the system prompt when a tool writes a file and names the files once in the next message", func() {
		svc, h := newFilesChat()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		before := h.SystemPrompt(id) + "\n" + h.TurnPrompt(id)

		sent(svc, alice, id, "write")
		idle(svc, alice, id)
		sent(svc, alice, id, "again")
		idle(svc, alice, id)
		Expect(h.SystemPrompt(id) + "\n" + h.TurnPrompt(id)).To(Equal(before), "the system prompt changed after a file was written")

		msgs := h.SentRaw(id)
		last := msgs[len(msgs)-1]
		Expect(last).To(ContainSubstring("Files in this conversation"), "the message after the write does not name the files:\n%s", last)
		Expect(last).To(ContainSubstring("- out.md (5 B, text/markdown) created by you"))

		sent(svc, alice, id, "third")
		idle(svc, alice, id)
		msgs = h.SentRaw(id)
		Expect(msgs[len(msgs)-1]).NotTo(ContainSubstring("Files in this conversation"), "an unchanged list is sent again")
		Expect(strings.Count(strings.Join(msgs, "\n"), "Files in this conversation")).To(Equal(1))
	})
})
