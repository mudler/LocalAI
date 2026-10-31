package studio_test

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// saveAsCommand writes what a command would leave in the user's files: one
// change set of the chat, as the commit of a sandbox makes it.
func saveAsCommand(f *files.Service, user, chat string, write map[string]string, remove ...string) {
	ctx := context.Background()
	Expect(f.EnsureHome(ctx, user)).To(Succeed())
	a := files.UserActor(user)
	a.ChatID = chat
	tx, err := f.Store().Begin(ctx, a, "Sandbox: bash")
	Expect(err).NotTo(HaveOccurred())
	for rel, body := range write {
		abs, err := f.Abs(user, rel)
		Expect(err).NotTo(HaveOccurred())
		_, err = tx.Put(ctx, abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	for _, rel := range remove {
		abs, err := f.Abs(user, rel)
		Expect(err).NotTo(HaveOccurred())
		Expect(tx.Delete(ctx, abs)).To(Succeed())
	}
	_, err = tx.Commit()
	Expect(err).NotTo(HaveOccurred())
}

// sandboxThatWrites is the Sandbox add-on whose bash saves a document, as its
// commit through the host does.
type sandboxThatWrites struct {
	*fakeAddons
	files *files.Service
	write map[string]string
}

func (s *sandboxThatWrites) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	out, err := s.fakeAddons.Call(ctx, user, conv, name, args)
	saveAsCommand(s.files, user, conv, s.write)
	return out, err
}

// hostExecThatWrites is the built-in bash whose command saves a document.
type hostExecThatWrites struct {
	files *files.Service
	write map[string]string
}

func (h *hostExecThatWrites) Unavailable() string { return "" }

func (h *hostExecThatWrites) Tool(a workspace.Actor) session.Tool {
	return session.Tool{Name: "bash", Description: "Run on this machine.", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		Run: func(context.Context, json.RawMessage) (string, error) {
			saveAsCommand(h.files, a.Principal, a.ChatID, h.write)
			return "OK", nil
		}}
}

func cardFile(card *session.ToolCard, path string) session.ToolFile {
	for _, f := range card.Files {
		if f.Path == path {
			return f
		}
	}
	Fail("the card has no file " + path)
	return session.ToolFile{}
}

var _ = Describe("the files a command saved", func() {
	runDoc := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "make a document" {
			return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"python3 build.py"}`}, {Say: "Here it is."}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}

	It("go on the card of the Sandbox's bash step, in the chat's files and to its clients", func() {
		f := newFiles()
		sb := &sandboxThatWrites{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}, files: f,
			write: map[string]string{"/out/report.docx": "PK document", "/out/notes.txt": "notes"}}
		svc, _ := newToolService(runDoc, func(d *studio.Deps) { d.Files = f; d.AddonTools = sb })
		id := create(svc, alice)
		// The user's own file, changed by the command later, is not the command's creation.
		saveAsCommand(f, alice, "", map[string]string{"/old.txt": "before"})
		ctx := context.Background()
		evs, cancel, err := svc.Events(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()

		sent(svc, alice, id, "make a document")
		waitKind(evs, session.KindFiles)
		idle(svc, alice, id)

		card := toolOf(svc, alice, id)
		Expect(card).NotTo(BeNil())
		Expect(card.Kind).To(Equal(studio.ProviderCardKind), "the card still says who served the call")
		Expect(card.ID).To(Equal("sandbox"))
		Expect(card.Files).To(HaveLen(2), "%+v", card.Files)
		doc := cardFile(card, "/out/report.docx")
		Expect(doc.Op).To(Equal(session.ToolFileCreated))
		Expect(doc.Rev).To(BeNumerically(">", 0))

		// The file index (the pane, the chips under the answer, the preview
		// that opens by itself) lists the document as the turn's work.
		got := chatFiles(svc, alice, id)
		Expect(got).To(HaveKey("report.docx"))
		Expect(got["report.docx"].Origin).NotTo(Equal(session.FileFromUser))
		Expect(got["report.docx"].Turn).To(Equal(1))
		Expect(got["report.docx"].GuestPath).To(Equal("/workspace/home/out/report.docx"))
		Expect(got).NotTo(HaveKey("old.txt"), "a file of the user is not the chat's")
	})

	It("lists only what the call changed: a second call names its own files", func() {
		f := newFiles()
		sb := &sandboxThatWrites{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}, files: f,
			write: map[string]string{"/a.txt": "one"}}
		svc, _ := newToolService(runDoc, func(d *studio.Deps) { d.Files = f; d.AddonTools = sb })
		id := create(svc, alice)
		sent(svc, alice, id, "make a document")
		idle(svc, alice, id)
		sb.write = map[string]string{"/a.txt": "two", "/b.txt": "new"}
		sent(svc, alice, id, "make a document")
		idle(svc, alice, id)
		card := toolOf(svc, alice, id)
		Expect(card.Files).To(HaveLen(2), "%+v", card.Files)
		Expect(cardFile(card, "/a.txt").Op).To(Equal(session.ToolFileChanged))
		Expect(cardFile(card, "/b.txt").Op).To(Equal(session.ToolFileCreated))
	})

	It("go on the card of the built-in bash too, next to who allowed it", func() {
		f := newFiles()
		hx := &hostExecThatWrites{files: f, write: map[string]string{"/report.pdf": "%PDF"}}
		svc, _ := newToolService(runDoc, func(d *studio.Deps) {
			d.Files = f
			d.HostExec = hx
			d.HostExecMode = func() string { return "on" }
		})
		id := create(svc, bob)
		sent(svc, bob, id, "make a document")
		idle(svc, bob, id)
		card := toolOf(svc, bob, id)
		Expect(card).NotTo(BeNil())
		Expect(card.Kind).To(Equal(studio.HostExecCardKind))
		Expect(card.ID).To(Equal("ran"))
		Expect(cardFile(card, "/report.pdf").Op).To(Equal(session.ToolFileCreated))
		Expect(chatFiles(svc, bob, id)).To(HaveKey("report.pdf"))
	})

	It("leave the card alone when the command saved nothing", func() {
		c := newHostChat("on", nil)
		id := create(c.svc, bob)
		sent(c.svc, bob, id, "run")
		idle(c.svc, bob, id)
		card := toolOf(c.svc, bob, id)
		Expect(card.Kind).To(Equal(studio.HostExecCardKind))
		Expect(card.Files).To(BeEmpty())
	})

	It("tell the model where a command of the Sandbox starts", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		svc, h := newToolService(runDoc, func(d *studio.Deps) { d.Files = newFiles(); d.AddonTools = fa })
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		Expect(h.TurnPrompt(id)).To(ContainSubstring("It starts in /workspace/home, the user's home"))
	})
})
