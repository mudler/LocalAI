package session_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const filesAdmin = "admin"

type filesRig struct {
	m     *session.Manager
	ws    workspace.Store
	store session.Store
}

// newFilesRig is a manager with a workspace in which alice and bob have a home
// folder, and a harness whose every turn says "ok".
func newFilesRig() *filesRig {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: filesAdmin})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	admin := workspace.Actor{Principal: filesAdmin}
	for _, p := range []string{"alice", "bob"} {
		Expect(ws.Mkdir(context.Background(), admin, session.ChatsPath(p))).Error().NotTo(HaveOccurred())
		Expect(ws.Grant(context.Background(), admin, p, session.HomePath(p), workspace.ModeWrite)).To(Succeed())
	}
	turns := make([][]sessiontest.Step, 20)
	for i := range turns {
		turns[i] = []sessiontest.Step{{Say: "ok"}}
	}
	st := sessiontest.NewMemoryStore()
	m, err := session.New(session.Options{Store: st, Workspace: ws, Harness: &sessiontest.FakeHarness{Turns: turns}, StateDir: GinkgoT().TempDir()})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	return &filesRig{m: m, ws: ws, store: st}
}

func (r *filesRig) open(p, id string) *session.Chat {
	c, err := r.m.Open(context.Background(), workspace.Actor{Principal: p, ChatID: id}, id)
	Expect(err).NotTo(HaveOccurred())
	return c
}

// turn sends a message and waits for its turn_end. It returns the kinds of the
// events of the turn.
func turn(c *session.Chat, msg session.Message) []session.EventKind {
	Expect(c.Send(context.Background(), msg)).To(Succeed())
	var kinds []session.EventKind
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-c.Events():
			kinds = append(kinds, e.Kind)
			if e.Kind == session.KindTurnEnd {
				return kinds
			}
		case <-timeout:
			Fail("timed out waiting for turn_end")
		}
	}
}

func upload(names ...string) []session.Upload {
	var out []session.Upload
	for _, n := range names {
		out = append(out, session.Upload{Name: n, Body: strings.NewReader("content of " + n)})
	}
	return out
}

func put(ws workspace.Store, a workspace.Actor, p, body string) workspace.Node {
	n, err := ws.Put(context.Background(), a, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
	return n
}

func byName(cf session.ChatFiles) map[string]session.ChatFile {
	out := map[string]session.ChatFile{}
	for _, f := range cf.Files {
		out[f.Name] = f
	}
	return out
}

var _ = Describe("MimeOf", func() {
	It("gives the same type for the common text files on every host", func() {
		Expect(session.MimeOf("notes/out.md")).To(Equal("text/markdown"))
		Expect(session.MimeOf("README.MARKDOWN")).To(Equal("text/markdown"))
		Expect(session.MimeOf("data.csv")).To(Equal("text/csv"))
		Expect(session.MimeOf("a.txt")).To(Equal("text/plain"))
		Expect(session.MimeOf("pic.png")).To(Equal("image/png"))
		Expect(session.MimeOf("noext")).To(Equal("application/octet-stream"))
	})
})

var _ = Describe("the files of a chat", func() {
	It("tells uploads, model and tool writes apart", func() {
		r := newFilesRig()
		ctx := context.Background()
		alice := workspace.Actor{Principal: "alice"}
		chatActor := workspace.Actor{Principal: "alice", ChatID: "c1"}
		home := session.HomePath("alice")
		put(r.ws, alice, home+"/old.txt", "there before")

		c := r.open("alice", "c1")
		kinds := turn(c, session.Message{Text: "read these", Files: upload("q1.csv", "photo.png")})
		Expect(kinds).To(ContainElement(session.KindFiles), "an upload emits no files event")

		// What a sandbox checkpoint commits is the model's work; the transcript
		// names its revision.
		report := put(r.ws, chatActor, home+"/report.md", "# report")
		Expect(r.store.AppendEntry(ctx, "c1", session.Entry{Turn: 2, Role: session.RoleCheckpoint, Rev: report.Rev})).Error().NotTo(HaveOccurred())
		// A host tool's change has no checkpoint.
		put(r.ws, chatActor, home+"/notes/tool.txt", "from a tool")
		put(r.ws, chatActor, home+"/old.txt", "changed by the chat")
		// A file the chat wrote and then deleted is not listed.
		put(r.ws, chatActor, home+"/gone.txt", "x")
		Expect(r.ws.Delete(ctx, chatActor, home+"/gone.txt")).To(Succeed())
		// A change of another chat is not this chat's.
		put(r.ws, workspace.Actor{Principal: "alice", ChatID: "other"}, home+"/other.txt", "x")

		cf, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		got := byName(cf)
		want := map[string]struct {
			origin  session.FileOrigin
			changed bool
			turn    int
		}{
			"q1.csv":    {session.FileFromUser, false, 1},
			"photo.png": {session.FileFromUser, false, 1},
			"report.md": {session.FileFromModel, false, 2},
			// Made after the entry of turn 2, so they count as turn 2.
			"tool.txt": {session.FileFromTool, false, 2},
			"old.txt":  {session.FileFromTool, true, 2},
		}
		Expect(got).To(HaveLen(len(want)), "files = %+v", cf.Files)
		for name, w := range want {
			f, ok := got[name]
			Expect(ok).To(BeTrue(), "%s is missing", name)
			Expect(f.Origin).To(Equal(w.origin), name)
			Expect(f.Changed).To(Equal(w.changed), name)
			Expect(f.Turn).To(Equal(w.turn), name)
		}
		f := got["q1.csv"]
		Expect(f.GuestPath).To(HavePrefix(session.GuestRoot+"/home/Chats/"), "q1.csv = %+v", f)
		Expect(f.Mime).To(Equal("text/csv"), "q1.csv = %+v", f)
		Expect(f.Size).NotTo(BeZero(), "q1.csv = %+v", f)
		Expect(got["photo.png"].Mime).To(Equal("image/png"))
	})

	It("follows a rename of the chat folder", func() {
		r := newFilesRig()
		ctx := context.Background()
		c := r.open("alice", "c1")
		turn(c, session.Message{Text: "first", Files: upload("a.txt")})
		before, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred(), "before the rename")
		Expect(before.Files).To(HaveLen(1), "before the rename: %+v", before)
		oldPath := before.Files[0].GuestPath

		Expect(c.SetTitle(ctx, "Budget review")).To(Succeed())
		after, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred(), "after the rename")
		Expect(after.Files).To(HaveLen(1), "after the rename: %+v", after)
		f := after.Files[0]
		Expect(f.GuestPath).NotTo(Equal(oldPath), "path after the rename = %q (was %q)", f.GuestPath, oldPath)
		Expect(f.GuestPath).To(ContainSubstring("Budget review/a.txt"), "path after the rename = %q (was %q)", f.GuestPath, oldPath)
		Expect(f.Origin).To(Equal(session.FileFromUser), "the upload lost its origin or turn: %+v", f)
		Expect(f.Turn).To(Equal(1), "the upload lost its origin or turn: %+v", f)
		_, err = r.ws.Stat(ctx, workspace.Actor{Principal: "alice"}, f.Path)
		Expect(err).NotTo(HaveOccurred(), "the listed path does not exist")
		oldFolder := oldPath[:strings.LastIndex(oldPath, "/")+1]
		Expect(after.Stale).To(Equal([]string{oldFolder}), "stale")
		prompt := session.FilesPrompt(after)
		Expect(prompt).To(ContainSubstring(after.Folder+"\n- a.txt ("), "the prompt lacks the current path")
		Expect(prompt).To(ContainSubstring("renamed to "+after.Folder), "the prompt does not say the folder moved")
		Expect(prompt).To(ContainSubstring(oldFolder), "the prompt does not say the folder moved")
		Expect(strings.Split(prompt, "Earlier messages")[0]).NotTo(ContainSubstring(oldFolder), "the file list still names the old folder")
	})

	It("are not found for another user", func() {
		r := newFilesRig()
		c := r.open("alice", "c1")
		turn(c, session.Message{Text: "x", Files: upload("secret.txt")})
		_, err := r.m.Files(context.Background(), "bob", "c1")
		Expect(err).To(MatchError(session.ErrNotFound), "bob reads alice's chat files")
	})

	It("give an empty prompt without files and a capped one with many", func() {
		r := newFilesRig()
		ctx := context.Background()
		c := r.open("alice", "c1")
		turn(c, session.Message{Text: "no files"})
		cf, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(cf.Files).To(BeEmpty(), "a chat without files")
		Expect(session.FilesPrompt(cf)).To(BeEmpty(), "a chat without files")

		names := make([]string, 60)
		for i := range names {
			names[i] = fmt.Sprintf("f%02d.txt", i)
		}
		turn(c, session.Message{Text: "many", Files: upload(names...)})
		cf, err = r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(cf.Files).To(HaveLen(60), "index of 60 files")
		prompt := session.FilesPrompt(cf)
		lines := strings.Split(prompt, "\n")
		Expect(lines).To(HaveLen(1+1+session.MaxPromptFiles+1), "want a head, the folder, %d files and a count:\n%s", session.MaxPromptFiles, prompt)
		Expect(lines[len(lines)-1]).To(ContainSubstring("and 10 older files"))
		Expect(prompt).NotTo(ContainSubstring("f00.txt"), "the cap keeps the oldest files, not the newest:\n%s", prompt)
		Expect(prompt).To(ContainSubstring("\n- f59.txt ("), "the cap keeps the oldest files, not the newest:\n%s", prompt)
		Expect(strings.Count(prompt, cf.Folder)).To(Equal(2), "the cap keeps the oldest files, not the newest:\n%s", prompt)
	})
})
