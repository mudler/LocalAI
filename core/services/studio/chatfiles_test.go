package studio_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pngBytes is the start of a PNG file: enough for the type sniffer.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func newFilesChat() (*studio.Service, *studiotest.ToolHarness) {
	f := newFiles()
	return newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "write" {
			return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/notes/out.md","content":"# out"}`}, {Say: "written"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) {
		d.Files = f
		d.Modalities = func(string) studio.Modalities { return studio.Modalities{Vision: true} }
	})
}

func chatFiles(svc *studio.Service, user, id string) map[string]studio.ChatFile {
	cf, err := svc.ChatFiles(context.Background(), user, id)
	Expect(err).NotTo(HaveOccurred())
	out := map[string]studio.ChatFile{}
	for _, f := range cf.Files {
		out[f.Name] = f
	}
	return out
}

// waitKind reads events until one of kind k arrives.
func waitKind(ch <-chan session.Event, k session.EventKind) {
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == k {
				return
			}
		case <-timeout:
			Fail(fmt.Sprintf("no %s event", k))
		}
	}
}

var _ = Describe("chatfiles", func() {
	It("chat files list uploads and tool writes and reach the prompt", func() {
		svc, h := newFilesChat()
		ctx := context.Background()
		id := create(svc, alice)
		evs, cancel, err := svc.Events(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()

		up := []session.Upload{
			{Name: "pic.png", Body: bytes.NewReader(pngBytes)},
			{Name: "data.csv", Body: strings.NewReader("a,b\n1,2\n")},
		}
		_, err = svc.SendWithFiles(ctx, alice, id, "look", up)
		Expect(err).NotTo(HaveOccurred())
		waitKind(evs, session.KindFiles)
		idle(svc, alice, id)

		files := chatFiles(svc, alice, id)
		f := files["pic.png"]
		Expect(f.Origin).To(Equal(session.FileFromUser), "pic.png = %+v, want an upload sent as media", f)
		Expect(f.Media).To(BeTrue(), "pic.png = %+v, want an upload sent as media", f)
		f = files["data.csv"]
		Expect(f.Origin).To(Equal(session.FileFromUser), "data.csv = %+v, want an upload that is not media", f)
		Expect(f.Media).To(BeFalse(), "data.csv = %+v, want an upload that is not media", f)
		prompt := h.TurnPrompt(id)
		Expect(prompt).To(ContainSubstring("Files in this conversation"), "the turn prompt does not list the uploads:\n%s", prompt)
		Expect(prompt).To(ContainSubstring("\n- data.csv (8 B, text/csv) uploaded by the user, turn 1"), "the turn prompt does not list the uploads:\n%s", prompt)

		sent(svc, alice, id, "write")
		waitKind(evs, session.KindFiles)
		idle(svc, alice, id)
		files = chatFiles(svc, alice, id)
		f = files["out.md"]
		Expect(f.Origin).To(Equal(session.FileFromTool), "out.md = %+v, want a file created by a tool", f)
		Expect(f.Changed).To(BeFalse(), "out.md = %+v, want a file created by a tool", f)
		Expect(f.GuestPath).To(Equal("/workspace/home/notes/out.md"), "out.md = %+v, want a file created by a tool", f)
		sent(svc, alice, id, "again")
		idle(svc, alice, id)
		Expect(h.TurnPrompt(id)).To(ContainSubstring("/workspace/home/notes/\n- out.md (5 B, text/markdown) created by you"))

		_, err = svc.ChatFiles(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound), "bob lists alice's chat files: %v", err)
	})

	It("chat files follow a rename and tell the model", func() {
		svc, h := newFilesChat()
		ctx := context.Background()
		id := create(svc, alice)
		_, err := svc.SendWithFiles(ctx, alice, id, "first", []session.Upload{{Name: "a.txt", Body: strings.NewReader("a")}})
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		old := chatFiles(svc, alice, id)["a.txt"].GuestPath

		evs, cancel, err := svc.Events(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		title := "Quarterly numbers"
		_, err = svc.Update(ctx, alice, id, studio.UpdateInput{Title: &title})
		Expect(err).NotTo(HaveOccurred())
		waitKind(evs, session.KindFiles)
		cur := chatFiles(svc, alice, id)["a.txt"].GuestPath
		Expect(cur).NotTo(Equal(old), "path after the rename = %q (was %q)", cur, old)
		Expect(cur).To(ContainSubstring("Quarterly numbers/a.txt"), "path after the rename = %q (was %q)", cur, old)
		sent(svc, alice, id, "next")
		idle(svc, alice, id)
		p := h.TurnPrompt(id)
		Expect(p).To(ContainSubstring(strings.TrimSuffix(cur, "a.txt")+"\n- a.txt ("), "the prompt after the rename lacks the new path or the note:\n%s", p)
		Expect(p).To(ContainSubstring("was renamed to"), "the prompt after the rename lacks the new path or the note:\n%s", p)
	})

	It("chat files are empty and the prompt has no block without files", func() {
		svc, h := newFilesChat()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		Expect(chatFiles(svc, alice, id)).To(BeEmpty())
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("Files in this conversation"))
	})

	It("a fork lists the files its messages attached", func() {
		svc, _ := newFilesChat()
		ctx := context.Background()
		id := create(svc, alice)
		_, err := svc.SendWithFiles(ctx, alice, id, "first", []session.Upload{{Name: "src.txt", Body: strings.NewReader("s")}})
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		fork, err := svc.Fork(ctx, alice, id, 0, "")
		Expect(err).NotTo(HaveOccurred())
		cf, err := svc.ChatFiles(ctx, alice, fork.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cf.Files).To(HaveLen(1), "fork files = %+v, want the source chat's attachment and no folder", cf)
		Expect(cf.Files[0].Name).To(Equal("src.txt"), "fork files = %+v, want the source chat's attachment and no folder", cf)
		Expect(cf.Files[0].Origin).To(Equal(session.FileFromUser), "fork files = %+v, want the source chat's attachment and no folder", cf)
		Expect(cf.Folder).To(Equal(""), "fork files = %+v, want the source chat's attachment and no folder", cf)
		Expect(cf.Files[0].GuestPath).To(Equal(chatFiles(svc, alice, id)["src.txt"].GuestPath), "the fork names another path than the source: %q", cf.Files[0].GuestPath)
	})
})
