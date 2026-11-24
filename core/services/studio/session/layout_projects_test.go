package session_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the project mounts of a chat's view", func() {
	ctx := context.Background()
	alice := workspace.Actor{Principal: "alice", ChatID: "c1"}

	// rig makes alice's project folders under her home.
	mkdirs := func(r *layoutRig, paths ...string) {
		for _, p := range paths {
			_, err := r.ws.Mkdir(ctx, r.admin, session.HomePath("alice")+p)
			Expect(err).NotTo(HaveOccurred())
		}
	}
	req := func(p, at string, m workspace.Mode) workspace.MountRequest {
		return workspace.MountRequest{Path: session.HomePath("alice") + p, At: at, Mode: m}
	}
	role := func(r workspace.MountRequest, role string) workspace.MountRequest {
		r.Role = role
		return r
	}

	It("mounts the project and its memory read and write, and a pulled project read only", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files", "/P/memory", "/Q", "/Q/files", "/Q/memory")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			req("/P/files", session.GuestProjectName, workspace.ModeWrite),
			req("/P/memory", session.GuestProjectName+"-memory", workspace.ModeWrite),
			req("/Q/files", session.GuestPulledPrefix+"q", workspace.ModeRead),
			req("/Q/memory", session.GuestPulledPrefix+"q-memory", workspace.ModeRead))
		Expect(err).NotTo(HaveOccurred())
		p, ok := mountAt(v, "project")
		Expect(ok).To(BeTrue())
		Expect(p.Mode).To(Equal(workspace.ModeWrite))
		Expect(p.Path).To(Equal(session.HomePath("alice") + "/P/files"))
		m, ok := mountAt(v, "project-memory")
		Expect(ok).To(BeTrue())
		Expect(m.Mode).To(Equal(workspace.ModeWrite))
		q, ok := mountAt(v, "pulled-q")
		Expect(ok).To(BeTrue())
		Expect(q.Mode).To(Equal(workspace.ModeRead))
		_, ok = mountAt(v, "pulled-q-memory")
		Expect(ok).To(BeTrue())
		home, ok := mountAt(v, session.GuestHome)
		Expect(ok).To(BeTrue())
		Expect(home.Path).To(Equal(session.HomePath("alice")), "the home mount stays")

		pl := session.NewPlace(v)
		t, err := pl.Resolve("/workspace/pulled-q/notes.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Write).To(BeFalse(), "a write into a pulled project is refused")
		t, err = pl.Resolve("/workspace/project/notes.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Write).To(BeTrue())
	})

	It("keeps the names unique against the other mounts", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files", "/P/memory", "/X", "/Y")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), []string{session.HomePath("alice") + "/Other"}, r.chat.Path, layoutShared, quiet,
			req("/P/files", session.GuestHome, workspace.ModeWrite),
			req("/X", session.GuestChat, workspace.ModeWrite),
			req("/Y", "shared-team", workspace.ModeRead),
			req("/P/memory", "folder-Other", workspace.ModeRead))
		Expect(err).NotTo(HaveOccurred())
		ats := map[string]int{}
		for _, m := range v.Mounts {
			ats[workspace.FoldName(m.At)]++
		}
		for at, n := range ats {
			Expect(n).To(Equal(1), at)
		}
		home, _ := mountAt(v, session.GuestHome)
		Expect(home.Path).To(Equal(session.HomePath("alice")))
		c, _ := mountAt(v, session.GuestChat)
		Expect(c.Path).To(Equal(r.chat.Path))
	})

	It("skips a project folder that is missing and still builds the view", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			req("/P/files", session.GuestProjectName, workspace.ModeWrite),
			req("/P/memory", session.GuestProjectName+"-memory", workspace.ModeWrite))
		Expect(err).NotTo(HaveOccurred())
		_, ok := mountAt(v, "project")
		Expect(ok).To(BeTrue())
		_, ok = mountAt(v, "project-memory")
		Expect(ok).To(BeFalse())
	})

	It("does not pass a project folder inside the home through the shared-root skip", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			req("/P/files", session.GuestProjectName, workspace.ModeWrite))
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Mounts).To(HaveLen(2))
	})

	It("describes the project mounts in the prompt", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files", "/P/memory", "/Q", "/Q/files", "/Q/memory")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			role(req("/P/files", session.GuestProjectName, workspace.ModeWrite), workspace.RoleProjectFiles),
			role(req("/P/memory", session.GuestProjectName+"-memory", workspace.ModeRead), workspace.RoleProjectMemory),
			role(req("/Q/files", session.GuestPulledPrefix+"q", workspace.ModeRead), workspace.RolePulledFiles),
			role(req("/Q/memory", session.GuestPulledPrefix+"q-memory", workspace.ModeRead), workspace.RolePulledMemory))
		Expect(err).NotTo(HaveOccurred())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(ContainSubstring("- /workspace/project: the files of this chat's project (read and write)."))
		Expect(p).To(ContainSubstring("- /workspace/project-memory: the memory of this chat's project (read only)."))
		Expect(p).To(ContainSubstring("propose_change"), "the model is told how a note is saved")
		Expect(p).NotTo(ContainSubstring("project-memory: the memory of this chat's project (read and write)"))
		Expect(p).To(ContainSubstring("- /workspace/pulled-q: the files of another project, for reading (read only)."))
		Expect(p).To(ContainSubstring("- /workspace/pulled-q-memory: the memory of another project, for reading (read only)."))
		Expect(p).NotTo(ContainSubstring("a folder shared with the user"))
	})

	It("tells the model not to change the memory of a shared project, also through the shared folder", func() {
		r := newLayoutRig()
		mkdirs(r, "/S", "/S/files", "/S/memory")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			role(req("/S/files", session.GuestPulledPrefix+"s", workspace.ModeWrite), workspace.RoleSharedFiles),
			role(req("/S/memory", session.GuestPulledPrefix+"s-memory", workspace.ModeRead), workspace.RoleSharedMemory))
		Expect(err).NotTo(HaveOccurred())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(ContainSubstring("for reading (read only). Do not change it, also not through the shared folder; propose a note instead."))
	})

	It("describes a mount by the role it was asked for, never by its name", func() {
		r := newLayoutRig()
		mkdirs(r, "/P", "/P/files", "/P/memory", "/Q", "/Q/files", "/Q/memory", "/Z")
		v, err := session.BuildViewFolders(ctx, r.ws, alice, session.HomePath("alice"), nil, "", nil, quiet,
			// A folder with no role that is named like a project mount.
			req("/Z", session.GuestProjectName, workspace.ModeWrite),
			req("/Q/memory", session.GuestPulledPrefix+"x", workspace.ModeRead),
			// A pulled project whose slug ends in -memory: its files are files.
			role(req("/Q/files", session.GuestPulledPrefix+"notes-memory", workspace.ModeRead), workspace.RolePulledFiles),
			role(req("/P/memory", session.GuestPulledPrefix+"notes-memory-memory", workspace.ModeRead), workspace.RolePulledMemory))
		Expect(err).NotTo(HaveOccurred())
		p := session.LayoutPrompt(v, session.PromptScope(v))
		Expect(p).To(ContainSubstring("- /workspace/project: a folder shared with the user"))
		Expect(p).To(ContainSubstring("- /workspace/pulled-x: a folder shared with the user"))
		Expect(p).To(ContainSubstring("- /workspace/pulled-notes-memory: the files of another project, for reading"))
		Expect(p).To(ContainSubstring("- /workspace/pulled-notes-memory-memory: the memory of another project, for reading"))
	})

	It("adds the notes of the view to the prompt", func() {
		v := workspace.View{Notes: []string{"One pulled project is not available this turn."}}
		Expect(session.LayoutPrompt(v, session.ScopedPrompt{})).To(ContainSubstring("One pulled project is not available this turn."))
	})
})
