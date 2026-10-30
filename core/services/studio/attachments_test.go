package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// userLines returns the user messages of a chat's transcript.
func userLines(svc *studio.Service, user, id string) []session.Entry {
	es, err := svc.Messages(context.Background(), user, id, 0, 0)
	Expect(err).NotTo(HaveOccurred())
	var out []session.Entry
	for _, e := range es {
		if e.Role == session.RoleUser {
			out = append(out, e)
		}
	}
	return out
}

// chatFolder is the one chat folder of alice, as a path inside her home.
func chatFolder(svc *studio.Service) string {
	kids, err := svc.Files().Store().List(context.Background(), workspace.Actor{Principal: alice}, session.ChatsPath(alice))
	Expect(err).NotTo(HaveOccurred())
	Expect(kids).To(HaveLen(1), "chat folders = %v", kids)
	return strings.TrimPrefix(kids[0].Path, session.HomePath(alice))
}

var _ = Describe("attachments", func() {
	It("keeps the words of a message apart from its files, and the model gets the paths as before", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "whats in this photo", []session.Upload{upload("photo.jpg", "JPEGDATA")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		folder := chatFolder(svc)
		Expect(h.Sent()[0]).To(Equal("whats in this photo\n\nThe user attached these files:\n- /workspace/home" + folder + "/photo.jpg"))
		h.Gate <- struct{}{}
		idle(svc, alice, id)

		lines := userLines(svc, alice, id)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(Equal("whats in this photo"))
		Expect(lines[0].Attachments).To(HaveLen(1))
		a := lines[0].Attachments[0]
		Expect(a.Path).To(Equal(folder + "/photo.jpg"))
		Expect(a.Name).To(Equal("photo.jpg"))
		Expect(a.Size).To(Equal(int64(len("JPEGDATA"))))
		Expect(a.Mime).To(Equal("image/jpeg"))
		Expect(a.Version).To(BeNumerically(">", 0))
	})

	It("keeps a message with only files", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "", []session.Upload{upload("a.png", "A"), upload("b.txt", "B")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		Expect(h.Sent()[0]).To(HavePrefix("The user attached these files:\n- /workspace/home/Chats/"))
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		lines := userLines(svc, alice, id)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(BeEmpty())
		Expect(lines[0].Attachments).To(HaveLen(2))
	})

	It("a waiting message with files reaches the transcript with its files apart", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		_, err := svc.SendWithFiles(context.Background(), alice, id, "and this", []session.Upload{upload("x.txt", "extra")})
		Expect(err).NotTo(HaveOccurred())
		h.Gate <- struct{}{}
		until("the waiting message to reach the model", func() bool { return len(h.Delivered()) == 1 })
		Expect(h.Delivered()[0]).To(Equal([]string{"and this\n\nThe user attached these files:\n- /workspace/home" + chatFolder(svc) + "/x.txt"}))
		idle(svc, alice, id)
		lines := userLines(svc, alice, id)
		Expect(lines).To(HaveLen(2))
		Expect(lines[1].Text).To(Equal("and this"))
		Expect(lines[1].Attachments).To(HaveLen(1))
		Expect(lines[1].Attachments[0].Name).To(Equal("x.txt"))
	})

	It("an edit of a waiting message keeps its files", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		res, err := svc.SendWithFiles(context.Background(), alice, id, "and this", []session.Upload{upload("x.txt", "extra")})
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.EditPending(context.Background(), alice, id, res.PendingID, "this instead")).To(Succeed())
		got := pendingTexts(svc, alice, id)
		Expect(got).To(HaveLen(1))
		Expect(got[0]).To(Equal("this instead\n\nThe user attached these files:\n- /workspace/home" + chatFolder(svc) + "/x.txt"))
		h.Gate <- struct{}{}
		idle(svc, alice, id)
	})

	It("regenerate and edit send the files of the message again", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "whats in this photo", []session.Upload{upload("photo.jpg", "JPEGDATA")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		first := h.Sent()[0]

		_, err = svc.Regenerate(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start again", func() bool { return len(h.Sent()) == 2 })
		Expect(h.Sent()[1]).To(Equal(first))
		h.Gate <- struct{}{}
		idle(svc, alice, id)

		lines := userLines(svc, alice, id)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(Equal("whats in this photo"))
		Expect(lines[0].Attachments).To(HaveLen(1))

		_, err = svc.EditAndRerun(context.Background(), alice, id, lines[0].Seq, "and the colours?")
		Expect(err).NotTo(HaveOccurred())
		until("the edited turn to start", func() bool { return len(h.Sent()) == 3 })
		Expect(h.Sent()[2]).To(Equal(strings.Replace(first, "whats in this photo", "and the colours?", 1)))
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		lines = userLines(svc, alice, id)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(Equal("and the colours?"))
		Expect(lines[0].Attachments).To(HaveLen(1))
	})

	It("an export holds the words and the files apart, and an import brings both back", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, newFiles())
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "whats in this photo", []session.Upload{upload("photo.jpg", "JPEGDATA")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)

		exp, err := svc.Export(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(exp.Messages[0].Text).To(Equal("whats in this photo"))
		Expect(exp.Messages[0].Attachments).To(HaveLen(1))
		Expect(exp.Messages[0].Attachments[0].Name).To(Equal("photo.jpg"))
		// The model's answer of the test harness repeats what it got; the
		// person's part is what matters.
		md, _, _ := strings.Cut(exp.Markdown(), "## Assistant")
		Expect(md).To(ContainSubstring("## You\n\nwhats in this photo\n\nAttached:\n- photo.jpg (8 B)\n"))
		Expect(md).NotTo(ContainSubstring("The user attached these files"))
		Expect(md).NotTo(ContainSubstring("/workspace/home"))

		raw, err := json.Marshal(exp)
		Expect(err).NotTo(HaveOccurred())
		var back studio.ChatExport
		Expect(json.Unmarshal(raw, &back)).To(Succeed())
		rec, err := svc.ImportExport(context.Background(), alice, back, "")
		Expect(err).NotTo(HaveOccurred())
		lines := userLines(svc, alice, rec.ID)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(Equal("whats in this photo"))
		Expect(lines[0].Attachments).To(Equal(userLines(svc, alice, id)[0].Attachments))
	})

	It("an old export with the list in the text imports with the files apart", func() {
		svc := newServiceWithFiles(studiotest.NewGateHarness(), newFiles())
		rec, err := svc.ImportExport(context.Background(), alice, studio.ChatExport{
			Version: studio.ExportVersion, Title: "old", Model: "m",
			Messages: []studio.ChatExportMessage{{Role: session.RoleUser, Text: "see\n\nThe user attached these files:\n- /workspace/home/Chats/x/a.txt"}},
		}, "")
		Expect(err).NotTo(HaveOccurred())
		lines := userLines(svc, alice, rec.ID)
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Text).To(Equal("see"))
		Expect(lines[0].Attachments).To(HaveLen(1))
		Expect(lines[0].Attachments[0].Path).To(Equal("/Chats/x/a.txt"))
	})

	It("search does not find the list of files of an old message", func() {
		svc := newServiceWithFiles(studiotest.NewGateHarness(), newFiles())
		_, err := svc.ImportChat(context.Background(), alice, "old", "m", time.Time{}, time.Time{}, []studio.ImportedEntry{
			{Role: session.RoleUser, Text: "see this\n\nThe user attached these files:\n- /workspace/home/Chats/x/a.txt"},
		})
		Expect(err).NotTo(HaveOccurred())
		hits, err := svc.Search(context.Background(), alice, "attached these files", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(BeEmpty())
		hits, err = svc.Search(context.Background(), alice, "see this", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Snippet).To(Equal("see this"))
	})
})
