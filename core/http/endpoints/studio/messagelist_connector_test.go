package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

var _ = Describe("the transcript of a connector chat", func() {
	It("names the sender, lists the thread lines and carries the host notes, and reads a plain line as before", func() {
		block := "heading\n<thread-messages>\nAda: first\nAssistant (you): second\n</thread-messages>\nOlder messages of the thread are not shown."
		out := messageList("alice", []session.Entry{
			{Seq: 1, Role: session.RoleThread, Text: block, Rev: 2, Note: session.ThreadCut},
			{Seq: 2, Role: session.RoleUser, Text: "hi", Sender: &session.Sender{ID: "U1", Name: "Ada", Standing: session.StandingChannel}},
			{Seq: 3, Role: session.RoleHostNote, Note: session.HostNoteTooLong, Text: "That was long."},
			{Seq: 4, Role: session.RoleUser, Text: "an older line"},
		})
		Expect(out.Messages).To(HaveLen(4))
		th := out.Messages[0]
		Expect(th.Text).To(BeEmpty(), "the block is not a message text")
		Expect(th.Thread.Lines).To(Equal([]string{"Ada: first", "Assistant (you): second"}))
		Expect(th.Thread.Cut).To(BeTrue())
		Expect(out.Messages[1].Sender.Name).To(Equal("Ada"))
		Expect(out.Messages[1].Sender.Standing).To(Equal("channel"))
		Expect(out.Messages[2].HostNote.Code).To(Equal("too_long"))
		Expect(out.Messages[2].Text).To(Equal("That was long."))
		Expect(out.Messages[3].Sender).To(BeNil())
	})
})
