package studio_test

import (
	"context"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A project that another user shares reaches a chat only as a mount of a chat
// without a folder. The fence of an agent with a folder (a project agent, a
// folder agent, a sub-agent of either) never holds it.
var _ = Describe("a shared project and the fence of an agent", func() {
	ctx := context.Background()

	type rig struct {
		svc  *studio.Service
		proj *projects.Service
		st   store.Store
		f    *files.Service
	}
	newRig := func() rig {
		f := newFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studio.New(studio.Deps{Store: st, Agents: ag, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			Harness: studiotest.NewGateHarness(), Files: f, Projects: proj, FolderDirectory: folderPeople{}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		for _, u := range []string{alice, bob} {
			Expect(f.EnsureHome(ctx, u)).To(Succeed())
		}
		return rig{svc, proj, st, f}
	}
	mk := func(r rig, owner, name string) projects.Project {
		p, err := r.proj.Create(ctx, owner, projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	// shareNode shares a folder of alice's with bob at mode.
	shareNode := func(r rig, node int64, mode workspace.Mode) {
		_, err := r.svc.SharedFolders().OwnSetMember(ctx, files.UserActor(alice), node, bob, mode)
		Expect(err).NotTo(HaveOccurred())
	}
	put := func(r rig, abs, content string) {
		_, err := r.f.Store().Put(ctx, r.f.Actor(alice), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	// canRead says whether the actor reads the file through the store, where
	// the fence is held.
	canRead := func(r rig, a workspace.Actor, abs string) bool {
		rc, _, err := r.f.Store().Open(ctx, a, abs)
		if err != nil {
			return false
		}
		defer func() { _ = rc.Close() }()
		_, _ = io.Copy(io.Discard, rc)
		return true
	}
	chatOf := func(r rig, agentID string) session.Access {
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: agentID})
		Expect(err).NotTo(HaveOccurred())
		x, err := r.svc.ChatAccess(ctx, bob, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		return x
	}
	subOf := func(r rig, agentID, id string) session.Access {
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: agentID})
		Expect(err).NotTo(HaveOccurred())
		kid := session.ChatRecord{ID: id, Principal: bob, Title: "Sub", Kind: session.KindSubAgent, ParentID: rec.ID, Model: "m"}
		Expect(r.st.CreateChat(ctx, kid)).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, bob, kid.ID)
		Expect(err).NotTo(HaveOccurred())
		return x
	}
	folderAgent := func(r rig) agents.Agent {
		_, err := r.f.Store().Mkdir(ctx, r.f.Actor(bob), session.HomePath(bob)+"/Work")
		Expect(err).NotTo(HaveOccurred())
		a, err := r.svc.SaveAgentWithFolder(ctx, bob, agents.Agent{Name: "Folder", Model: "m"}, &studio.AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	projectAgent := func(r rig, p projects.Project) agents.Agent {
		a, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Project agent", Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	sharedMounts := func(x session.Access) []workspace.Mount {
		var out []workspace.Mount
		for _, m := range x.View().Mounts {
			if strings.HasPrefix(m.At, session.GuestSharedPrefix) {
				out = append(out, m)
			}
		}
		return out
	}

	const secret = "/users/alice/Projects/acme/files/secret.md"

	// setup: alice shares her project Acme with bob at write, and a plain
	// folder Team at read; bob has a project of his own.
	setup := func() (rig, projects.Project, projects.Project, workspace.Node) {
		r := newRig()
		acme := mk(r, alice, "Acme")
		put(r, secret, "alice's project file")
		shareNode(r, acme.Node, workspace.ModeWrite)
		team, err := r.f.Store().Mkdir(ctx, r.f.Actor(alice), "/users/alice/Team")
		Expect(err).NotTo(HaveOccurred())
		put(r, "/users/alice/Team/note.md", "team note")
		shareNode(r, team.ID, workspace.ModeRead)
		mine := mk(r, bob, "Mine")
		return r, acme, mine, team
	}

	It("keeps the plain shared mount in a chat without a folder", func() {
		r, _, _, team := setup()
		x := chatOf(r, "")
		var paths []string
		for _, m := range sharedMounts(x) {
			paths = append(paths, m.Path)
		}
		Expect(paths).To(ConsistOf("/users/alice/Projects/acme", team.Path), "%+v", x.View().Mounts)
		Expect(canRead(r, x.Actor(), secret)).To(BeTrue())
	})

	DescribeTable("keeps the shared project out of the fence of",
		func(access func(r rig, mine projects.Project) session.Access) {
			r, _, mine, team := setup()
			x := access(r, mine)
			Expect(x.Scoped()).To(BeTrue())
			Expect(canRead(r, x.Actor(), secret)).To(BeFalse(), "a read of the shared project's file is refused")
			Expect(canRead(r, x.Actor(), "/users/alice/Projects/acme/project.json")).To(BeFalse())
			for _, m := range x.View().Mounts {
				Expect(m.Path).NotTo(HavePrefix("/users/alice/Projects"), "%+v", x.View().Mounts)
			}
			// The prompt tells nothing of what the fence left out.
			Expect(x.View().NotMounted).To(BeZero())
			Expect(session.LayoutPrompt(x.View(), session.PromptScope(x.View()))).NotTo(ContainSubstring("not mounted"))
			var teamMount []workspace.Mount
			for _, m := range x.View().Mounts {
				if m.Path == team.Path {
					teamMount = append(teamMount, m)
				}
			}
			Expect(teamMount).To(HaveLen(1), "the ordinary shared folder is mounted")
			Expect(teamMount[0].Mode).To(Equal(workspace.ModeRead))
			// An ordinary shared folder stays, read only.
			Expect(canRead(r, x.Actor(), team.Path+"/note.md")).To(BeTrue())
			_, err := r.f.Store().Put(ctx, x.Actor(), team.Path+"/x.md", strings.NewReader("x"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly))
		},
		Entry("a folder agent", func(r rig, _ projects.Project) session.Access { return chatOf(r, folderAgent(r).ID) }),
		Entry("a project agent", func(r rig, mine projects.Project) session.Access { return chatOf(r, projectAgent(r, mine).ID) }),
		Entry("a sub-agent of a folder agent", func(r rig, _ projects.Project) session.Access {
			return subOf(r, folderAgent(r).ID, "kid-folder")
		}),
		Entry("a sub-agent of a project agent", func(r rig, mine projects.Project) session.Access {
			return subOf(r, projectAgent(r, mine).ID, "kid-project")
		}),
	)

	It("keeps a deeper grant inside the shared project out of the fence too", func() {
		r := newRig()
		mk(r, alice, "Acme")
		put(r, secret, "alice's project file")
		filesDir, err := r.f.Store().Stat(ctx, r.f.Actor(alice), "/users/alice/Projects/acme/files")
		Expect(err).NotTo(HaveOccurred())
		shareNode(r, filesDir.ID, workspace.ModeRead) // no grant on the project itself
		Expect(canRead(r, r.f.Actor(bob), secret)).To(BeTrue(), "bob reads it through the deeper grant")
		x := chatOf(r, folderAgent(r).ID)
		Expect(canRead(r, x.Actor(), secret)).To(BeFalse())
		Expect(sharedMounts(x)).To(BeEmpty())
		plain := chatOf(r, "")
		Expect(sharedMounts(plain)).To(HaveLen(1), "a chat without a folder keeps it")
	})

	It("keeps a folder that only looks like a project in the fence", func() {
		r := newRig()
		for _, dir := range []string{"/users/alice/projects/x", "/users/alice/Other/Projects/x"} {
			n, err := r.f.Store().Mkdir(ctx, r.f.Actor(alice), dir)
			Expect(err).NotTo(HaveOccurred(), dir)
			put(r, dir+"/f.md", "f")
			shareNode(r, n.ID, workspace.ModeRead)
		}
		x := chatOf(r, folderAgent(r).ID)
		Expect(canRead(r, x.Actor(), "/users/alice/projects/x/f.md")).To(BeTrue())
		Expect(canRead(r, x.Actor(), "/users/alice/Other/Projects/x/f.md")).To(BeTrue())
	})

	It("does not touch the project agent's own project", func() {
		r, _, mine, _ := setup()
		put2 := func(rel string) {
			_, err := r.f.Store().Put(ctx, r.f.Actor(bob), session.HomePath(bob)+"/Projects/"+mine.Slug+rel, strings.NewReader("own"), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		put2("/files/own.md")
		put2("/memory/m.md")
		x := chatOf(r, projectAgent(r, mine).ID)
		base := session.HomePath(bob) + "/Projects/" + mine.Slug
		Expect(canRead(r, x.Actor(), base+"/files/own.md")).To(BeTrue())
		Expect(canRead(r, x.Actor(), base+"/memory/m.md")).To(BeTrue())
		_, err := r.f.Store().Put(ctx, x.Actor(), base+"/files/new.md", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
	})
})
