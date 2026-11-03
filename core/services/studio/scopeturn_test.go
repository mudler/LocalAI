package studio_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// outsideMark is in every file outside the agent's folder: no tool output and
// no tool error of a scoped chat may hold it.
const outsideMark = "OUTSIDE-e41c7b"

// scopeTurnRig is a chat service with files, media tools and saved agents
// whose turns run the tool calls a spec gives for each message.
type scopeTurnRig struct {
	ctx  context.Context
	f    *files.Service
	svc  *studio.Service
	h    *studiotest.ToolHarness
	home string

	mu    sync.Mutex
	steps map[string][]studiotest.ToolStep
}

func newScopeTurnRig() *scopeTurnRig {
	r := &scopeTurnRig{ctx: context.Background(), f: newFiles(), home: session.HomePath(alice), steps: map[string][]studiotest.ToolStep{}}
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	m, _ := newMediaService()
	r.svc, r.h = newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
		r.mu.Lock()
		defer r.mu.Unlock()
		// The message may come after the turn's note (the rules line, a
		// sub-agent's missing grants).
		for k, s := range r.steps {
			if text == k || strings.HasSuffix(text, "\n"+k) || strings.HasSuffix(text, " "+k) {
				return append(s, studiotest.ToolStep{Say: "done " + k})
			}
		}
		return []studiotest.ToolStep{{Say: "ok " + text}}
	}, func(d *studio.Deps) { d.Files = r.f; d.Agents = ag; d.Media = m })
	Expect(r.f.EnsureHome(r.ctx, alice)).To(Succeed())
	r.put("/Projects/Acme/a.txt", "inside-a common")
	r.put("/Projects/Acme/AGENTS.md", "acme rules")
	r.put("/Projects/Acme/sub/s.txt", "inside-sub")
	r.put("/AGENTS.md", "home rules "+outsideMark)
	r.put("/Other/secret.txt", "secret "+outsideMark+" common")
	r.put("/Projects/Acme2/x.txt", "sibling "+outsideMark+" common")
	r.putBytes("/Other/doc.docx", extracttest.Docx(extracttest.Para("", "doc "+outsideMark)))
	r.putBytes("/Projects/Acme/doc.docx", extracttest.Docx(extracttest.Para("", "inside doc text")))
	for i := range 5 {
		r.put(fmt.Sprintf("/Other/many/f%d.txt", i), "common "+outsideMark)
	}
	return r
}

func (r *scopeTurnRig) put(rel, content string) {
	GinkgoHelper()
	r.putBytes(rel, []byte(content))
}

func (r *scopeTurnRig) putBytes(rel string, data []byte) {
	GinkgoHelper()
	_, err := r.f.Store().Put(r.ctx, r.f.Actor(alice), r.home+rel, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func (r *scopeTurnRig) exists(rel string) bool {
	_, err := r.f.Store().Stat(r.ctx, r.f.Actor(alice), r.home+rel)
	return err == nil
}

func (r *scopeTurnRig) read(rel string) string {
	GinkgoHelper()
	rc, _, err := r.f.Store().Open(r.ctx, r.f.Actor(alice), r.home+rel)
	Expect(err).NotTo(HaveOccurred(), "open %s", rel)
	defer func() { _ = rc.Close() }()
	var b bytes.Buffer
	_, err = b.ReadFrom(rc)
	Expect(err).NotTo(HaveOccurred())
	return b.String()
}

// agent saves an agent that works in folder (a path of the home) at mode.
func (r *scopeTurnRig) agent(folder, mode string) agents.Agent {
	GinkgoHelper()
	var w *studio.AgentFolderWrite
	if folder != "" {
		w = &studio.AgentFolderWrite{Path: folder, Mode: mode}
	}
	a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Agent " + folder + mode}, w)
	Expect(err).NotTo(HaveOccurred())
	return a
}

func (r *scopeTurnRig) chat(agentID string) session.ChatRecord {
	GinkgoHelper()
	rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: agentID})
	Expect(err).NotTo(HaveOccurred())
	return rec
}

// run sends text with the given tool calls and returns the results of this
// turn, in order.
func (r *scopeTurnRig) run(conv, text string, steps ...studiotest.ToolStep) []studiotest.ToolResult {
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

func call(tool, args string) studiotest.ToolStep {
	return studiotest.ToolStep{Tool: tool, Args: args}
}

func outOf(res studiotest.ToolResult) string {
	return res.Out + " " + fmt.Sprint(res.Err)
}

// chatFolder is the workspace path of the chat's own folder.
func (r *scopeTurnRig) chatFolder(conv string) string {
	GinkgoHelper()
	rec, err := r.svc.Get(r.ctx, alice, conv)
	Expect(err).NotTo(HaveOccurred())
	Expect(rec.Folder).NotTo(BeEmpty())
	return "/" + session.ChatsDir + "/" + rec.Folder
}

var _ = Describe("a chat of a scoped agent, end to end", func() {
	var r *scopeTurnRig
	BeforeEach(func() { r = newScopeTurnRig() })

	It("reads and writes inside a read-write folder by the paths of its place", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderWrite).ID)
		res := r.run(rec.ID, "go",
			call("read_file", `{"path":"/a.txt"}`),
			call("read_file", `{"path":"/workspace/home/sub/s.txt"}`),
			call("write_file", `{"path":"/new.txt","content":"made"}`),
			call("edit_file", `{"path":"/a.txt","old_string":"inside-a","new_string":"changed-a"}`),
			call("list_files", `{"path":"/"}`),
			call("read_document", `{"path":"/doc.docx"}`),
		)
		Expect(res).To(HaveLen(6))
		for _, x := range res {
			Expect(x.Err).NotTo(HaveOccurred(), "%s: %s", x.Tool, outOf(x))
		}
		Expect(res[0].Out).To(ContainSubstring("inside-a"))
		Expect(res[1].Out).To(ContainSubstring("inside-sub"))
		Expect(r.read("/Projects/Acme/new.txt")).To(Equal("made"))
		Expect(r.read("/Projects/Acme/a.txt")).To(ContainSubstring("changed-a"))
		Expect(res[4].Out).To(ContainSubstring("a.txt"))
		Expect(res[4].Out).NotTo(ContainSubstring("Projects"), "the folder is the root of its place")
		Expect(res[5].Out).To(ContainSubstring("inside doc text"))
		Expect(r.exists("/new.txt")).To(BeFalse(), "nothing is written at the home root")
	})

	It("only reads a read-only folder", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderRead).ID)
		res := r.run(rec.ID, "go",
			call("read_file", `{"path":"/a.txt"}`),
			call("write_file", `{"path":"/new.txt","content":"x"}`),
			call("edit_file", `{"path":"/a.txt","old_string":"inside-a","new_string":"x"}`),
			call("delete_file", `{"path":"/a.txt"}`),
			call("move_file", `{"from":"/a.txt","to":"/b.txt"}`),
		)
		Expect(res).To(HaveLen(5))
		Expect(res[0].Out).To(ContainSubstring("inside-a"))
		for _, x := range res[1:] {
			Expect(x.Err).To(HaveOccurred(), "%s must be refused: %s", x.Tool, outOf(x))
		}
		Expect(r.exists("/Projects/Acme/new.txt")).To(BeFalse())
		Expect(r.read("/Projects/Acme/a.txt")).To(Equal("inside-a common"))
	})

	It("cannot read, list, search, count or name anything outside its folder", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderWrite).ID)
		escapes := []string{
			"/Other/secret.txt", "Other/secret.txt", "../Other/secret.txt", "/../Other/secret.txt",
			"/workspace/home/../Other/secret.txt", "/workspace/chat/../../Other/secret.txt",
			"/users/alice/Other/secret.txt", "/workspace/users/alice/Other/secret.txt",
			"../Acme2/x.txt", "/workspace/home/../../Acme2/x.txt", "/AGENTS.md/../../AGENTS.md",
			"/workspace/Home/../Other/secret.txt", "..%2fOther%2fsecret.txt", "/workspace/shared-x/../home/../Other/secret.txt",
		}
		var steps []studiotest.ToolStep
		for _, p := range escapes {
			steps = append(steps,
				call("read_file", fmt.Sprintf(`{"path":%q}`, p)),
				call("list_files", fmt.Sprintf(`{"path":%q}`, p)),
				call("read_document", fmt.Sprintf(`{"path":%q}`, strings.Replace(p, "secret.txt", "doc.docx", 1))),
				call("describe_image", fmt.Sprintf(`{"path":%q}`, p)),
			)
		}
		listing := []studiotest.ToolStep{
			call("list_files", `{"path":"/","pattern":"*"}`),
			call("list_files", `{"path":"/workspace","pattern":"*"}`),
			call("search_files", `{"query":"common"}`),
			call("search_files", `{"query":"common","path":"/"}`),
			call("search_files", `{"query":"`+outsideMark+`"}`),
			call("search_files", `{"query":"sec.*t","regex":true}`),
		}
		steps = append(steps, listing...)
		// The writes come last: a spelling without ".." is a path inside
		// the folder, so it makes a file there, never outside.
		for _, p := range escapes {
			steps = append(steps,
				call("write_file", fmt.Sprintf(`{"path":%q,"content":"overwritten"}`, p)),
				call("delete_file", fmt.Sprintf(`{"path":%q}`, p)),
			)
		}
		res := r.run(rec.ID, "go", steps...)
		Expect(res).To(HaveLen(len(steps)))
		for i, x := range res {
			Expect(outOf(x)).NotTo(ContainSubstring(outsideMark), "%s %s", x.Tool, steps[i].Args)
			Expect(outOf(x)).NotTo(ContainSubstring("home rules"), "%s %s", x.Tool, steps[i].Args)
		}
		first := len(escapes) * 4
		for _, x := range res[first : first+len(listing)] {
			Expect(x.Out).NotTo(ContainSubstring("Acme2"), x.Tool)
			Expect(x.Out).NotTo(ContainSubstring("secret.txt"), x.Tool)
			Expect(x.Out).NotTo(ContainSubstring("many"), x.Tool)
			Expect(x.Out).NotTo(ContainSubstring("Projects"), x.Tool)
			Expect(x.Out).NotTo(ContainSubstring("Other"), x.Tool)
		}
		// The plain search finds the one file inside, never a count of
		// the six outside.
		search := res[first+2].Out
		Expect(search).To(ContainSubstring("a.txt"))
		Expect(strings.Count(search, "common")).To(BeNumerically("<=", 1), search)
		Expect(r.read("/Other/secret.txt")).To(ContainSubstring(outsideMark), "nothing outside changed")
		writes := res[first+len(listing):]
		Expect(writes[0].Err).NotTo(HaveOccurred(), "/Other/secret.txt is a path inside the folder: %s", outOf(writes[0]))
		Expect(r.exists("/Projects/Acme/Other")).To(BeTrue(), "it was written inside the folder")
		for i, p := range escapes {
			// An encoded separator is a literal name inside the folder.
			if strings.Contains(p, "..") && !strings.Contains(p, "%2f") {
				Expect(writes[2*i].Err).To(HaveOccurred(), "write %q", p)
				Expect(writes[2*i+1].Err).To(HaveOccurred(), "delete %q", p)
			}
		}
		Expect(r.read("/Projects/Acme2/x.txt")).To(ContainSubstring(outsideMark))
		Expect(r.exists("/AGENTS.md")).To(BeTrue())
	})

	It("is told the layout of its mounts and uses /workspace/chat for its work folder", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderWrite).ID)
		res := r.run(rec.ID, "go",
			call("write_file", `{"path":"/workspace/chat/work/notes.md","content":"draft"}`),
			call("write_file", `{"path":"/helper.py","content":"print(1)"}`),
			call("list_files", `{"path":"/workspace/chat"}`),
		)
		Expect(res).To(HaveLen(3))
		for _, x := range res {
			Expect(x.Err).NotTo(HaveOccurred(), "%s: %s", x.Tool, outOf(x))
		}
		dir := r.chatFolder(rec.ID)
		Expect(r.read(dir + "/work/notes.md")).To(Equal("draft"))
		Expect(r.exists("/Projects/Acme/helper.py")).To(BeTrue())
		Expect(res[1].Out).To(ContainSubstring("/workspace/chat/work/"), "the nudge names the chat's work folder")
		Expect(res[2].Out).To(ContainSubstring("work"))

		system := r.h.SystemPrompt(rec.ID)
		Expect(system).To(ContainSubstring(`/workspace/home: the folder "Acme" of the user (read and write)`))
		Expect(system).To(ContainSubstring("/workspace/chat: this chat's own folder"))
		Expect(system).NotTo(ContainSubstring("Page"))
		Expect(system).NotTo(ContainSubstring("home folder"))
		turnPrompt := r.h.TurnPrompt(rec.ID)
		Expect(turnPrompt).To(ContainSubstring("/workspace/chat/work/"))
		Expect(turnPrompt).NotTo(ContainSubstring("/workspace/home/" + session.ChatsDir))

		// The next turn's note points at the rules of the agent's folder,
		// never at the home's, and its file list names the new files by
		// their place.
		r.run(rec.ID, "again")
		note := strings.Join(r.h.Sent(rec.ID), "\n")
		Expect(note).To(ContainSubstring("/workspace/home/AGENTS.md"))
		Expect(note).NotTo(ContainSubstring("/workspace/home/Projects"))
		Expect(r.h.TurnPrompt(rec.ID)).NotTo(ContainSubstring("/Projects/Acme"))
	})

	It("keeps its scope when the folder is renamed", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderRead).ID)
		Expect(r.run(rec.ID, "go", call("read_file", `{"path":"/a.txt"}`))[0].Out).To(ContainSubstring("inside-a"))
		_, err := r.f.Store().Mkdir(r.ctx, r.f.Actor(alice), r.home+"/Clients")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.f.Store().Move(r.ctx, r.f.Actor(alice), r.home+"/Projects/Acme", r.home+"/Clients/Acme Corp")
		Expect(err).NotTo(HaveOccurred())
		res := r.run(rec.ID, "again",
			call("read_file", `{"path":"/a.txt"}`),
			call("read_file", `{"path":"/Other/secret.txt"}`),
			call("read_file", `{"path":"/Projects/Acme2/x.txt"}`),
		)
		Expect(res[0].Out).To(ContainSubstring("inside-a"))
		Expect(outOf(res[1])).NotTo(ContainSubstring(outsideMark))
		Expect(outOf(res[2])).NotTo(ContainSubstring(outsideMark))
		Expect(r.h.SystemPrompt(rec.ID)).To(ContainSubstring(`the folder "Acme Corp" of the user`))
	})

	It("does not start a turn when the folder is deleted", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderWrite).ID)
		r.run(rec.ID, "go")
		Expect(r.f.Store().Delete(r.ctx, r.f.Actor(alice), r.home+"/Projects/Acme")).To(Succeed())
		before := len(r.h.Sent(rec.ID))
		_, err := r.svc.Send(r.ctx, alice, rec.ID, "after delete")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing), "the endpoints answer it with 409 agent_folder_missing")
		Expect(r.h.Sent(rec.ID)).To(HaveLen(before), "no turn ran on the home")
	})

	It("gives a generic sub-agent the scope of its parent", func() {
		rec := r.chat(r.agent("/Projects/Acme", agents.FolderRead).ID)
		r.mu.Lock()
		r.steps["child task"] = []studiotest.ToolStep{
			call("read_file", `{"path":"/a.txt"}`),
			call("read_file", `{"path":"/Other/secret.txt"}`),
			call("list_files", `{"path":"/","pattern":"*"}`),
			call("write_file", `{"path":"/child.txt","content":"x"}`),
			call("write_file", `{"path":"/workspace/chat/work/child.md","content":"from the child"}`),
		}
		r.mu.Unlock()
		res := r.run(rec.ID, "go", call("spawn_agent", `{"task":"child task","wait":true}`))
		Expect(res[len(res)-1].Tool).To(Equal("spawn_agent"))
		Expect(res[len(res)-1].Err).NotTo(HaveOccurred(), outOf(res[len(res)-1]))
		var child []studiotest.ToolResult
		for _, x := range res {
			if x.Tool != "spawn_agent" {
				child = append(child, x)
			}
		}
		Expect(child).To(HaveLen(5))
		Expect(child[0].Out).To(ContainSubstring("inside-a"))
		Expect(outOf(child[1])).NotTo(ContainSubstring(outsideMark))
		Expect(child[2].Out).NotTo(ContainSubstring("Other"))
		Expect(child[3].Err).To(HaveOccurred(), "the parent's folder is read only")
		Expect(child[4].Err).NotTo(HaveOccurred(), outOf(child[4]))
		Expect(r.read(r.chatFolder(rec.ID) + "/work/child.md")).To(Equal("from the child"))
	})

	It("gives a sub-agent of a saved agent its own folder inside the parent's", func() {
		parent := r.agent("/Projects/Acme", agents.FolderWrite)
		r.agent("/Projects/Acme/sub", agents.FolderWrite)
		rec := r.chat(parent.ID)
		allowSavedAgents(r.svc, rec.ID)
		r.mu.Lock()
		r.steps["sub task"] = []studiotest.ToolStep{
			call("read_file", `{"path":"/s.txt"}`),
			call("read_file", `{"path":"/a.txt"}`),
		}
		r.mu.Unlock()
		res := r.run(rec.ID, "go", call("spawn_agent", `{"task":"sub task","agent":"Agent /Projects/Acme/subwrite"}`))
		var reads []studiotest.ToolResult
		for _, x := range res {
			if x.Tool == "read_file" {
				reads = append(reads, x)
			}
		}
		Expect(reads).To(HaveLen(2), "%+v", res)
		Expect(reads[0].Out).To(ContainSubstring("inside-sub"))
		Expect(outOf(reads[1])).NotTo(ContainSubstring("inside-a"), "the child's folder is the innermost")
	})

	It("applies a change of the folder or the mode from the next turn", func() {
		ag := r.agent("/Projects/Acme", agents.FolderRead)
		rec := r.chat(ag.ID)
		res := r.run(rec.ID, "one", call("write_file", `{"path":"/n1.txt","content":"x"}`))
		Expect(res[0].Err).To(HaveOccurred())

		_, err := r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		res = r.run(rec.ID, "two", call("write_file", `{"path":"/n2.txt","content":"x"}`))
		Expect(res[0].Err).NotTo(HaveOccurred(), outOf(res[0]))
		Expect(r.exists("/Projects/Acme/n2.txt")).To(BeTrue())

		_, err = r.svc.SaveAgentWithFolder(r.ctx, alice, ag, &studio.AgentFolderWrite{Path: "/Other", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		res = r.run(rec.ID, "three",
			call("read_file", `{"path":"/secret.txt"}`),
			call("read_file", `{"path":"/a.txt"}`),
		)
		Expect(res[0].Out).To(ContainSubstring("secret"))
		Expect(outOf(res[1])).NotTo(ContainSubstring("inside-a"))
		Expect(r.h.SystemPrompt(rec.ID)).To(ContainSubstring(`the folder "Other" of the user (read only)`))
	})

	It("leaves a chat whose agent has no folder on the whole home", func() {
		for i, agentID := range []string{"", r.agent("", "").ID} {
			rec := r.chat(agentID)
			res := r.run(rec.ID, fmt.Sprintf("go-%d", i),
				call("read_file", `{"path":"/Projects/Acme/a.txt"}`),
				call("read_file", `{"path":"/Other/secret.txt"}`),
				call("write_file", `{"path":"/top-`+rec.ID+`.txt","content":"x"}`),
			)
			for _, x := range res {
				Expect(x.Err).NotTo(HaveOccurred(), "%s: %s", x.Tool, outOf(x))
			}
			Expect(res[1].Out).To(ContainSubstring(outsideMark))
			Expect(r.exists("/top-" + rec.ID + ".txt")).To(BeTrue())
			system := r.h.SystemPrompt(rec.ID)
			Expect(system).To(ContainSubstring("the user's home folder (read and write)"))
			Expect(system).To(ContainSubstring("build a Page"))
			Expect(system).NotTo(ContainSubstring("/workspace/chat"))
		}
	})
})
