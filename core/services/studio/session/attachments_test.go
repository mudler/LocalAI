package session_test

import (
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("attachments", func() {
	photo := session.Attachment{Path: "/Chats/2026-10-30 ci sei?/photo_1_y.jpg", Name: "photo_1_y.jpg", Size: 2048, Mime: "image/jpeg", Version: 1}
	notes := session.Attachment{Path: "/Chats/2026-10-30 ci sei?/notes.txt", Name: "notes.txt", Size: 10, Mime: "text/plain", Version: 1}

	It("gives the model the user's text and the guest paths, as before", func() {
		got := session.ComposeMessage("whats in this photo", "", []session.Attachment{photo})
		Expect(got).To(Equal("whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_1_y.jpg"))
	})

	It("gives the model only the list when there is no text", func() {
		got := session.ComposeMessage("", "", []session.Attachment{photo, notes})
		Expect(got).To(Equal("The user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_1_y.jpg\n- /workspace/home/Chats/2026-10-30 ci sei?/notes.txt"))
	})

	It("puts the text read from the files between the words and the list", func() {
		note := "\n\n[Voice note memo.m4a, transcribed: buy milk]"
		got := session.ComposeMessage("my day", note, []session.Attachment{{Path: "/Chats/x/memo.m4a", Name: "memo.m4a"}})
		Expect(got).To(Equal("my day\n\n[Voice note memo.m4a, transcribed: buy milk]\n\nThe user attached these files:\n- /workspace/home/Chats/x/memo.m4a"))
	})

	It("leaves a message without files alone", func() {
		Expect(session.ComposeMessage("hello", "", nil)).To(Equal("hello"))
	})

	It("splits what ComposeMessage wrote back into the words, the note and the files", func() {
		note := "\n\n[Text of report.pdf:\nQuarter one]"
		atts := []session.Attachment{photo, {Path: "/Chats/2026-10-30 ci sei?/report.pdf", Name: "report.pdf"}}
		body, gotNote, got := session.SplitMessage(session.ComposeMessage("compare these", note, atts))
		Expect(body).To(Equal("compare these"))
		Expect(gotNote).To(Equal(note))
		Expect(got).To(HaveLen(2))
		Expect(got[0].Path).To(Equal(photo.Path))
		Expect(got[0].Name).To(Equal("photo_1_y.jpg"))
		Expect(got[0].Mime).To(Equal("image/jpeg"))
		Expect(got[1].Name).To(Equal("report.pdf"))
		Expect(got[1].Mime).To(Equal("application/pdf"))
	})

	It("splits a message that has only files", func() {
		body, note, got := session.SplitMessage("The user attached these files:\n- /workspace/home/Chats/a/b.png")
		Expect(body).To(Equal(""))
		Expect(note).To(Equal(""))
		Expect(got).To(HaveLen(1))
		Expect(got[0].Path).To(Equal("/Chats/a/b.png"))
	})

	It("splits a voice note without words", func() {
		text := session.ComposeMessage("", "\n\n[Voice note memo.m4a, transcribed: hi]", []session.Attachment{{Path: "/Chats/x/memo.m4a", Name: "memo.m4a"}})
		body, note, got := session.SplitMessage(text)
		Expect(body).To(Equal(""))
		Expect(note).To(Equal("\n\n[Voice note memo.m4a, transcribed: hi]"))
		Expect(got).To(HaveLen(1))
		Expect(session.ComposeMessage(body, note, got)).To(Equal(text), "the model text changed after a split and a compose")
	})

	It("splits waiting messages joined into one, each with its files", func() {
		joined := "first\n\nThe user attached these files:\n- /workspace/home/Chats/a/one.txt\n\nsecond\n\n[Document scan.pdf has no text layer. It may be a scan.]\n\nThe user attached these files:\n- /workspace/home/Chats/a/scan.pdf\n\nthird"
		body, note, got := session.SplitMessage(joined)
		Expect(body).To(Equal("first\n\nsecond\n\nthird"))
		Expect(note).To(Equal("\n\n[Document scan.pdf has no text layer. It may be a scan.]"))
		Expect(got).To(HaveLen(2))
		Expect(got[0].Name).To(Equal("one.txt"))
		Expect(got[1].Name).To(Equal("scan.pdf"))
	})

	It("keeps text that only looks like the list", func() {
		for _, text := range []string{
			"The user attached these files:",
			"The user attached these files:\n- not a path",
			"say The user attached these files:\n- /workspace/home/x",
		} {
			body, note, got := session.SplitMessage(text)
			Expect(body).To(Equal(text))
			Expect(note).To(BeEmpty())
			Expect(got).To(BeEmpty())
		}
	})

	It("shows the user's words of an old message that holds the list", func() {
		e := session.Entry{Role: session.RoleUser, Text: "whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_1_y.jpg"}
		d := session.DisplayEntry(e)
		Expect(d.Text).To(Equal("whats in this photo"))
		Expect(d.Attachments).To(HaveLen(1))
		Expect(d.Attachments[0].Name).To(Equal("photo_1_y.jpg"))
		Expect(session.ModelText(d)).To(Equal(e.Text), "the model text of an old message changed")
		Expect(session.ModelText(e)).To(Equal(e.Text))
	})

	It("leaves the other lines alone", func() {
		e := session.Entry{Role: session.RoleAssistant, Text: "The user attached these files:\n- /workspace/home/a"}
		Expect(session.DisplayEntry(e)).To(Equal(e))
		Expect(session.ModelText(e)).To(Equal(e.Text))
	})

	It("gives the model the list of a message stored with its files apart", func() {
		e := session.Entry{Role: session.RoleUser, Text: "whats in this photo", Attachments: []session.Attachment{photo}}
		Expect(session.ModelText(e)).To(Equal("whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_1_y.jpg"))
		Expect(session.DisplayEntry(e)).To(Equal(e))
	})
})
