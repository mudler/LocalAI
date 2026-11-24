package session_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

var _ = Describe("the access of an add-on call", func() {
	var (
		ctx   context.Context
		ws    workspace.Store
		m     *session.Manager
		alice = workspace.Actor{Principal: "alice"}
		home  = session.HomePath("alice")
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
		Expect(ws.Grant(ctx, admin, "alice", home, workspace.ModeWrite)).To(Succeed())
		for _, dir := range []string{"/Acme", "/Acme/docs", "/Other", "/Chats/c1"} {
			_, err = ws.Mkdir(ctx, alice, home+dir)
			Expect(err).NotTo(HaveOccurred())
		}
		m, err = session.New(session.Options{Store: sessiontest.NewMemoryStore(), Workspace: ws, Harness: &sessiontest.FakeHarness{}, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
	})

	scoped := func(mode workspace.Mode) session.Access {
		acme, err := ws.Stat(ctx, alice, home+"/Acme")
		Expect(err).NotTo(HaveOccurred())
		chat, err := ws.Stat(ctx, alice, home+"/Chats/c1")
		Expect(err).NotTo(HaveOccurred())
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
			workspace.FenceRoot{Node: acme.ID, Mode: mode},
			workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite})
		x, err := m.ScopedAccess(ctx, a, acme.Path, chat.Path)
		Expect(err).NotTo(HaveOccurred())
		return x
	}

	It("gives a person's own call the whole home and no chat", func() {
		x, err := m.PersonAccess(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.Scoped()).To(BeFalse())
		Expect(x.Actor().ChatID).To(BeEmpty())
		Expect(x.Place().Primary()).To(Equal(home))
		v, err := x.SubView(ctx, ws, []workspace.MountRequest{{Path: home + "/Other", At: "other", Mode: workspace.ModeWrite}})
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Mounts).To(ConsistOf(HaveField("Mode", workspace.ModeWrite)))
	})

	It("refuses a person's access for a fenced actor or an actor of a chat", func() {
		_, err := m.PersonAccess(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"})
		Expect(err).To(MatchError(session.ErrInvalid))
		_, err = m.PersonAccess(ctx, alice.Fenced(workspace.FenceRoot{Node: 1, Mode: workspace.ModeRead}))
		Expect(err).To(MatchError(session.ErrInvalid))
		_, err = m.PersonAccess(ctx, workspace.Actor{})
		Expect(err).To(MatchError(session.ErrInvalid))
	})

	It("builds a view only of folders inside the access, at no more than their mode", func() {
		x := scoped(workspace.ModeRead)
		v, err := x.SubView(ctx, ws, []workspace.MountRequest{
			{Path: home + "/Acme/docs", At: "home", Mode: workspace.ModeWrite},
			{Path: home + "/Chats/c1", At: "chat", Mode: workspace.ModeWrite},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Actor.SameFence(x.Actor())).To(BeTrue(), "the view is the fenced actor's")
		modes := map[string]workspace.Mode{}
		for _, mt := range v.Mounts {
			modes[mt.At] = mt.Mode
		}
		Expect(modes).To(Equal(map[string]workspace.Mode{"home": workspace.ModeRead, "chat": workspace.ModeWrite}))
	})

	DescribeTable("refuses a folder outside the access",
		func(p string) {
			x := scoped(workspace.ModeWrite)
			_, err := x.SubView(ctx, ws, []workspace.MountRequest{{Path: p, At: "x", Mode: workspace.ModeRead}})
			Expect(err).To(HaveOccurred())
		},
		Entry("a sibling", session.HomePath("alice")+"/Other"),
		Entry("the home", session.HomePath("alice")),
		Entry("a name that starts like the folder", session.HomePath("alice")+"/AcmeX"),
		Entry("a parent spelled inside", session.HomePath("alice")+"/Acme/../Other"),
		Entry("another home", session.HomePath("bob")),
		Entry("a relative path", "Acme"),
	)

	It("refuses an empty request and a missing store", func() {
		x := scoped(workspace.ModeWrite)
		_, err := x.SubView(ctx, ws, nil)
		Expect(err).To(MatchError(session.ErrInvalid))
		_, err = x.SubView(ctx, nil, []workspace.MountRequest{{Path: home + "/Acme", At: "home"}})
		Expect(err).To(MatchError(session.ErrInvalid))
		_, err = session.Access{}.SubView(ctx, ws, []workspace.MountRequest{{Path: home + "/Acme", At: "home"}})
		Expect(err).To(HaveOccurred(), "the zero access reaches nothing")
	})
})
