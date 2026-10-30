package studio_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newFiles() *files.Service {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	return files.New(ws)
}

func newServiceWithFiles(h *studiotest.GateHarness, f *files.Service) *studio.Service {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Files: f})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

var _ = Describe("files", func() {
	It("a chat gives its user a home folder when files are on", func() {
		f := newFiles()
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, f)
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)

		_, err := f.Store().Stat(context.Background(), workspace.Actor{Principal: alice}, session.HomePath(alice))
		Expect(err).NotTo(HaveOccurred(), "the user's home does not exist after a chat")
		_, err = f.Store().Stat(context.Background(), workspace.Actor{Principal: "bob"}, session.HomePath(alice))
		Expect(err).To(MatchError(workspace.ErrNotFound), "another user sees the home: %v", err)
	})

	It("a chat mounts the folders shared with its owner, with the mode of the grant", func() {
		ctx := context.Background()
		f := newFiles()
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, f)
		team, err := svc.SharedFolders().Create(ctx, "", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SharedFolders().SetMember(ctx, "", team.ID, files.EveryoneGroup, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		env := h.Env()
		Expect(env.Actor.Groups).To(ContainElement(files.EveryoneGroup))
		Expect(env.SystemPrompt).To(ContainSubstring("/workspace/shared-Team: a folder shared with the user (read only)"))
	})

	It("a chat works without files", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, files.Disabled("off for this test"))
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
		Expect(svc.Files().Available()).To(BeFalse(), "the service reports files as available")
	})
})

func upload(name, body string) session.Upload {
	return session.Upload{Name: name, Body: strings.NewReader(body)}
}

var _ = Describe("files", func() {
	It("an attached file lands in the chat folder and the model gets its path", func() {
		f := newFiles()
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, f)
		id := create(svc, alice)
		res, err := svc.SendWithFiles(context.Background(), alice, id, "summarize this", []session.Upload{upload("notes.txt", "alpha beta")})
		Expect(err).NotTo(HaveOccurred(), "SendWithFiles = %+v, %v", res, err)
		Expect(res.Started).To(BeTrue(), "SendWithFiles = %+v, %v", res, err)
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		sent := h.Sent()[0]
		Expect(sent).To(ContainSubstring("summarize this"), "the model got %q, want the text and the guest path of notes.txt", sent)
		Expect(sent).To(ContainSubstring("/workspace/home/Chats/"), "the model got %q, want the text and the guest path of notes.txt", sent)
		Expect(sent).To(ContainSubstring("notes.txt"), "the model got %q, want the text and the guest path of notes.txt", sent)
		// The file is in the user's chat folder, as the user.
		kids, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, session.ChatsPath(alice))
		Expect(err).NotTo(HaveOccurred(), "chat folders = %v, %v, want one", kids, err)
		Expect(kids).To(HaveLen(1), "chat folders = %v, %v, want one", kids, err)
		inner, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, kids[0].Path)
		Expect(err).NotTo(HaveOccurred(), "chat folder holds %v, %v, want notes.txt", inner, err)
		Expect(inner).To(HaveLen(1), "chat folder holds %v, %v, want notes.txt", inner, err)
		Expect(inner[0].Name).To(Equal("notes.txt"), "chat folder holds %v, %v, want notes.txt", inner, err)
		h.Gate <- struct{}{}
		idle(svc, alice, id)
	})

	It("a file sent while a turn runs is saved and queued with its path", func() {
		f := newFiles()
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, f)
		id := create(svc, alice)
		sent(svc, alice, id, "start")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		res, err := svc.SendWithFiles(context.Background(), alice, id, "and this", []session.Upload{upload("x.txt", "extra")})
		Expect(err).NotTo(HaveOccurred(), "SendWithFiles during a turn = %+v, %v; want a waiting message", res, err)
		Expect(res.Started).To(BeFalse(), "SendWithFiles during a turn = %+v, %v; want a waiting message", res, err)
		Expect(res.PendingID).NotTo(Equal(""), "SendWithFiles during a turn = %+v, %v; want a waiting message", res, err)
		got := pendingTexts(svc, alice, id)
		Expect(got).To(HaveLen(1), "waiting messages = %q, want the text and the guest path of x.txt", got)
		Expect(got[0]).To(ContainSubstring("and this"), "waiting messages = %q, want the text and the guest path of x.txt", got)
		Expect(got[0]).To(ContainSubstring("/workspace/home/Chats/"), "waiting messages = %q, want the text and the guest path of x.txt", got)
		Expect(got[0]).To(ContainSubstring("x.txt"), "waiting messages = %q, want the text and the guest path of x.txt", got)
		// The file is saved already, in the chat folder, as the user.
		kids, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, session.ChatsPath(alice))
		Expect(err).NotTo(HaveOccurred(), "chat folders = %v, %v, want one", kids, err)
		Expect(kids).To(HaveLen(1), "chat folders = %v, %v, want one", kids, err)
		inner, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, kids[0].Path)
		Expect(err).NotTo(HaveOccurred(), "chat folder holds %v, %v, want x.txt", inner, err)
		Expect(inner).To(HaveLen(1), "chat folder holds %v, %v, want x.txt", inner, err)
		Expect(inner[0].Name).To(Equal("x.txt"), "chat folder holds %v, %v, want x.txt", inner, err)
		h.Gate <- struct{}{}
		idle(svc, alice, id)
	})

	It("a file cannot be attached when files are off", func() {
		h := studiotest.NewGateHarness()
		svc := newServiceWithFiles(h, files.Disabled("no data path"))
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "see", []session.Upload{upload("x.txt", "x")})
		Expect(err).To(MatchError(files.ErrUnavailable), "SendWithFiles with files off = %v, want ErrUnavailable", err)
		Expect(h.Sent()).To(BeEmpty(), "a turn started: %v", h.Sent())
	})

	It("the model and its sub agents get the file tools when files are on", func() {
		f := newFiles()
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f })
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)

		parent := h.ToolNames(id)
		for _, want := range []string{"read_file", "write_file", "list_files", "move_file", "delete_file", "edit_file", "search_files", "spawn_agent"} {
			Expect(contains(parent, want)).To(BeTrue(), "the chat's tools %v lack %s", parent, want)
		}
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %d, want 1", len(kids))
		// The file tools are the one file toolset of a chat: a sub-agent that was
		// not narrowed gets them too.
		Expect(h.ToolNames(kids[0].ID)).To(ContainElements("read_file", "write_file", "edit_file", "list_files", "search_files"))
	})

	It("the model gets no file tools when files are off", func() {
		svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = files.Disabled("off") })
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		for _, name := range h.ToolNames(id) {
			Expect(name).NotTo(HaveSuffix("_file"), "a chat with files off got the tool %s", name)
			Expect(name).NotTo(Equal("list_files"), "a chat with files off got the tool %s", name)
			Expect(name).NotTo(Equal("search_files"), "a chat with files off got the tool %s", name)
		}
	})
})

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func newMediaService() (*media.Service, *provider.Provider) {
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"buy milk and call the dentist"}`))
	}))
	users := func(_ context.Context, id string) (*auth.User, error) {
		return &auth.User{ID: id, Status: auth.StatusActive}, nil
	}
	return media.New(p, users, func(c media.Capability) string { return "any-model" }), p
}

var _ = Describe("files", func() {
	It("a voice note becomes text in the message the model gets", func() {
		f := newFiles()
		m, p := newMediaService()
		h := studiotest.NewGateHarness()
		st, _ := store.NewJSON("")
		svc, err := studio.New(studio.Deps{Store: st, Provider: p, StateDir: GinkgoT().TempDir(), Harness: h, Files: f, Media: m})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		id := create(svc, alice)
		_, err = svc.SendWithFiles(context.Background(), alice, id, "my day", []session.Upload{upload("memo.m4a", "AUDIO")})
		Expect(err).NotTo(HaveOccurred())
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		got := h.Sent()[0]
		Expect(got).To(ContainSubstring("my day"), "the model got %q, want the text, the voice note's name and its transcript", got)
		Expect(got).To(ContainSubstring("buy milk and call the dentist"), "the model got %q, want the text, the voice note's name and its transcript", got)
		Expect(got).To(ContainSubstring("memo.m4a"), "the model got %q, want the text, the voice note's name and its transcript", got)
		h.Gate <- struct{}{}
		idle(svc, alice, id)
	})

	It("a voice note sent while a turn runs is transcribed and queued", func() {
		f := newFiles()
		m, p := newMediaService()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"text":"buy milk"}`))
		}))
		h := studiotest.NewGateHarness()
		st, _ := store.NewJSON("")
		svc, err := studio.New(studio.Deps{Store: st, Provider: p, StateDir: GinkgoT().TempDir(), Harness: h, Files: f, Media: m})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		until("the turn to start", func() bool { return len(h.Sent()) == 1 })
		res, err := svc.SendWithFiles(context.Background(), alice, id, "x", []session.Upload{upload("memo.m4a", "A")})
		Expect(err).NotTo(HaveOccurred(), "SendWithFiles = %+v, %v; want a waiting message", res, err)
		Expect(res.PendingID).NotTo(Equal(""), "SendWithFiles = %+v, %v; want a waiting message", res, err)
		got := pendingTexts(svc, alice, id)
		Expect(got).To(HaveLen(1), "waiting messages = %q, want the transcript and the voice note's path", got)
		Expect(got[0]).To(ContainSubstring("buy milk"), "waiting messages = %q, want the transcript and the voice note's path", got)
		Expect(got[0]).To(ContainSubstring("memo.m4a"), "waiting messages = %q, want the transcript and the voice note's path", got)
		h.Gate <- struct{}{}
		idle(svc, alice, id)
	})
})
