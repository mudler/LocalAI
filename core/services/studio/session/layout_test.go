package session_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

type layoutRig struct {
	ws          workspace.Store
	admin       workspace.Actor
	acme, chat  workspace.Node
	team, chatS workspace.Node
}

// newLayoutRig is a workspace where alice has Projects/Acme, a chat folder
// and two shared folders, team (write) and chat (read).
func newLayoutRig() *layoutRig {
	ctx := context.Background()
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: filesAdmin})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	r := &layoutRig{ws: ws, admin: workspace.Actor{Principal: filesAdmin}}
	mk := func(p string) workspace.Node {
		n, err := ws.Mkdir(ctx, r.admin, p)
		Expect(err).NotTo(HaveOccurred())
		return n
	}
	mk(session.HomePath("alice"))
	r.acme = mk(session.HomePath("alice") + "/Projects/Acme")
	r.chat = mk(session.ChatsPath("alice") + "/2026-11-01 Plan")
	mk(session.HomePath("alice") + "/Other")
	r.team = mk("/shared/team")
	r.chatS = mk("/shared/chat")
	Expect(ws.Grant(ctx, r.admin, "alice", session.HomePath("alice"), workspace.ModeWrite)).To(Succeed())
	Expect(ws.Grant(ctx, r.admin, "alice", "/shared/team", workspace.ModeWrite)).To(Succeed())
	Expect(ws.Grant(ctx, r.admin, "alice", "/shared/chat", workspace.ModeRead)).To(Succeed())
	return r
}

// scoped is alice's chat actor fenced to Acme at mode, the chat folder and
// the shared folders. The shared roots are at write so that the test sees
// BuildView, not the fence, make them read only.
func (r *layoutRig) scoped(mode workspace.Mode) workspace.Actor {
	return workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
		workspace.FenceRoot{Node: r.acme.ID, Mode: mode},
		workspace.FenceRoot{Node: r.chat.ID, Mode: workspace.ModeWrite},
		workspace.FenceRoot{Node: r.team.ID, Mode: workspace.ModeWrite},
		workspace.FenceRoot{Node: r.chatS.ID, Mode: workspace.ModeWrite},
	)
}

var layoutShared = []session.SharedRoot{{Path: "/shared/team", Mode: workspace.ModeWrite}, {Path: "/shared/chat", Mode: workspace.ModeRead}}

func mountAt(v workspace.View, at string) (workspace.Mount, bool) {
	for _, m := range v.Mounts {
		if m.At == at {
			return m, true
		}
	}
	return workspace.Mount{}, false
}

var quiet = slog.New(slog.DiscardHandler)

var _ = Describe("the layout of a chat's view", func() {
	It("mounts the folder at home and the chat folder at chat", func() {
		r := newLayoutRig()
		v, err := session.BuildView(context.Background(), r.ws, r.scoped(workspace.ModeRead), r.acme.Path, r.chat.Path, nil, quiet)
		Expect(err).NotTo(HaveOccurred())
		home, ok := mountAt(v, session.GuestHome)
		Expect(ok).To(BeTrue())
		Expect(home.Path).To(Equal(r.acme.Path))
		Expect(home.Mode).To(Equal(workspace.ModeRead))
		chat, ok := mountAt(v, session.GuestChat)
		Expect(ok).To(BeTrue())
		Expect(chat.Path).To(Equal(r.chat.Path))
		Expect(chat.Mode).To(Equal(workspace.ModeWrite))
		Expect(v.Mounts).To(HaveLen(2))
	})

	It("keeps today's view for an unscoped chat", func() {
		r := newLayoutRig()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}
		v, err := session.BuildView(context.Background(), r.ws, a, session.HomePath("alice"), "", layoutShared, quiet)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Mounts).To(HaveLen(3))
		home, _ := mountAt(v, session.GuestHome)
		Expect(home.Path).To(Equal(session.HomePath("alice")))
		Expect(home.Mode).To(Equal(workspace.ModeWrite))
		team, _ := mountAt(v, "shared-team")
		Expect(team.Mode).To(Equal(workspace.ModeWrite))
		_, ok := mountAt(v, session.GuestChat)
		Expect(ok).To(BeFalse())
	})

	It("refuses a primary folder outside the fence as a missing home", func() {
		r := newLayoutRig()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(workspace.FenceRoot{Node: r.acme.ID, Mode: workspace.ModeWrite})
		_, err := session.BuildView(context.Background(), r.ws, a, session.HomePath("alice"), "", nil, quiet)
		Expect(err).To(MatchError(session.ErrNoHome))
	})

	It("mounts shared folders read only for a scoped view", func() {
		r := newLayoutRig()
		v, err := session.BuildView(context.Background(), r.ws, r.scoped(workspace.ModeWrite), r.acme.Path, r.chat.Path, layoutShared, quiet)
		Expect(err).NotTo(HaveOccurred())
		team, ok := mountAt(v, "shared-team")
		Expect(ok).To(BeTrue())
		Expect(team.Mode).To(Equal(workspace.ModeRead))
		home, _ := mountAt(v, session.GuestHome)
		Expect(home.Mode).To(Equal(workspace.ModeWrite))
	})

	It("renames a shared folder called chat", func() {
		r := newLayoutRig()
		v, err := session.BuildView(context.Background(), r.ws, r.scoped(workspace.ModeRead), r.acme.Path, r.chat.Path, layoutShared, quiet)
		Expect(err).NotTo(HaveOccurred())
		chat, _ := mountAt(v, session.GuestChat)
		Expect(chat.Path).To(Equal(r.chat.Path), "the name chat is the chat folder's")
		s, ok := mountAt(v, "shared-chat")
		Expect(ok).To(BeTrue())
		Expect(s.Path).To(Equal("/shared/chat"))
		ats := map[string]int{}
		for _, m := range v.Mounts {
			ats[workspace.FoldName(m.At)]++
		}
		for at, n := range ats {
			Expect(n).To(Equal(1), at)
		}
	})

	It("tells a scoped chat only its folders and no page hint", func() {
		r := newLayoutRig()
		v, err := session.BuildView(context.Background(), r.ws, r.scoped(workspace.ModeRead), r.acme.Path, r.chat.Path, layoutShared, quiet)
		Expect(err).NotTo(HaveOccurred())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(ContainSubstring(`- /workspace/home: the folder "Acme" of the user (read only). You cannot see the user's other files.`))
		Expect(p).To(ContainSubstring("- /workspace/chat: this chat's own folder (read and write). Files the user attaches are here; save working files in /workspace/chat/work/."))
		Expect(p).To(ContainSubstring("- /workspace/shared-team: a folder shared with the user (read only)."))
		Expect(p).To(ContainSubstring("There is nothing else under /workspace."))
		for _, leak := range []string{"Page", "pages/", "Projects", "Other", "Chats", "/users/", "home folder", "2026-11-01 Plan"} {
			Expect(p).NotTo(ContainSubstring(leak))
		}
	})

	It("names the whole home for a scoped chat whose folder is the home", func() {
		r := newLayoutRig()
		home, err := r.ws.Stat(context.Background(), r.admin, session.HomePath("alice"))
		Expect(err).NotTo(HaveOccurred())
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
			workspace.FenceRoot{Node: home.ID, Mode: workspace.ModeRead},
			workspace.FenceRoot{Node: r.chat.ID, Mode: workspace.ModeWrite},
		)
		v, err := session.BuildView(context.Background(), r.ws, a, home.Path, r.chat.Path, nil, quiet)
		Expect(err).NotTo(HaveOccurred())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(ContainSubstring("- /workspace/home: the user's home folder (read only)."))
		Expect(p).NotTo(ContainSubstring("Page"))
	})

	It("keeps today's prompt for an unscoped chat", func() {
		r := newLayoutRig()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}
		v, err := session.BuildView(context.Background(), r.ws, a, session.HomePath("alice"), "", layoutShared, quiet)
		Expect(err).NotTo(HaveOccurred())
		Expect(session.PromptScope(v).Scoped).To(BeFalse())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(HavePrefix("The user's files are in these folders"))
		Expect(p).To(ContainSubstring("- /workspace/home: the user's home folder (read and write). Files the user uploads to a chat are in /workspace/home/Chats/."))
		Expect(p).To(ContainSubstring("build a Page: /workspace/home/pages/<name>/index.html"))
		Expect(strings.Count(p, "\n- ")).To(Equal(3))
	})
})

var _ = Describe("the view of an agent with several folders", func() {
	It("mounts the first at home and the others by name, each capped by the fence", func() {
		r := newLayoutRig()
		other := session.HomePath("alice") + "/Other"
		on, err := r.ws.Stat(context.Background(), r.admin, other)
		Expect(err).NotTo(HaveOccurred())
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
			workspace.FenceRoot{Node: r.acme.ID, Mode: workspace.ModeWrite},
			workspace.FenceRoot{Node: on.ID, Mode: workspace.ModeRead},
			workspace.FenceRoot{Node: r.chat.ID, Mode: workspace.ModeWrite},
		)
		v, err := session.BuildViewFolders(context.Background(), r.ws, a, r.acme.Path, []string{other}, r.chat.Path, nil, quiet)
		Expect(err).NotTo(HaveOccurred())
		home, ok := mountAt(v, session.GuestHome)
		Expect(ok).To(BeTrue())
		Expect(home.Path).To(Equal(r.acme.Path))
		Expect(home.Mode).To(Equal(workspace.ModeWrite))
		o, ok := mountAt(v, "folder-Other")
		Expect(ok).To(BeTrue(), "%+v", v.Mounts)
		Expect(o.Mode).To(Equal(workspace.ModeRead), "the fence caps the mount")
		Expect(session.LayoutPrompt(v, session.PromptScope(v))).To(ContainSubstring("/workspace/folder-Other"))
	})

	It("skips a folder outside the fence instead of widening it", func() {
		r := newLayoutRig()
		other := session.HomePath("alice") + "/Other"
		v, err := session.BuildViewFolders(context.Background(), r.ws, r.scoped(workspace.ModeWrite), r.acme.Path, []string{other}, r.chat.Path, nil, quiet)
		Expect(err).NotTo(HaveOccurred())
		_, ok := mountAt(v, "folder-Other")
		Expect(ok).To(BeFalse(), "the folder is not a root of the fence")
	})

	It("names two folders with the same name apart and never takes a reserved name", func() {
		names := session.FolderMountNames([]string{"/users/a/X/Docs", "/users/a/Y/Docs", "/users/a/Z/Docs"})
		Expect(names).To(Equal([]string{"folder-Docs", "folder-Docs 2", "folder-Docs 3"}))
	})

	It("refuses a path that climbs out of a mounted folder", func() {
		r := newLayoutRig()
		other := session.HomePath("alice") + "/Other"
		on, _ := r.ws.Stat(context.Background(), r.admin, other)
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}.Fenced(
			workspace.FenceRoot{Node: r.acme.ID, Mode: workspace.ModeWrite},
			workspace.FenceRoot{Node: on.ID, Mode: workspace.ModeRead},
			workspace.FenceRoot{Node: r.chat.ID, Mode: workspace.ModeWrite})
		v, err := session.BuildViewFolders(context.Background(), r.ws, a, r.acme.Path, []string{other}, r.chat.Path, nil, quiet)
		Expect(err).NotTo(HaveOccurred())
		pl := session.NewPlace(v)
		for _, p := range []string{"/workspace/folder-Other/../home/x", "/workspace/folder-Other/../../alice/secret", "folder-Other/.."} {
			_, err := pl.Resolve(p)
			Expect(err).To(HaveOccurred(), p)
		}
		t, err := pl.Resolve("/workspace/folder-Other/notes.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Write).To(BeFalse())
		Expect(t.Abs).To(Equal(other + "/notes.md"))
	})
})
