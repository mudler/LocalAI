package session_test

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("session access", func() {
	var (
		ctx   context.Context
		ws    workspace.Store
		h     *sessiontest.FakeHarness
		m     *session.Manager
		st    session.Store
		alice = workspace.Actor{Principal: "alice"}
	)
	BeforeEach(func() {
		ctx = context.Background()
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		js, err := jsonstore.New("", blobs, jsonstore.Options{Admin: filesAdmin})
		Expect(err).NotTo(HaveOccurred())
		ws = js
		admin := workspace.Actor{Principal: filesAdmin}
		Expect(ws.Mkdir(ctx, admin, session.ChatsPath("alice"))).Error().NotTo(HaveOccurred())
		Expect(ws.Grant(ctx, admin, "alice", session.HomePath("alice"), workspace.ModeWrite)).To(Succeed())
		_, err = ws.Mkdir(ctx, alice, session.HomePath("alice")+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		h = &sessiontest.FakeHarness{}
		st = sessiontest.NewMemoryStore()
		m, err = session.New(session.Options{Store: st, Workspace: ws, Harness: h, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
	})

	It("needs a chat ID and keeps the owner without the fence", func() {
		_, err := session.NewAccess(alice, workspace.View{})
		Expect(err).To(MatchError(session.ErrInvalid))
		_, err = session.NewAccess(workspace.Actor{ChatID: "c1"}, workspace.View{})
		Expect(err).To(MatchError(session.ErrInvalid))
		a := workspace.Actor{Principal: "alice", Groups: []string{"g"}, ChatID: "c1"}.Fenced(workspace.FenceRoot{Node: 5, Mode: workspace.ModeRead})
		x, err := session.NewAccess(a, workspace.View{})
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeTrue())
		Expect(x.Actor().SameFence(a)).To(BeTrue())
		Expect(x.View().Actor.SameFence(a)).To(BeTrue(), "the view carries the actor")
		Expect(x.Owner()).To(Equal(workspace.Actor{Principal: "alice", Groups: []string{"g"}}))
		_, err = m.UserAccess(ctx, a)
		Expect(err).To(MatchError(session.ErrInvalid), "a fenced actor gets no view of the home")
	})

	It("takes a new fence at the next open even when the view is the same", func() {
		notes, err := ws.Stat(ctx, alice, session.HomePath("alice")+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		base := workspace.Actor{Principal: "alice", ChatID: "c1"}
		read := base.Fenced(workspace.FenceRoot{Node: notes.ID, Mode: workspace.ModeRead})
		write := base.Fenced(workspace.FenceRoot{Node: notes.ID, Mode: workspace.ModeWrite})
		v, err := ws.View(ctx, read, []workspace.MountRequest{{Path: notes.Path, At: session.GuestHome, Mode: workspace.ModeRead}})
		Expect(err).NotTo(HaveOccurred())
		x1, err := session.NewAccess(read, v)
		Expect(err).NotTo(HaveOccurred())
		x2, err := session.NewAccess(write, v)
		Expect(err).NotTo(HaveOccurred())

		_, err = m.Open(ctx, x1, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Starts()).To(HaveLen(1))
		_, err = m.Open(ctx, x1, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(h.Starts()).To(HaveLen(1), "the same access changes nothing")
		_, err = m.Open(ctx, x2, "c1")
		Expect(err).NotTo(HaveOccurred())
		starts := h.Starts()
		Expect(starts).To(HaveLen(2), "a new fence restarts the harness")
		Expect(starts[1].Actor.SameFence(write)).To(BeTrue())
	})

	It("makes the folder of a chat that is not open, once, as the user", func() {
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "c2", Principal: "alice", Title: "Plans"})).To(Succeed())
		n, err := m.EnsureChatFolder(ctx, alice, "c2")
		Expect(err).NotTo(HaveOccurred())
		Expect(n.Kind).To(Equal(workspace.KindDir))
		rec, err := st.GetChat(ctx, "alice", "c2")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Folder).NotTo(BeEmpty())
		Expect(n.Path).To(Equal(session.ChatsPath("alice") + "/" + rec.Folder))
		again, err := m.EnsureChatFolder(ctx, alice, "c2")
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).To(Equal(n.ID))

		_, err = m.EnsureChatFolder(ctx, workspace.Actor{Principal: "bob"}, "c2")
		Expect(err).To(MatchError(session.ErrNotFound), "another user's chat")
		_, err = m.EnsureChatFolder(ctx, alice.Fenced(workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeWrite}), "c2")
		Expect(err).To(MatchError(session.ErrInvalid), "a fenced actor")
		_, err = m.EnsureChatFolder(ctx, workspace.Actor{Principal: "alice", ChatID: "c2"}, "c2")
		Expect(err).To(MatchError(session.ErrInvalid), "an actor of a chat")
	})

	It("builds a scoped access with the view builder of every chat", func() {
		notes, err := ws.Stat(ctx, alice, session.HomePath("alice")+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		chat, err := ws.Mkdir(ctx, alice, session.ChatsPath("alice")+"/2026-11-01 Plan")
		Expect(err).NotTo(HaveOccurred())
		base := workspace.Actor{Principal: "alice", ChatID: "c1"}
		a := base.Fenced(workspace.FenceRoot{Node: notes.ID, Mode: workspace.ModeRead}, workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite})

		x, err := m.ScopedAccess(ctx, a, notes.Path, chat.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeTrue())
		Expect(x.View().Mounts).To(Equal([]workspace.Mount{
			{Path: notes.Path, At: session.GuestHome, Mode: workspace.ModeRead, NodeID: notes.ID},
			{Path: chat.Path, At: session.GuestChat, Mode: workspace.ModeWrite, NodeID: chat.ID},
		}), "the folder at the fence's mode, the chat folder read and write")
		Expect(x.Place().Primary()).To(Equal(notes.Path))
		Expect(x.Place().Chat()).To(Equal(chat.Path))

		_, err = m.ScopedAccess(ctx, base, session.HomePath("alice"), chat.Path)
		Expect(err).To(MatchError(session.ErrInvalid), "an actor without a fence has no scoped access")
		_, err = m.ScopedAccess(ctx, a, notes.Path, "")
		Expect(err).To(MatchError(session.ErrInvalid), "a scoped access needs its chat folder")
		_, err = m.ScopedAccess(ctx, a, session.HomePath("alice"), chat.Path)
		Expect(err).To(MatchError(session.ErrNoHome), "the home is outside the fence: never mounted")
		other, err := ws.Mkdir(ctx, alice, session.ChatsPath("alice")+"/other")
		Expect(err).NotTo(HaveOccurred())
		_, err = m.ScopedAccess(ctx, a, notes.Path, other.Path)
		Expect(err).To(MatchError(workspace.ErrNotFound), "another chat's folder is outside the fence")
	})

	It("gives a command runner the turn's view and the skill layers the actor can read", func() {
		admin := workspace.Actor{Principal: filesAdmin}
		_, err := ws.Put(ctx, admin, "/builtin-skills/pdf/SKILL.md", strings.NewReader("pdf"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Grant(ctx, admin, "alice", "/builtin-skills", workspace.ModeRead)).To(Succeed())
		notes, err := ws.Stat(ctx, alice, session.HomePath("alice")+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		layer, err := ws.Stat(ctx, admin, "/builtin-skills")
		Expect(err).NotTo(HaveOccurred())
		chat, err := ws.Mkdir(ctx, alice, session.ChatsPath("alice")+"/2026-11-01 Run")
		Expect(err).NotTo(HaveOccurred())
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
			workspace.FenceRoot{Node: notes.ID, Mode: workspace.ModeRead},
			workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite},
			workspace.FenceRoot{Node: layer.ID, Mode: workspace.ModeRead})
		x, err := m.ScopedAccess(ctx, a, notes.Path, chat.Path)
		Expect(err).NotTo(HaveOccurred())
		v, err := x.SandboxView(ctx, ws)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Actor.SameFence(a)).To(BeTrue())
		var ats []string
		for _, mt := range v.Mounts {
			ats = append(ats, mt.At+" "+mt.Path)
			Expect(mt.Mode == workspace.ModeRead || mt.At == session.GuestChat).To(BeTrue(), "%s is writable", mt.At)
		}
		Expect(ats).To(Equal([]string{"home " + notes.Path, "chat " + chat.Path, "shared-builtin-skills /builtin-skills"}))

		// The unscoped access of the same user mounts the home, writable.
		ux, err := m.UserAccess(ctx, workspace.Actor{Principal: "alice", ChatID: "c2"})
		Expect(err).NotTo(HaveOccurred())
		uv, err := ux.SandboxView(ctx, ws)
		Expect(err).NotTo(HaveOccurred())
		Expect(uv.Mounts[0].Path).To(Equal(session.HomePath("alice")))
		Expect(uv.Mounts[0].Mode).To(Equal(workspace.ModeWrite))
		Expect(uv.Mounts[len(uv.Mounts)-1].At).To(Equal("shared-builtin-skills"))
	})

	It("gives the same access to another chat of the user", func() {
		notes, err := ws.Stat(ctx, alice, session.HomePath("alice")+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		a := workspace.Actor{Principal: "alice", ChatID: "child"}.Fenced(workspace.FenceRoot{Node: notes.ID, Mode: workspace.ModeRead})
		v, err := ws.View(ctx, a, []workspace.MountRequest{{Path: notes.Path, At: session.GuestHome, Mode: workspace.ModeRead}})
		Expect(err).NotTo(HaveOccurred())
		x, err := session.NewAccess(a, v)
		Expect(err).NotTo(HaveOccurred())
		p, err := x.ForChat("parent")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Actor().ChatID).To(Equal("parent"))
		Expect(p.Actor().SameFence(a)).To(BeTrue(), "the fence stays")
		Expect(p.View().Mounts).To(Equal(x.View().Mounts))
		Expect(p.Place().Primary()).To(Equal(notes.Path))
		Expect(x.Actor().ChatID).To(Equal("child"), "the access it came from is unchanged")
		_, err = x.ForChat("")
		Expect(err).To(MatchError(session.ErrInvalid))
	})
})
