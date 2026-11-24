package studio_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// newAgentFolderService is a service with files and saved agents.
func newAgentFolderService(f *files.Service) *studio.Service {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness(), Files: f, Agents: ag})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

var _ = Describe("agent folders", func() {
	var (
		ctx  context.Context
		f    *files.Service
		svc  *studio.Service
		ws   workspace.Store
		user workspace.Actor
	)
	home := session.HomePath(alice)
	mkdir := func(rel string) workspace.Node {
		n, err := ws.Mkdir(ctx, user, home+rel)
		Expect(err).NotTo(HaveOccurred())
		return n
	}
	BeforeEach(func() {
		ctx = context.Background()
		f = newFiles()
		svc = newAgentFolderService(f)
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		ws, user = f.Store(), f.Actor(alice)
	})

	It("stores the folder by node and answers its current path after a rename and a move", func() {
		acme := mkdir("/Projects/Acme")
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped"}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		Expect(a.FolderID).To(Equal(acme.ID))
		Expect(a.FolderMode).To(Equal(agents.FolderRead))
		st, ok := svc.AgentFolder(ctx, alice, a)
		Expect(ok).To(BeTrue())
		Expect(st).To(Equal(studio.AgentFolderState{Path: "/Projects/Acme", Name: "Acme", Mode: "read", Effective: "read", State: studio.AgentFolderOK}))

		_, err = ws.Move(ctx, user, home+"/Projects/Acme", home+"/Projects/Acme Corp")
		Expect(err).NotTo(HaveOccurred())
		st, _ = svc.AgentFolder(ctx, alice, a)
		Expect(st.Path).To(Equal("/Projects/Acme Corp"), "after a rename")
		Expect(st.Name).To(Equal("Acme Corp"))

		// A move of the parent leaves no journal row on the folder itself.
		_, err = ws.Move(ctx, user, home+"/Projects", home+"/Archive")
		Expect(err).NotTo(HaveOccurred())
		st, _ = svc.AgentFolder(ctx, alice, a)
		Expect(st.Path).To(Equal("/Archive/Acme Corp"), "after a move of the parent")
		Expect(st.State).To(Equal(studio.AgentFolderOK))
	})

	It("keeps an unnamed readable root without exposing another home's workspace path", func() {
		mkdir("/Notes")
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped"}, &studio.AgentFolderWrite{Path: "/Notes", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		otherHome := session.HomePath("other")
		_, err = ws.Mkdir(ctx, admin, otherHome)
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Grant(ctx, admin, alice, otherHome, workspace.ModeRead)).To(Succeed())
		_, err = ws.Move(ctx, admin, home+"/Notes", otherHome+"/Notes")
		Expect(err).NotTo(HaveOccurred())
		st, ok := svc.AgentFolder(ctx, alice, a)
		Expect(ok).To(BeTrue())
		Expect(st).To(Equal(studio.AgentFolderState{Name: "Notes", Mode: "read", Effective: "read", State: studio.AgentFolderOK}))
		abs, live := svc.FolderResolver().PathOf(ctx, alice, a.FolderID)
		Expect(live).To(BeTrue())
		Expect(abs).To(Equal(otherHome + "/Notes"))
		Expect(ws.Revoke(ctx, admin, alice, otherHome)).To(Succeed())
		st, _ = svc.AgentFolder(ctx, alice, a)
		Expect(st).To(Equal(studio.AgentFolderState{Mode: "read", State: studio.AgentFolderMissing}))
	})

	It("keeps the folder when a save has no folder, and clears it with an empty path", func() {
		mkdir("/Notes")
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped"}, &studio.AgentFolderWrite{Path: "/Notes", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		node := a.FolderID

		// A caller that knows nothing of folders replaces the agent.
		a2, err := svc.SaveAgent(ctx, alice, agents.Agent{ID: a.ID, Name: "Renamed"})
		Expect(err).NotTo(HaveOccurred())
		Expect(a2.Name).To(Equal("Renamed"))
		Expect(a2.FolderID).To(Equal(node), "a save without the folder widened the agent")
		Expect(a2.FolderMode).To(Equal(agents.FolderWrite))

		a3, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{ID: a.ID, Name: "Renamed"}, &studio.AgentFolderWrite{Path: ""})
		Expect(err).NotTo(HaveOccurred())
		Expect(a3.FolderID).To(BeZero())
		Expect(a3.FolderMode).To(BeEmpty())
		_, ok := svc.AgentFolder(ctx, alice, a3)
		Expect(ok).To(BeFalse(), "an agent without a folder has no folder state")
	})

	It("takes the whole home as the folder", func() {
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Reader"}, &studio.AgentFolderWrite{Path: "/", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		root, err := ws.Stat(ctx, user, home)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.FolderID).To(Equal(root.ID))
		st, _ := svc.AgentFolder(ctx, alice, a)
		Expect(st).To(Equal(studio.AgentFolderState{Path: "/", Mode: "read", Effective: "read", State: studio.AgentFolderOK}))
	})

	It("refuses a file, a missing path, Chats, LocalAI, a bad path and a bad mode as the folder", func() {
		mkdir("/Docs")
		_, err := ws.Put(ctx, user, home+"/Docs/a.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		mkdir("/" + session.SettingsDir + "/inner")
		for _, w := range []studio.AgentFolderWrite{
			{Path: "/Docs/a.txt", Mode: "read"},
			{Path: "/Nope", Mode: "read"},
			{Path: "/" + session.ChatsDir, Mode: "read"},
			{Path: "/" + session.SettingsDir, Mode: "read"},
			{Path: "/" + session.SettingsDir + "/inner", Mode: "write"},
			{Path: "/../bob", Mode: "read"},
			{Path: "/Docs", Mode: ""},
			{Path: "/Docs", Mode: "admin"},
		} {
			_, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "x"}, &w)
			Expect(err).To(MatchError(studio.ErrAgentFolder), "%+v", w)
		}
		list, err := svc.Agents(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty(), "a refused folder saved an agent")
	})

	It("cannot name a folder of another user", func() {
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		_, err := ws.Mkdir(ctx, f.Actor("bob"), session.HomePath("bob")+"/Secret")
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "x"}, &studio.AgentFolderWrite{Path: "/users/bob/Secret", Mode: "read"})
		Expect(err).To(MatchError(studio.ErrAgentFolder), "the path is read inside alice's home")
		bobs, err := ws.Stat(ctx, f.Actor("bob"), session.HomePath("bob")+"/Secret")
		Expect(err).NotTo(HaveOccurred())
		_, ok := svc.FolderResolver().PathOf(ctx, alice, bobs.ID)
		Expect(ok).To(BeFalse(), "alice resolved bob's folder")
	})

	It("answers missing for a deleted folder and ok again after undo", func() {
		mkdir("/Gone")
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped"}, &studio.AgentFolderWrite{Path: "/Gone", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Delete(ctx, user, home+"/Gone")).To(Succeed())
		st, ok := svc.AgentFolder(ctx, alice, a)
		Expect(ok).To(BeTrue())
		Expect(st).To(Equal(studio.AgentFolderState{Mode: "read", State: studio.AgentFolderMissing}))

		hist, err := ws.History(ctx, user, workspace.HistoryQuery{Path: home, Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		_, _, err = ws.Undo(ctx, user, hist[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		st, _ = svc.AgentFolder(ctx, alice, a)
		Expect(st.State).To(Equal(studio.AgentFolderOK), "after undo")
		Expect(st.Path).To(Equal("/Gone"))
	})

	It("resolves several agents with one call", func() {
		mkdir("/A")
		mkdir("/B")
		a, _ := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "a"}, &studio.AgentFolderWrite{Path: "/A", Mode: "read"})
		b, _ := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "b"}, &studio.AgentFolderWrite{Path: "/B", Mode: "write"})
		plain, _ := svc.SaveAgent(ctx, alice, agents.Agent{Name: "plain"})
		unset, err := svc.SaveAgent(ctx, alice, agents.Agent{Name: "unset", FolderMode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		got := svc.AgentFolders(ctx, alice, []agents.Agent{a, b, plain, unset})
		Expect(got).To(HaveLen(3))
		Expect(got[a.ID].Path).To(Equal("/A"))
		Expect(got[b.ID].Path).To(Equal("/B"))
		Expect(got[unset.ID]).To(Equal(studio.AgentFolderState{Mode: "read", State: studio.AgentFolderUnset}))
	})

	Describe("import", func() {
		It("resolves the folder of the file in the importer's home", func() {
			acme := mkdir("/Projects/Acme")
			a, err := svc.ImportAgent(ctx, alice, agents.Agent{Name: "Imported", FolderID: 999, FolderMode: "write"}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: "read"})
			Expect(err).NotTo(HaveOccurred())
			Expect(a.FolderID).To(Equal(acme.ID))
			Expect(a.FolderMode).To(Equal("read"))
		})

		It("saves a folder that is not there as unset, never as no folder", func() {
			for i, w := range []studio.AgentFolderWrite{{Path: "/Projects/Acme", Mode: "read"}, {Path: "", Mode: "write"}, {Path: "/" + session.ChatsDir, Mode: "read"}} {
				a, err := svc.ImportAgent(ctx, alice, agents.Agent{Name: "Imported " + string(rune('a'+i))}, &w)
				Expect(err).NotTo(HaveOccurred(), "%+v", w)
				Expect(a.FolderID).To(BeZero())
				Expect(a.FolderMode).To(Equal(w.Mode), "%+v", w)
			}
		})

		It("refuses a folder without a valid mode and ignores the node of the file", func() {
			_, err := svc.ImportAgent(ctx, alice, agents.Agent{Name: "x"}, &studio.AgentFolderWrite{Path: "/A"})
			Expect(err).To(MatchError(studio.ErrAgentFolder))
			a, err := svc.ImportAgent(ctx, alice, agents.Agent{Name: "y", FolderID: 5, FolderMode: "read"}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(a.FolderID).To(BeZero())
			Expect(a.FolderMode).To(BeEmpty())
		})
	})
})
