package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// listCounter counts the lists of the project index: a reindex reads it once
// per pass.
type listCounter struct {
	projects.Store
	n atomic.Int32
}

func (c *listCounter) List(ctx context.Context, owner string) ([]projects.Project, error) {
	c.n.Add(1)
	return c.Store.List(ctx, owner)
}

// readCounters counts the list reads of the stores that a project listing
// may touch, so a spec shows that one route reads only its own store.
type readCounters struct {
	bindings, triggers atomic.Int32
}

type countBindings struct {
	connectors.Store
	c *readCounters
}

func (s countBindings) ListBindings(ctx context.Context, owner string) ([]connectors.Binding, error) {
	s.c.bindings.Add(1)
	return s.Store.ListBindings(ctx, owner)
}

type countTriggers struct {
	triggers.Store
	c *readCounters
}

func (s countTriggers) ListTriggers(ctx context.Context, owner string) ([]triggers.Trigger, error) {
	s.c.triggers.Add(1)
	return s.Store.ListTriggers(ctx, owner)
}

// flakyAgents fails the reads of one agent while down is set.
type flakyAgents struct {
	agents.Store
	down atomic.Bool
}

func (f *flakyAgents) Get(ctx context.Context, owner, id string) (agents.Agent, error) {
	if f.down.Load() {
		return agents.Agent{}, errors.New("agent store is down")
	}
	return f.Store.Get(ctx, owner, id)
}

// projRig is a service with files, projects, saved agents, triggers and a
// connector, whose turns run the tool calls a spec gives for each message.
type projRig struct {
	*connRig
	ctx   context.Context
	proj  *projects.Service
	lists *listCounter
	home  string
	user  workspace.Actor
	reads *readCounters
	flaky *flakyAgents

	mu    sync.Mutex
	steps map[string][]studiotest.ToolStep
}

func newProjRig() *projRig {
	r := &projRig{ctx: context.Background(), home: session.HomePath(alice), steps: map[string][]studiotest.ToolStep{}}
	sh := newShared()
	r.reads = &readCounters{}
	r.flaky = &flakyAgents{Store: sh.agents}
	sh.agents = r.flaky
	sh.trg = countTriggers{Store: sh.trg, c: r.reads}
	sh.deps = func(d *studio.Deps) {
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		r.lists = &listCounter{Store: ps}
		r.proj = projects.NewService(projects.Options{Store: r.lists, Files: d.Files})
		d.Projects = r.proj
	}
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		r.mu.Lock()
		defer r.mu.Unlock()
		for k, s := range r.steps {
			if text == k || strings.HasSuffix(text, "\n"+k) || strings.HasSuffix(text, " "+k) {
				return append(s, studiotest.ToolStep{Say: "done " + k})
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	r.connRig = newConnRig(sh, countBindings{Store: connStore(), c: r.reads}, newScriptedBot(), script, nil)
	Expect(sh.files.EnsureHome(r.ctx, alice)).To(Succeed())
	r.user = sh.files.Actor(alice)
	r.put("/AGENTS.md", "home rules "+outsideMark)
	r.put("/Other/secret.txt", "secret "+outsideMark)
	return r
}

func (r *projRig) put(rel, content string) {
	GinkgoHelper()
	_, err := r.sh.files.Store().Put(r.ctx, r.user, r.home+rel, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred(), "put %s", rel)
}

func (r *projRig) exists(rel string) bool {
	_, err := r.sh.files.Store().Stat(r.ctx, r.user, r.home+rel)
	return err == nil
}

// project makes a project with a file in its files folder.
func (r *projRig) project(name string) projects.Project {
	GinkgoHelper()
	p, err := r.proj.Create(r.ctx, alice, projects.CreateInput{Name: name})
	Expect(err).NotTo(HaveOccurred())
	r.put("/Projects/"+p.Slug+"/files/a.txt", "inside "+p.Slug)
	return p
}

// agentOf saves a project agent that takes its folders from the project.
func (r *projRig) agentOf(p projects.Project, name string) agents.Agent {
	GinkgoHelper()
	a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: name, Model: "m", ProjectID: p.ID, Builtins: []string{"act.files"}})
	Expect(err).NotTo(HaveOccurred())
	return a
}

func (r *projRig) chat(agentID string) session.ChatRecord {
	GinkgoHelper()
	rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: agentID})
	Expect(err).NotTo(HaveOccurred())
	return rec
}

func (r *projRig) run(conv, text string, steps ...studiotest.ToolStep) []studiotest.ToolResult {
	GinkgoHelper()
	r.mu.Lock()
	r.steps[text] = steps
	r.mu.Unlock()
	before, turns := len(r.h.Results()), len(r.h.Sent(conv))
	sent(r.svc, alice, conv, text)
	until("the turn to end", func() bool {
		return len(r.h.Sent(conv)) > turns && !turn(r.svc, alice, conv).Running
	})
	return r.h.Results()[before:]
}

func (r *projRig) actor(conv string) workspace.Actor {
	GinkgoHelper()
	x, err := r.svc.ChatAccess(r.ctx, alice, conv)
	Expect(err).NotTo(HaveOccurred())
	return x.Actor()
}

func (r *projRig) stat(a workspace.Actor, rel string) error {
	_, err := r.sh.files.Store().Stat(r.ctx, a, r.home+rel)
	return err
}

func (r *projRig) write(a workspace.Actor, rel string) error {
	_, err := r.sh.files.Store().Put(r.ctx, a, r.home+rel, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
	return err
}

func (r *projRig) fence(p projects.Project) (projects.FenceNode, projects.FenceNode) {
	GinkgoHelper()
	f, m, err := r.proj.FenceRoots(r.ctx, alice, p.ID)
	Expect(err).NotTo(HaveOccurred())
	return f, m
}

var _ = Describe("a saved agent of a project: save", func() {
	var (
		r    *projRig
		acme projects.Project
		beta projects.Project
	)
	BeforeEach(func() {
		r = newProjRig()
		acme, beta = r.project("Acme"), r.project("Beta")
	})

	It("fills an empty list with the files folder at write and the memory folder at read", func() {
		a := r.agentOf(acme, "Helper")
		files, memory := r.fence(acme)
		Expect(a.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: memory.Node, Mode: agents.FolderRead}}))
		Expect(files.Path).To(Equal("/Projects/acme/files"))
		Expect(memory.Path).To(Equal("/Projects/acme/memory"))
		// Clearing the folders on a replace fills them again.
		empty := []studio.AgentFolderWrite{}
		again, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Helper"}, &empty)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Folders).To(Equal(a.Folders))
	})

	It("refuses a folder outside the project, by path and by node", func() {
		_, bFiles := r.fence(beta)
		files, _ := r.fence(acme)
		for _, path := range []string{"/Other", "/", "/Projects/acme", "/Projects/acme/files/sub", "/Projects/beta/files", "/Projects/acme/files/../../beta/files"} {
			r.mkdirs(path)
			rows := []studio.AgentFolderWrite{{Path: path, Mode: agents.FolderRead}}
			_, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: "X" + path, ProjectID: acme.ID}, &rows)
			Expect(err).To(HaveOccurred(), "folder %s", path)
		}
		rows := []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderWrite}, {Path: "/Other", Mode: agents.FolderRead}}
		_, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: "Two", ProjectID: acme.ID}, &rows)
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		// A record that names nodes is held to the same rule.
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Nodes", ProjectID: acme.ID,
			Folders: []agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: bFiles.Node, Mode: agents.FolderRead}}})
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Legacy", ProjectID: acme.ID, FolderID: bFiles.Node, FolderMode: agents.FolderRead})
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		// An existing agent cannot be widened by a replace either.
		a := r.agentOf(acme, "Helper")
		wide := []studio.AgentFolderWrite{{Path: "/Other", Mode: agents.FolderWrite}}
		_, err = r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Helper"}, &wide)
		Expect(err).To(HaveOccurred())
		got, _ := r.svc.Agent(r.ctx, alice, a.ID)
		Expect(got.Folders).To(Equal(a.Folders), "a refused save changes nothing")
	})

	It("lowers a write on the memory folder to read and keeps the files folder mode", func() {
		rows := []studio.AgentFolderWrite{{Path: "/Projects/acme/memory", Mode: agents.FolderWrite}, {Path: "/Projects/acme/files", Mode: agents.FolderRead}}
		a, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: "Clamped", ProjectID: acme.ID}, &rows)
		Expect(err).NotTo(HaveOccurred())
		files, memory := r.fence(acme)
		Expect(a.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderRead}, {Node: memory.Node, Mode: agents.FolderRead}}))
	})

	It("needs the files folder and adds the memory folder at read", func() {
		memOnly := []studio.AgentFolderWrite{{Path: "/Projects/acme/memory", Mode: agents.FolderRead}}
		_, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: "Mem", ProjectID: acme.ID}, &memOnly)
		Expect(err).To(MatchError(studio.ErrAgentFolder))
		filesOnly := []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderWrite}}
		a, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{Name: "Files", ProjectID: acme.ID}, &filesOnly)
		Expect(err).NotTo(HaveOccurred())
		_, memory := r.fence(acme)
		Expect(a.Folders).To(HaveLen(2))
		Expect(a.Folders[1]).To(Equal(agents.AgentFolder{Node: memory.Node, Mode: agents.FolderRead}))
	})

	It("refuses a save for an archived, deleted or unknown project, and for another user's", func() {
		a := r.agentOf(acme, "Helper")
		_, err := r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Renamed"})
		Expect(err).To(MatchError(studio.ErrProjectUnavailable), "an archived project takes no save")
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "New", ProjectID: acme.ID})
		Expect(err).To(MatchError(agents.ErrInvalid), "an archived project takes no new agent")
		Expect(r.proj.Delete(r.ctx, alice, beta.ID)).To(Succeed())
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "New", ProjectID: beta.ID})
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = r.svc.SaveAgent(r.ctx, bob, agents.Agent{Name: "Theirs", ProjectID: acme.ID})
		Expect(err).To(MatchError(projects.ErrNotFound), "the project of another user")
	})
})

func (r *projRig) mkdirs(rel string) {
	_, err := r.sh.files.Store().Mkdir(r.ctx, r.user, r.home+"/"+strings.Trim(strings.ReplaceAll(rel, "..", "x"), "/"))
	_ = err
}

var _ = Describe("a chat of a project agent: the fence", func() {
	var (
		r    *projRig
		acme projects.Project
		beta projects.Project
	)
	BeforeEach(func() {
		r = newProjRig()
		acme, beta = r.project("Acme"), r.project("Beta")
		r.put("/Projects/acme/files/sub/s.txt", "inside-sub")
		r.put("/Projects/beta/files/b.txt", "beta-secret "+outsideMark)
	})

	It("reads and writes the project's files, reads the memory and reaches nothing else", func() {
		a := r.agentOf(acme, "Helper")
		rec := r.chat(a.ID)
		act := r.actor(rec.ID)
		Expect(r.stat(act, "/Projects/acme/files/a.txt")).To(Succeed())
		Expect(r.write(act, "/Projects/acme/files/new.txt")).To(Succeed())
		Expect(r.exists("/Projects/acme/files/new.txt")).To(BeTrue())
		Expect(r.stat(act, "/Projects/acme/memory/index.md")).To(Succeed(), "the memory is read")
		Expect(r.write(act, "/Projects/acme/memory/note.md")).To(HaveOccurred(), "the memory is never written")
		Expect(r.write(act, "/Projects/acme/memory/index.md")).To(HaveOccurred())
		Expect(r.exists("/Projects/acme/memory/note.md")).To(BeFalse())
		for _, p := range []string{"/AGENTS.md", "/Other/secret.txt", "/Projects/beta/files/b.txt", "/Projects/beta/memory/index.md",
			"/Projects/acme/project.json", "/Projects/acme", "/Projects", "/"} {
			Expect(r.stat(act, p)).To(HaveOccurred(), "%s is outside the fence", p)
		}
		Expect(r.write(act, "/Projects/acme/project.json")).To(HaveOccurred(), "the manifest is not the agent's")
		Expect(r.write(act, "/escape.txt")).To(HaveOccurred())
		Expect(r.write(act, "/Projects/beta/files/new.txt")).To(HaveOccurred())
	})

	It("holds for the file tools of the chat, by the paths of its place", func() {
		a := r.agentOf(acme, "Helper")
		rec := r.chat(a.ID)
		escapes := []string{"/Other/secret.txt", "../Other/secret.txt", "../beta/files/b.txt", "/workspace/home/../Other/secret.txt",
			"/Projects/beta/files/b.txt", "/workspace/users/alice/Other/secret.txt"}
		steps := []studiotest.ToolStep{call("read_file", `{"path":"/a.txt"}`), call("write_file", `{"path":"/made.txt","content":"made"}`)}
		for _, p := range escapes {
			steps = append(steps, call("read_file", fmt.Sprintf(`{"path":%q}`, p)), call("list_files", fmt.Sprintf(`{"path":%q}`, p)))
		}
		steps = append(steps, call("search_files", `{"query":"`+outsideMark+`"}`), call("list_files", `{"path":"/","pattern":"*"}`))
		res := r.run(rec.ID, "go", steps...)
		Expect(res).To(HaveLen(len(steps)))
		Expect(res[0].Out).To(ContainSubstring("inside acme"))
		Expect(res[1].Err).NotTo(HaveOccurred(), outOf(res[1]))
		Expect(r.exists("/Projects/acme/files/made.txt")).To(BeTrue())
		for i, x := range res {
			Expect(outOf(x)).NotTo(ContainSubstring(outsideMark), "%s %s", x.Tool, steps[i].Args)
			Expect(outOf(x)).NotTo(ContainSubstring("home rules"))
		}
		Expect(res[len(res)-1].Out).NotTo(ContainSubstring("beta"))
	})

	It("keeps the fence for a sub-agent, and refuses an agent of another project as one", func() {
		a := r.agentOf(acme, "Helper")
		r.agentOf(beta, "Other helper")
		rec := r.chat(a.ID)
		allowSavedAgents(r.svc, rec.ID)
		r.mu.Lock()
		r.steps["child task"] = []studiotest.ToolStep{
			call("read_file", `{"path":"/a.txt"}`),
			call("read_file", `{"path":"/Other/secret.txt"}`),
			call("read_file", `{"path":"/Projects/beta/files/b.txt"}`),
			call("list_files", `{"path":"/","pattern":"*"}`),
		}
		r.mu.Unlock()
		res := r.run(rec.ID, "go", call("spawn_agent", `{"task":"child task","wait":true}`))
		var child []studiotest.ToolResult
		for _, x := range res {
			if x.Tool != "spawn_agent" {
				child = append(child, x)
			}
		}
		Expect(child).To(HaveLen(4))
		Expect(child[0].Out).To(ContainSubstring("inside acme"))
		Expect(outOf(child[1])).NotTo(ContainSubstring(outsideMark))
		Expect(outOf(child[2])).NotTo(ContainSubstring(outsideMark))
		Expect(child[3].Out).NotTo(ContainSubstring("Other"))
		Expect(child[3].Out).NotTo(ContainSubstring("beta"))

		r.mu.Lock()
		r.steps["other task"] = []studiotest.ToolStep{call("read_file", `{"path":"/b.txt"}`)}
		r.mu.Unlock()
		res = r.run(rec.ID, "go2", call("spawn_agent", `{"task":"other task","agent":"Other helper"}`))
		last := res[len(res)-1]
		Expect(last.Tool).To(Equal("spawn_agent"))
		Expect(last.Err).To(HaveOccurred(), "an agent of another project is outside this fence: %s", outOf(last))
		for _, x := range res {
			Expect(outOf(x)).NotTo(ContainSubstring(outsideMark))
		}
	})

	It("never gives a project agent without folders the whole home", func() {
		a := r.agentOf(acme, "Helper")
		bare := a
		bare.Folders, bare.FolderID, bare.FolderMode = nil, 0, ""
		Expect(r.sh.agents.Put(r.ctx, bare)).To(Succeed())
		_, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).To(MatchError(studio.ErrProjectUnavailable))
		wide := a
		wide.Folders = []agents.AgentFolder{{Node: r.nodeOf("/Other"), Mode: agents.FolderWrite}}
		Expect(r.sh.agents.Put(r.ctx, wide)).To(Succeed())
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).To(MatchError(studio.ErrProjectUnavailable), "a record with another folder is refused, not honoured")
		noFiles := a
		noFiles.BuiltinsOff = []string{"files"}
		Expect(r.sh.agents.Put(r.ctx, noFiles)).To(Succeed())
		rec := r.chat(a.ID)
		Expect(r.stat(r.actor(rec.ID), "/Projects/acme/files/a.txt")).To(HaveOccurred(), "an agent with No files reaches its chat folder only")
	})

	It("keeps the chat folders of two agents of one project and one of another apart", func() {
		a1, a2, b1 := r.agentOf(acme, "One"), r.agentOf(acme, "Two"), r.agentOf(beta, "Three")
		var recs []session.ChatRecord
		for _, a := range []agents.Agent{a1, a2, b1} {
			rec := r.chat(a.ID)
			r.actor(rec.ID) // makes the chat folder
			full, err := r.svc.Get(r.ctx, alice, rec.ID)
			Expect(err).NotTo(HaveOccurred())
			recs = append(recs, full)
		}
		for i, rec := range recs {
			act := r.actor(rec.ID)
			Expect(r.stat(act, "/"+session.ChatsDir+"/"+rec.Folder)).To(Succeed(), "its own chat folder")
			for j, other := range recs {
				if i != j {
					Expect(r.stat(act, "/"+session.ChatsDir+"/"+other.Folder)).To(HaveOccurred(), "chat %d reaches the folder of chat %d", i, j)
				}
			}
		}
		Expect(r.stat(r.actor(recs[2].ID), "/Projects/acme/files/a.txt")).To(HaveOccurred(), "the agent of another project")
	})
})

func (r *projRig) nodeOf(rel string) int64 {
	GinkgoHelper()
	n, err := r.sh.files.Store().Stat(r.ctx, r.user, r.home+rel)
	Expect(err).NotTo(HaveOccurred())
	return n.ID
}

var _ = Describe("a project agent while its project is not available", func() {
	var (
		r    *projRig
		acme projects.Project
		ag   agents.Agent
		conv string
	)
	BeforeEach(func() {
		r = newProjRig()
		acme = r.project("Acme")
		ag = r.agentOf(acme, "Helper")
		r.connRig.agent = ag
		conv = r.chat(ag.ID).ID
		r.run(conv, "before", call("read_file", `{"path":"/a.txt"}`))
	})

	// unavailable makes the project unusable, restore brings it back.
	type change struct {
		name    string
		unavail func()
		restore func()
	}
	changes := func() []change {
		var trashRev int64
		return []change{
			{name: "archived", unavail: func() {
				_, err := r.proj.Archive(r.ctx, alice, acme.ID)
				Expect(err).NotTo(HaveOccurred())
			}, restore: func() {
				_, err := r.proj.Restore(r.ctx, alice, acme.ID)
				Expect(err).NotTo(HaveOccurred())
			}},
			{name: "deleted to the Trash", unavail: func() {
				Expect(r.proj.Delete(r.ctx, alice, acme.ID)).To(Succeed())
				trash, err := r.sh.files.Trash(r.ctx, alice, 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(trash).NotTo(BeEmpty())
				trashRev = trash[0].Rev
			}, restore: func() {
				_, _, err := r.sh.files.Store().Undo(r.ctx, r.user, trashRev, workspace.UndoOptions{})
				Expect(err).NotTo(HaveOccurred())
			}},
			{name: "moved out of Projects", unavail: func() {
				_, err := r.sh.files.Store().Move(r.ctx, r.user, r.home+"/Projects/acme", r.home+"/Elsewhere/acme")
				Expect(err).NotTo(HaveOccurred())
			}, restore: func() {
				_, err := r.sh.files.Store().Move(r.ctx, r.user, r.home+"/Elsewhere/acme", r.home+"/Projects/acme")
				Expect(err).NotTo(HaveOccurred())
			}},
			{name: "without its files folder", unavail: func() {
				Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/files")).To(Succeed())
			}, restore: func() {
				trash, err := r.sh.files.Trash(r.ctx, alice, 10)
				Expect(err).NotTo(HaveOccurred())
				_, _, err = r.sh.files.Store().Undo(r.ctx, r.user, trash[0].Rev, workspace.UndoOptions{})
				Expect(err).NotTo(HaveOccurred())
			}},
		}
	}

	for _, i := range []int{0, 1, 2, 3} {
		It(fmt.Sprintf("refuses every start while the project is %s, and works again when it is back", changes()[i].name), func() {
			c := changes()[i]
			tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
			before, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			r.bind(studio.ConnectorInput{Allow: []string{"11"}})
			c.unavail()
			beforeTools := len(r.h.Results())

			// A new chat.
			_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
			Expect(err).To(MatchError(studio.ErrProjectUnavailable))
			// The next turn of an open chat, however it starts.
			_, err = r.svc.Send(r.ctx, alice, conv, "after")
			Expect(err).To(MatchError(studio.ErrProjectUnavailable))
			_, err = r.svc.Regenerate(r.ctx, alice, conv)
			Expect(err).To(MatchError(studio.ErrProjectUnavailable))
			_, err = r.svc.ChatAccess(r.ctx, alice, conv)
			Expect(err).To(MatchError(studio.ErrProjectUnavailable))
			_, err = r.svc.CommandActor(r.ctx, alice, conv)
			Expect(err).To(MatchError(studio.ErrProjectUnavailable))

			// A due trigger run is skipped: the trigger stays on, nothing is told.
			r.due()
			run := r.waitEnded(tr.ID)
			Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
			Expect(run.Reason).To(ContainSubstring("project"))
			Expect(run.ChatID).To(BeEmpty())
			got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue(), "the trigger stayed on")
			Expect(got.Since).To(Equal(before.Since), "the start of the watch did not move")
			Expect(r.notes()).To(BeEmpty(), "a skipped run is not told")

			// A connector message gets one reply and makes no chat.
			r.bot.say(1, 11, "Ada", "ada", "hi")
			waitFor("the reply", func() bool { return len(r.bot.answers()) >= 1 })
			time.Sleep(200 * time.Millisecond)
			Expect(r.bot.answers()).To(Equal([]string{"This agent cannot work right now: its project is archived or was removed."}))
			chats, err := r.svc.List(r.ctx, alice, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(chats).To(HaveLen(1), "only the chat that was open: %+v", chats)
			b, err := r.conns.GetConnector(r.ctx, alice, r.agentBinding())
			Expect(err).NotTo(HaveOccurred())
			Expect(b.Enabled).To(BeTrue(), "the binding stays on")
			Expect(r.h.Results()[beforeTools:]).To(BeEmpty(), "no tool ran")

			// Restored: everything works with no other action.
			c.restore()
			res := r.run(conv, "again", call("read_file", `{"path":"/a.txt"}`))
			Expect(res[0].Err).NotTo(HaveOccurred(), outOf(res[0]))
			Expect(res[0].Out).To(ContainSubstring("inside acme"))
			_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			r.sh.clock.Advance(24 * time.Hour)
			r.pass()
			run = r.waitEnded(tr.ID)
			Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
			r.bot.say(2, 11, "Ada", "ada", "back")
			waitFor("the answer", func() bool { return len(r.bot.answers()) >= 2 })
			Expect(r.bot.answers()[1]).To(Equal("ok"))
		})
	}

	It("keeps working when the project folder is renamed in Files", func() {
		_, err := r.sh.files.Store().Move(r.ctx, r.user, r.home+"/Projects/acme", r.home+"/Projects/renamed")
		Expect(err).NotTo(HaveOccurred())
		res := r.run(conv, "after rename", call("read_file", `{"path":"/a.txt"}`), call("write_file", `{"path":"/w.txt","content":"x"}`))
		Expect(res[0].Err).NotTo(HaveOccurred(), outOf(res[0]))
		Expect(res[0].Out).To(ContainSubstring("inside acme"))
		Expect(res[1].Err).NotTo(HaveOccurred(), outOf(res[1]))
		Expect(r.exists("/Projects/renamed/files/w.txt")).To(BeTrue())
		_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		again, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper"})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Folders).To(Equal(ag.Folders), "the nodes are the same")
	})

	It("keeps a trigger the user switched off disabled through archive and restore", func() {
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		_, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		r.due()
		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		r.due()
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
		runs, _ := r.host.TriggerRuns(r.ctx, alice, tr.ID, 5)
		Expect(runs).To(BeEmpty(), "a disabled trigger does not run")
	})

	It("refuses a new trigger for an agent whose project is archived", func() {
		_, err := r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "T", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", AgentID: ag.ID, Enabled: true})
		Expect(err).To(MatchError(studio.ErrBadTrigger))
	})
})

var _ = Describe("a project agent: loops, forks, resumes, replaced folders", func() {
	var (
		r    *projRig
		acme projects.Project
		ag   agents.Agent
		conv string
	)
	BeforeEach(func() {
		r = newProjRig()
		acme = r.project("Acme")
		ag = r.agentOf(acme, "Helper")
		conv = r.chat(ag.ID).ID
		r.run(conv, "before", call("read_file", `{"path":"/a.txt"}`))
	})

	It("skips the runs of a loop while the project is archived, without a failure, and goes on when it is back", func() {
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		l, err := host.Start(r.ctx, alice, conv, studio.LoopInput{Interval: "5m", Prompt: "Check the build."})
		Expect(err).NotTo(HaveOccurred())
		if l.State == loops.StatePending {
			l, err = host.SetState(r.ctx, alice, conv, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
		}
		get := func() loops.Loop {
			got, gerr := ls.Get(r.ctx, l.ID)
			Expect(gerr).NotTo(HaveOccurred())
			return got
		}
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		before := get()
		for range loops.MaxFailures + 2 {
			r.sh.clock.Advance(6 * time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
			time.Sleep(30 * time.Millisecond)
		}
		got := get()
		Expect(got.State).To(Equal(loops.StateActive), "the loop was %s: %s", got.State, got.Reason)
		Expect(got.Failures).To(Equal(before.Failures))
		Expect(got.Runs).To(Equal(before.Runs))
		Expect(got.Running).To(BeFalse())
		Expect(r.notes()).To(BeEmpty())

		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		until("the loop to run again", func() bool {
			r.sh.clock.Advance(6 * time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
			return get().Runs > before.Runs
		})
		until("the run to end", func() bool { return !get().Running })
		Expect(get().State).To(Equal(loops.StateActive))
		Expect(get().Failures).To(BeZero())
	})

	// selfPaced starts a self-paced loop of the chat, due at once.
	selfPaced := func() (*studio.Loops, loops.Store, string) {
		GinkgoHelper()
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: 5 * time.Second})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		l, err := host.Start(r.ctx, alice, conv, studio.LoopInput{Prompt: "Watch the queue."})
		Expect(err).NotTo(HaveOccurred())
		if l.State == loops.StatePending {
			_, err = host.SetState(r.ctx, alice, conv, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
		}
		return host, ls, l.ID
	}

	It("checks a self-paced loop of a deleted project at most once per back-off window", func() {
		host, ls, id := selfPaced()
		before, err := ls.Get(r.ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.proj.Delete(r.ctx, alice, acme.ID)).To(Succeed())
		r.lists.n.Store(0)
		r.sh.clock.Advance(time.Second)
		Expect(host.Pass(r.ctx)).To(Succeed())
		one := r.lists.n.Load()
		Expect(one).To(BeNumerically(">", 0), "the first pass checks the project")
		got, _ := ls.Get(r.ctx, id)
		Expect(got.Next.Sub(r.sh.clock.Now())).To(BeNumerically(">=", time.Minute), "the next check is at least a minute away")

		r.lists.n.Store(0)
		// Ten minutes in steps of one tick (5 s): without a back-off that is
		// 120 checks.
		for range 120 {
			r.sh.clock.Advance(5 * time.Second)
			Expect(host.Pass(r.ctx)).To(Succeed())
		}
		Expect(r.lists.n.Load()).To(BeNumerically("<=", 5*one), "the project was read %d times for %d reads of one check", r.lists.n.Load(), one)
		got, _ = ls.Get(r.ctx, id)
		Expect(got.State).To(Equal(loops.StateActive))
		Expect(got.Runs).To(Equal(before.Runs))
		Expect(got.Failures).To(Equal(before.Failures))
		Expect(r.notes()).To(BeEmpty())

		// The back-off grows and stops at 30 minutes.
		r.lists.n.Store(0)
		for range 240 {
			r.sh.clock.Advance(time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
		}
		Expect(r.lists.n.Load()).To(BeNumerically("<=", 10*one), "four hours: the window is capped at 30 minutes")
		got, _ = ls.Get(r.ctx, id)
		Expect(got.Next.Sub(r.sh.clock.Now())).To(BeNumerically("<=", 31*time.Minute))
		Expect(got.State).To(Equal(loops.StateActive))
	})

	It("runs a self-paced loop again after the project is restored, within the cap", func() {
		host, ls, id := selfPaced()
		_, err := r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		for range 12 {
			r.sh.clock.Advance(time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
		}
		before, _ := ls.Get(r.ctx, id)
		Expect(before.Runs).To(BeZero())
		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		until("the loop to run again", func() bool {
			r.sh.clock.Advance(time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
			g, _ := ls.Get(r.ctx, id)
			return g.Runs > 0
		})
		until("the run to end", func() bool { g, _ := ls.Get(r.ctx, id); return !g.Running })
		g, _ := ls.Get(r.ctx, id)
		Expect(g.Failures).To(BeZero())
		Expect(g.Reason).NotTo(ContainSubstring("project"), "the back-off note goes with the run")
	})

	It("does not copy a chat of a project agent while its project is not available, and marks the copy fenced", func() {
		cp, err := r.svc.Fork(r.ctx, alice, conv, 0, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(cp.ScopedAgent).To(BeTrue(), "the copy of a fenced chat is fenced")
		Expect(cp.ProjectID).To(Equal(acme.ID), "the copy stays in the project of its agent")
		n, err := r.svc.CountChatsInProject(r.ctx, alice, acme.ID, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2), "the copy is counted with the chat it was made from")
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		before, _ := r.svc.List(r.ctx, alice, 100)
		_, err = r.svc.Fork(r.ctx, alice, conv, 0, "")
		Expect(err).To(MatchError(studio.ErrProjectUnavailable))
		after, _ := r.svc.List(r.ctx, alice, 100)
		Expect(after).To(HaveLen(len(before)), "no chat was made")
	})

	It("does not spend a resume attempt while the project is not available", func() {
		st := r.sh.chats
		Expect(st.ClaimTurn(r.ctx, alice, conv, "r0")).To(Succeed())
		Expect(st.MarkInterrupted(r.ctx, alice, conv, "r0", 0)).To(Succeed())
		_, err := r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		res, err := r.svc.ResumeInterrupted(r.ctx, alice, conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Started).To(BeFalse())
		Expect(turn(r.svc, alice, conv).ResumeAttempts).To(BeZero(), "an attempt was spent")
		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		res, err = r.svc.ResumeInterrupted(r.ctx, alice, conv)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Started).To(BeTrue(), "the resume goes on after the restore")
	})

	It("reads the index at most once more for a project that was deleted", func() {
		Expect(r.proj.Delete(r.ctx, alice, acme.ID)).To(Succeed())
		r.lists.n.Store(0)
		_, err := r.svc.ChatAccess(r.ctx, alice, conv)
		Expect(err).To(MatchError(studio.ErrProjectUnavailable))
		check := r.lists.n.Load()
		r.lists.n.Store(0)
		_, err = r.proj.List(r.ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(check).To(BeNumerically("<=", r.lists.n.Load()), "one reindex pass, as one List")
	})

	// recreate deletes a folder of the project and makes a new one of the
	// same name, so its node changes.
	recreate := func(name string) {
		GinkgoHelper()
		Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/"+name)).To(Succeed())
		r.put("/Projects/acme/"+name+"/a.txt", "again acme")
		if name == "memory" {
			r.put("/Projects/acme/memory/index.md", "# Memory\n")
		}
	}
	It("skips the runs of a loop whose agent's folders changed, spends no budget, and runs again after a save", func() {
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		l, err := host.Start(r.ctx, alice, conv, studio.LoopInput{Interval: "5m", Prompt: "Check the build."})
		Expect(err).NotTo(HaveOccurred())
		if l.State == loops.StatePending {
			l, err = host.SetState(r.ctx, alice, conv, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
		}
		get := func() loops.Loop {
			got, gerr := ls.Get(r.ctx, l.ID)
			Expect(gerr).NotTo(HaveOccurred())
			return got
		}
		before := get()
		recreate("files")
		for range loops.MaxFailures + 2 {
			r.sh.clock.Advance(6 * time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
			time.Sleep(30 * time.Millisecond)
		}
		got := get()
		Expect(got.State).To(Equal(loops.StateActive), "the loop was %s: %s", got.State, got.Reason)
		Expect(got.Failures).To(Equal(before.Failures))
		Expect(got.Runs).To(Equal(before.Runs))
		Expect(got.Running).To(BeFalse())
		Expect(r.notes()).To(BeEmpty())
		// The owner's budget of the day is untouched: a budget of one is
		// still free.
		took, err := ls.TakeRun(r.ctx, alice, loops.Day(r.sh.clock.Now()), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(took).To(BeTrue(), "a skipped slot spent a loop run of the owner")

		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper"})
		Expect(err).NotTo(HaveOccurred())
		until("the loop to run again", func() bool {
			r.sh.clock.Advance(6 * time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
			return get().Runs > before.Runs
		})
		until("the run to end", func() bool { return !get().Running })
		Expect(get().State).To(Equal(loops.StateActive))
	})

	for _, name := range []string{"files", "memory"} {
		It("tells a replaced "+name+" folder apart, and a save with no folder list reconnects the agent", func() {
			// A read only agent keeps its mode through the heal.
			ro := []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderRead}}
			ag, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper"}, &ro)
			Expect(err).NotTo(HaveOccurred())
			recreate(name)
			_, err = r.svc.Send(r.ctx, alice, conv, "after")
			Expect(err).To(MatchError(studio.ErrProjectFoldersChanged))
			Expect(err.Error()).NotTo(Equal(studio.ProjectUnavailableText))
			_, err = r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
			Expect(err).To(MatchError(studio.ErrProjectFoldersChanged))

			healed, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Renamed"})
			Expect(err).NotTo(HaveOccurred())
			files, memory := r.fence(acme)
			Expect(healed.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderRead}, {Node: memory.Node, Mode: agents.FolderRead}}))
			res := r.run(conv, "next", call("read_file", `{"path":"/a.txt"}`))
			Expect(res[0].Err).NotTo(HaveOccurred(), outOf(res[0]))
		})
	}

	It("never keeps a foreign node of the old record: it is dropped and the project's folders are made again", func() {
		files, memory := r.fence(acme)
		for name, node := range map[string]int64{"the project folder": r.nodeOf("/Projects/acme"), "the home": r.nodeOf(""), "another folder": r.nodeOf("/Other")} {
			bad := ag
			bad.Folders = []agents.AgentFolder{{Node: node, Mode: agents.FolderWrite}, {Node: memory.Node, Mode: agents.FolderRead}}
			Expect(r.sh.agents.Put(r.ctx, bad)).To(Succeed())
			healed, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Renamed"})
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(healed.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: memory.Node, Mode: agents.FolderRead}}), name)
			// A dead node next to a live foreign one heals the same way.
			mixed := ag
			mixed.Folders = []agents.AgentFolder{{Node: node, Mode: agents.FolderWrite}, {Node: files.Node + 9999, Mode: agents.FolderWrite}}
			Expect(r.sh.agents.Put(r.ctx, mixed)).To(Succeed())
			healed, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Renamed"})
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(healed.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: memory.Node, Mode: agents.FolderRead}}), name)
		}
	})

	It("still refuses an explicit folder list that names a foreign folder", func() {
		for _, path := range []string{"/Projects/acme", "/Other"} {
			list := []studio.AgentFolderWrite{{Path: path, Mode: agents.FolderWrite}}
			_, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Renamed"}, &list)
			Expect(err).To(MatchError(studio.ErrAgentFolder), path)
		}
	})

	It("reconnects an agent whose files folder was renamed away and made again, at the stored read mode", func() {
		ro := []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderRead}}
		ag, err := r.svc.SaveAgentWithFolders(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper"}, &ro)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.sh.files.Store().Move(r.ctx, r.user, r.home+"/Projects/acme/files", r.home+"/Projects/acme/files-old")
		Expect(err).NotTo(HaveOccurred())
		r.put("/Projects/acme/files/b.txt", "again acme")
		_, err = r.svc.Send(r.ctx, alice, conv, "after")
		Expect(err).To(HaveOccurred(), "the chat keeps its refusal until the agent is saved")

		healed, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: ag.ID, Name: "Renamed"})
		Expect(err).NotTo(HaveOccurred(), "a live renamed folder is no dead end")
		files, memory := r.fence(acme)
		Expect(healed.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderRead}, {Node: memory.Node, Mode: agents.FolderRead}}), "the read mode is kept, never widened")
		got, err := r.svc.Agent(r.ctx, alice, ag.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folders).To(Equal(healed.Folders))
	})

})

var _ = Describe("the availability flag of the agents of a project", func() {
	var (
		r    *projRig
		acme projects.Project
	)
	BeforeEach(func() {
		r = newProjRig()
		acme = r.project("Acme")
		for _, n := range []string{"A", "B", "C", "D"} {
			r.agentOf(acme, n)
		}
	})
	flags := func() []bool {
		GinkgoHelper()
		rows, err := r.svc.ProjectAgents(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		out := make([]bool, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.Unavailable)
		}
		return out
	}
	// startable is what a run decides: a new chat with the agent starts or is refused.
	startable := func() []bool {
		GinkgoHelper()
		rows, err := r.svc.ProjectAgents(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		out := make([]bool, 0, len(rows))
		for _, row := range rows {
			_, cerr := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: row.Agent.ID})
			out = append(out, cerr != nil)
		}
		return out
	}
	DescribeTable("agrees with the run-time check in every state",
		func(setup func()) {
			setup()
			Expect(flags()).To(Equal(startable()))
		},
		Entry("a healthy project", func() {}),
		Entry("an archived project", func() { _, err := r.proj.Archive(r.ctx, alice, acme.ID); Expect(err).NotTo(HaveOccurred()) }),
		Entry("a deleted project", func() { Expect(r.proj.Delete(r.ctx, alice, acme.ID)).To(Succeed()) }),
		Entry("a replaced files folder", func() {
			Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/files")).To(Succeed())
			r.put("/Projects/acme/files/a.txt", "again")
		}),
	)

	It("flags every agent when the folders changed, and none when healthy", func() {
		Expect(flags()).To(Equal([]bool{false, false, false, false}))
		Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/files")).To(Succeed())
		r.put("/Projects/acme/files/a.txt", "again")
		Expect(flags()).To(Equal([]bool{true, true, true, true}))
	})

	It("flags only the agent whose folders no longer match", func() {
		files, _ := r.fence(acme)
		rows, err := r.svc.ProjectAgents(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		bad := rows[1].Agent
		bad.Folders = []agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: r.nodeOf("/Other"), Mode: agents.FolderRead}}
		Expect(r.sh.agents.Put(r.ctx, bad)).To(Succeed())
		Expect(flags()).To(Equal([]bool{false, true, false, false}))
	})

	It("resolves the project once for all its agents, even when it is gone", func() {
		Expect(r.proj.Delete(r.ctx, alice, acme.ID)).To(Succeed())
		r.lists.n.Store(0)
		Expect(flags()).To(Equal([]bool{true, true, true, true}))
		// One resolve of the project reads the index twice at most (the miss
		// of Get, and the look for a folder that came back); it was eight
		// for four agents when every agent resolved it again.
		Expect(r.lists.n.Load()).To(BeNumerically("<=", 2), "one resolve for the request, not one per agent")
	})
})
