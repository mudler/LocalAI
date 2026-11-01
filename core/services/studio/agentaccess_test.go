package studio_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing/fstest"

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

// accessRig is a service with files, saved agents and a harness, on stores
// that a second service can share.
type accessRig struct {
	ctx  context.Context
	f    *files.Service
	st   store.Store
	ag   agents.Store
	svc  *studio.Service
	h    *studiotest.GateHarness
	ws   workspace.Store
	user workspace.Actor
	home string

	mu        sync.Mutex
	hostActor workspace.Actor // the actor of the last turn's host tools
}

func newAccessRig() *accessRig {
	r := &accessRig{ctx: context.Background(), f: newFiles(), home: session.HomePath(alice)}
	var err error
	r.st, err = store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.ag, err = agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.svc, r.h = r.service()
	Expect(r.f.EnsureHome(r.ctx, alice)).To(Succeed())
	Expect(r.f.SyncBuiltin(r.ctx, fstest.MapFS{
		"pdf/SKILL.md": {Data: []byte("---\nname: pdf\ndescription: PDF.\n---\nbody\n")},
	})).To(Succeed())
	r.ws, r.user = r.f.Store(), r.f.Actor(alice)
	return r
}

// service is one more service on the rig's stores, as a second frontend
// would be.
func (r *accessRig) service() (*studio.Service, *studiotest.GateHarness) {
	h := studiotest.NewGateHarness()
	svc, err := studio.New(studio.Deps{Store: r.st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Files: r.f, Agents: r.ag,
		HostTools: func(_ context.Context, a workspace.Actor) ([]session.Tool, error) {
			r.mu.Lock()
			r.hostActor = a
			r.mu.Unlock()
			return nil, nil
		}})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return svc, h
}

func (r *accessRig) mkdir(rel string) workspace.Node {
	GinkgoHelper()
	n, err := r.ws.Mkdir(r.ctx, r.user, r.home+rel)
	Expect(err).NotTo(HaveOccurred())
	return n
}

func (r *accessRig) put(rel, content string) {
	GinkgoHelper()
	_, err := r.ws.Put(r.ctx, r.user, r.home+rel, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func (r *accessRig) stat(abs string) workspace.Node {
	GinkgoHelper()
	n, err := r.ws.Stat(r.ctx, files.UserActor(alice), abs)
	Expect(err).NotTo(HaveOccurred(), "stat %s", abs)
	return n
}

// agent saves an agent with a folder of the home (empty: none).
func (r *accessRig) agent(folder, mode string) agents.Agent {
	GinkgoHelper()
	var w *studio.AgentFolderWrite
	if folder != "" {
		w = &studio.AgentFolderWrite{Path: folder, Mode: mode}
	}
	a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Agent " + folder + mode}, w)
	Expect(err).NotTo(HaveOccurred())
	return a
}

func (r *accessRig) chat(agentID string) session.ChatRecord {
	GinkgoHelper()
	rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: agentID})
	Expect(err).NotTo(HaveOccurred())
	return rec
}

func (r *accessRig) access(conv string) session.Access {
	GinkgoHelper()
	x, err := r.svc.ChatAccess(r.ctx, alice, conv)
	Expect(err).NotTo(HaveOccurred())
	return x
}

// turn runs one turn of the chat on svc and returns the actor its harness
// and its host tools had.
func (r *accessRig) turn(svc *studio.Service, h *studiotest.GateHarness, conv string) (env, host workspace.Actor) {
	GinkgoHelper()
	before := len(h.Sent())
	sent(svc, alice, conv, "hi")
	until("the turn to start", func() bool { return len(h.Sent()) == before+1 })
	h.Gate <- struct{}{}
	idle(svc, alice, conv)
	r.mu.Lock()
	defer r.mu.Unlock()
	return h.Env().Actor, r.hostActor
}

// readOnlyRoots are the skill layers that exist, then the given folders, all
// read only: what a scoped chat may read besides its own folders.
func (r *accessRig) readOnlyRoots(shared ...string) []workspace.FenceRoot {
	var out []workspace.FenceRoot
	for _, p := range append([]string{files.SharedSkillsPath, files.BuiltinSkillsPath, files.AddonSkillsPath}, shared...) {
		if n, err := r.ws.Stat(r.ctx, files.UserActor(alice), p); err == nil {
			out = append(out, workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeRead})
		}
	}
	return out
}

func chatDir(r *accessRig, conv string) workspace.Node {
	GinkgoHelper()
	rec, err := r.svc.Get(r.ctx, alice, conv)
	Expect(err).NotTo(HaveOccurred())
	Expect(rec.Folder).NotTo(BeEmpty(), "the chat has a folder name")
	return r.stat(session.ChatsPath(alice) + "/" + rec.Folder)
}

func fenceOf(a workspace.Actor) []workspace.FenceRoot {
	roots, ok := a.Fence()
	Expect(ok).To(BeTrue(), "the actor is fenced")
	return roots
}

var _ = Describe("chat access", func() {
	var r *accessRig
	BeforeEach(func() { r = newAccessRig() })

	It("gives a chat without an agent folder today's actor and view", func() {
		team, err := r.svc.SharedFolders().Create(r.ctx, "", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.SharedFolders().SetMember(r.ctx, "", team.ID, files.EveryoneGroup, workspace.ModeRead)
		Expect(err).NotTo(HaveOccurred())
		for _, agentID := range []string{"", r.agent("", "").ID} {
			rec := r.chat(agentID)
			x := r.access(rec.ID)
			want := files.UserActor(alice)
			want.ChatID = rec.ID
			Expect(x.Actor()).To(Equal(want))
			Expect(x.Scoped()).To(BeFalse())
			shared, err := r.svc.SharedFolders().Roots(r.ctx, want)
			Expect(err).NotTo(HaveOccurred())
			v, err := session.BuildView(r.ctx, r.ws, want, r.home, "", shared, slog.Default())
			Expect(err).NotTo(HaveOccurred())
			Expect(x.View().Mounts).To(Equal(v.Mounts))
			Expect(x.View().Mounts).To(HaveLen(2), "the home and the shared folder")
			got, err := r.svc.Get(r.ctx, alice, rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.ScopedAgent).To(BeFalse())
		}
	})

	It("fences a chat whose agent has a folder", func() {
		team, err := r.svc.SharedFolders().Create(r.ctx, "", "Team")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.SharedFolders().SetMember(r.ctx, "", team.ID, files.EveryoneGroup, workspace.ModeWrite)
		Expect(err).NotTo(HaveOccurred())
		acme := r.mkdir("/Projects/Acme")
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderRead).ID)
		Expect(rec.ScopedAgent).To(BeTrue(), "CreateAs marks the chat")
		x := r.access(rec.ID)
		Expect(x.Scoped()).To(BeTrue())
		Expect(x.Actor().ChatID).To(Equal(rec.ID))
		dir := chatDir(r, rec.ID)
		want := append([]workspace.FenceRoot{
			{Node: acme.ID, Mode: workspace.ModeRead},
			{Node: dir.ID, Mode: workspace.ModeWrite},
		}, r.readOnlyRoots("/shared/Team")...)
		Expect(fenceOf(x.Actor())).To(Equal(want))
		Expect(len(want)).To(BeNumerically(">=", 4), "a skill layer and the shared folder are in the fence")
		teamNode := r.stat(team.Path)
		Expect(x.View().Mounts).To(HaveLen(3), "the folder, the chat folder and the shared folder")
		Expect(x.View().Mounts[0]).To(mountOf(acme.ID, r.home+"/Projects/Acme", session.GuestHome, workspace.ModeRead))
		Expect(x.View().Mounts[1]).To(mountOf(dir.ID, dir.Path, session.GuestChat, workspace.ModeWrite))
		Expect(x.View().Mounts[2]).To(mountOf(teamNode.ID, team.Path, session.GuestSharedPrefix+"Team", workspace.ModeRead), "read only, although the grant allows writes")

		// The store holds the fence: the shared folder is read only although
		// its grant allows writes, the home and the rest of it are hidden.
		a := x.Actor()
		_, err = r.ws.Put(r.ctx, a, "/shared/Team/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrReadOnly))
		_, err = r.ws.Stat(r.ctx, a, r.home)
		Expect(err).To(MatchError(workspace.ErrNotFound))
		_, err = r.ws.Put(r.ctx, a, r.home+"/Projects/Acme/new.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrReadOnly), "the folder is read only")
		_, err = r.ws.Stat(r.ctx, x.Owner(), r.home)
		Expect(err).NotTo(HaveOccurred(), "the owner's actor has no fence")
	})

	It("creates the chat folder before it fences", func() {
		r.mkdir("/Notes")
		rec := r.chat(r.agent("/Notes", agents.FolderWrite).ID)
		Expect(rec.Folder).To(BeEmpty(), "a new chat has no folder yet")
		x := r.access(rec.ID)
		dir := chatDir(r, rec.ID)
		Expect(dir.Kind).To(Equal(workspace.KindDir))
		_, err := r.ws.Put(r.ctx, x.Actor(), dir.Path+"/work/out.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred(), "the chat folder is read and write")
		_, err = r.ws.Put(r.ctx, x.Actor(), r.home+"/Notes/n.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred(), "the folder is read and write")
		_, err = r.ws.Put(r.ctx, x.Actor(), session.ChatsPath(alice)+"/other/x.txt", strings.NewReader("x"), workspace.PutOptions{})
		Expect(err).To(MatchError(workspace.ErrNotFound), "another chat's folder")

		// The second resolve finds the same folder.
		again := r.access(rec.ID)
		Expect(fenceOf(again.Actor())).To(Equal(fenceOf(x.Actor())))
	})

	It("refuses a chat whose folder is deleted and never returns the home", func() {
		r.mkdir("/Projects/Acme")
		r.put("/Projects/Acme/a.txt", "a")
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderWrite).ID)
		r.access(rec.ID)
		Expect(r.ws.Delete(r.ctx, r.user, r.home+"/Projects")).To(Succeed())
		_, err := r.svc.ChatAccess(r.ctx, alice, rec.ID)
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		_, err = r.svc.Send(r.ctx, alice, rec.ID, "hi")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing), "the turn does not start")
		Expect(r.h.Sent()).To(BeEmpty())

		// Restored from the Trash, the same node works again.
		hist, err := r.ws.History(r.ctx, r.user, workspace.HistoryQuery{Path: r.home, Limit: 1})
		Expect(err).NotTo(HaveOccurred())
		_, _, err = r.ws.Undo(r.ctx, r.user, hist[0].Rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.access(rec.ID).Scoped()).To(BeTrue())
	})

	It("refuses a chat whose scoped agent was deleted", func() {
		r.mkdir("/Notes")
		ag := r.agent("/Notes", agents.FolderRead)
		rec := r.chat(ag.ID)
		Expect(r.svc.DeleteAgent(r.ctx, alice, ag.ID)).To(Succeed())
		_, err := r.svc.ChatAccess(r.ctx, alice, rec.ID)
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		_, err = r.svc.Send(r.ctx, alice, rec.ID, "hi")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
	})

	It("keeps today's rule for a deleted agent that had no folder", func() {
		ag := r.agent("", "")
		rec := r.chat(ag.ID)
		Expect(r.svc.DeleteAgent(r.ctx, alice, ag.ID)).To(Succeed())
		x := r.access(rec.ID)
		Expect(x.Scoped()).To(BeFalse())
		r.turn(r.svc, r.h, rec.ID)
	})

	It("refuses CreateAs with an unset or a missing folder", func() {
		unset, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Unset", FolderMode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: unset.ID})
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))

		r.mkdir("/Gone")
		gone := r.agent("/Gone", agents.FolderRead)
		Expect(r.ws.Delete(r.ctx, r.user, r.home+"/Gone")).To(Succeed())
		_, err = r.svc.CreateAs(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: gone.ID}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		list, err := r.svc.List(r.ctx, alice, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty(), "no chat was created")
	})

	It("applies an agent edit at the next turn", func() {
		acme := r.mkdir("/Projects/Acme")
		ag := r.agent("/Projects/Acme", agents.FolderRead)
		rec := r.chat(ag.ID)
		env, host := r.turn(r.svc, r.h, rec.ID)
		Expect(fenceOf(env)[0]).To(Equal(workspace.FenceRoot{Node: acme.ID, Mode: workspace.ModeRead}))
		Expect(host.SameFence(env)).To(BeTrue(), "the turn's tools have the chat's fence")
		starts := r.h.Starts()

		_, err := r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		env, host = r.turn(r.svc, r.h, rec.ID)
		Expect(fenceOf(env)[0]).To(Equal(workspace.FenceRoot{Node: acme.ID, Mode: workspace.ModeWrite}))
		Expect(host.SameFence(env)).To(BeTrue())
		Expect(r.h.Starts()).To(BeNumerically(">", starts), "the harness started again with the new access")

		// Without the folder the chat works on the whole home again.
		_, err = r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: ""})
		Expect(err).NotTo(HaveOccurred())
		env, _ = r.turn(r.svc, r.h, rec.ID)
		_, fenced := env.Fence()
		Expect(fenced).To(BeFalse())
		got, err := r.svc.Get(r.ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ScopedAgent).To(BeFalse(), "the mark follows the agent")
	})

	It("applies an edit made through another service instance", func() {
		acme := r.mkdir("/Projects/Acme")
		other := r.mkdir("/Other")
		ag := r.agent("/Projects/Acme", agents.FolderWrite)
		rec := r.chat(ag.ID)
		env, _ := r.turn(r.svc, r.h, rec.ID)
		Expect(fenceOf(env)[0].Node).To(Equal(acme.ID))

		svc2, h2 := r.service()
		_, err := svc2.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Other", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		env, _ = r.turn(r.svc, r.h, rec.ID)
		Expect(fenceOf(env)[0]).To(Equal(workspace.FenceRoot{Node: other.ID, Mode: workspace.ModeRead}), "the first instance sees the edit")

		Expect(svc2.DeleteAgent(r.ctx, alice, ag.ID)).To(Succeed())
		_, err = r.svc.Send(r.ctx, alice, rec.ID, "hi")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing), "the first instance sees the delete")
		_, err = svc2.Send(r.ctx, alice, rec.ID, "hi")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		Expect(h2.Sent()).To(BeEmpty())
	})

	It("renames the chat folder and keeps it in the fence", func() {
		r.mkdir("/Notes")
		rec := r.chat(r.agent("/Notes", agents.FolderRead).ID)
		r.turn(r.svc, r.h, rec.ID)
		before := chatDir(r, rec.ID)
		title := "A new name"
		_, err := r.svc.Update(r.ctx, alice, rec.ID, studio.UpdateInput{Title: &title})
		Expect(err).NotTo(HaveOccurred())
		after := chatDir(r, rec.ID)
		Expect(after.ID).To(Equal(before.ID), "the same folder")
		Expect(after.Path).NotTo(Equal(before.Path), "under a new name")
		x := r.access(rec.ID)
		Expect(fenceOf(x.Actor())).To(ContainElement(workspace.FenceRoot{Node: after.ID, Mode: workspace.ModeWrite}))
		Expect(x.View().Mounts[1].Path).To(Equal(after.Path))
	})

	It("gives a generic sub-agent its parent's folder and chat folder", func() {
		acme := r.mkdir("/Projects/Acme")
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderRead).ID)
		x := r.access(rec.ID)
		err := r.st.CreateChat(r.ctx, session.ChatRecord{ID: rec.ID + "-sub", Principal: alice, Title: "Sub-agent", Kind: session.KindSubAgent, ParentID: rec.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		cx := r.access(rec.ID + "-sub")
		Expect(cx.Actor().ChatID).To(Equal(rec.ID + "-sub"))
		Expect(fenceOf(cx.Actor())).To(Equal(fenceOf(x.Actor())))
		Expect(fenceOf(cx.Actor())[0].Node).To(Equal(acme.ID))
	})
})

var _ = Describe("chat access in a turn", func() {
	It("gives the file tools of a scoped chat only its folders", func() {
		f := newFiles()
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text != "go" {
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			return []studiotest.ToolStep{
				{Tool: "read_file", Args: `{"path":"/a.txt"}`},
				{Tool: "read_file", Args: `{"path":"/workspace/home/../../Other/o.txt"}`},
				{Tool: "write_file", Args: `{"path":"/new.txt","content":"x"}`},
				{Say: "done"},
			}
		}, func(d *studio.Deps) { d.Files = f; d.Agents = ag })
		ctx := context.Background()
		Expect(f.EnsureHome(ctx, alice)).To(Succeed())
		home := session.HomePath(alice)
		for p, c := range map[string]string{"/Projects/Acme/a.txt": "inside", "/Other/o.txt": "outside"} {
			_, err := f.Store().Put(ctx, f.Actor(alice), home+p, strings.NewReader(c), workspace.PutOptions{})
			Expect(err).NotTo(HaveOccurred())
		}
		a, err := svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Reader"}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, rec.ID, "go")
		idle(svc, alice, rec.ID)

		reads := h.ResultsFor("read_file")
		Expect(reads).To(HaveLen(2))
		Expect(reads[0].Err).NotTo(HaveOccurred())
		Expect(reads[0].Out).To(ContainSubstring("inside"))
		Expect(reads[1].Out+fmt.Sprint(reads[1].Err)).NotTo(ContainSubstring("outside"), "a file outside the folder")
		Expect(h.ResultsFor("write_file")).To(HaveLen(1))
		_, err = f.Store().Stat(ctx, f.Actor(alice), home+"/Projects/Acme/new.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "a read-only folder takes no new file")
	})
})

// mountOf matches a mount by its node, path, mount point and mode.
func mountOf(node int64, p, at string, m workspace.Mode) OmegaMatcher {
	return Equal(workspace.Mount{Path: p, At: at, Mode: m, NodeID: node})
}
