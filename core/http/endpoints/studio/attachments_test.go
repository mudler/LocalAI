package studio

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("attachments in the chats API", func() {
	It("a user line gives the words and the files apart", func() {
		list := messageList("alice", []session.Entry{{
			Seq: 1, Role: session.RoleUser, Text: "whats in this photo",
			Attachments: []session.Attachment{{Path: "/Chats/c/photo.jpg", Name: "photo.jpg", Size: 8, Mime: "image/jpeg", Version: 2, Missing: true}},
		}})
		Expect(list.Messages).To(HaveLen(1))
		m := list.Messages[0]
		Expect(m.Text).To(Equal("whats in this photo"))
		Expect(m.Attachments).To(Equal([]schema.ChatsAttachment{{Path: "/Chats/c/photo.jpg", Name: "photo.jpg", Size: 8, Mime: "image/jpeg", Version: 2, Missing: true}}))
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"attachments":[{"path":"/Chats/c/photo.jpg","name":"photo.jpg","size":8,"mime":"image/jpeg","version":2,"missing":true}]`))
	})

	It("an old user line with the list in its text gives the words and the files apart", func() {
		list := messageList("alice", []session.Entry{{
			Seq: 1, Role: session.RoleUser,
			Text: "whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_5985526360434217157_y.jpg",
		}})
		m := list.Messages[0]
		Expect(m.Text).To(Equal("whats in this photo"))
		Expect(m.Attachments).To(HaveLen(1))
		Expect(m.Attachments[0].Path).To(Equal("/Chats/2026-10-30 ci sei?/photo_5985526360434217157_y.jpg"))
		Expect(m.Attachments[0].Name).To(Equal("photo_5985526360434217157_y.jpg"))
		Expect(m.Attachments[0].Mime).To(Equal("image/jpeg"))
	})

	It("a line without files has no attachments in its JSON", func() {
		list := messageList("alice", []session.Entry{{Seq: 1, Role: session.RoleAssistant, Text: "hi"}})
		raw, err := json.Marshal(list.Messages[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("attachments"))
	})

	It("a waiting message gives the words and the files apart", func() {
		p := pendingOf(store.Pending{ID: "p1", Seq: 3, Text: "and this\n\nThe user attached these files:\n- /workspace/home/Chats/c/x.txt"})
		Expect(p.ID).To(Equal("p1"))
		Expect(p.Seq).To(Equal(int64(3)))
		Expect(p.Text).To(Equal("and this"))
		Expect(p.Attachments).To(HaveLen(1))
		Expect(p.Attachments[0].Path).To(Equal("/Chats/c/x.txt"))
	})
})
