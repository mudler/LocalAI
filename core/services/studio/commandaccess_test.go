//go:build linux || darwin

package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// commandRig is a chat service with files, saved agents and the built-in
// bash on, and a sandbox runner wired to the service's command hooks as the
// server wires its runners.
type commandRig struct {
	ctx  context.Context
	f    *files.Service
	svc  *studio.Service
	h    *studiotest.ToolHarness
	fx   *fakeHostExec
	m    *runner.Manager
	home string
	op   int
}

func newCommandRig(opts ...func(*studio.Deps)) *commandRig {
	r := &commandRig{ctx: context.Background(), f: newFiles(), home: session.HomePath(alice), fx: &fakeHostExec{}}
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.svc, r.h = newToolService(runBash, append([]func(*studio.Deps){func(d *studio.Deps) {
		d.Files = r.f
		d.Agents = ag
		d.HostExec = r.fx
		d.HostExecMode = func() string { return "on" }
	}}, opts...)...)
	Expect(r.f.EnsureHome(r.ctx, alice)).To(Succeed())
	r.put("/Projects/Acme/in.txt", "inside\n")
	r.put("/secret.txt", outsideMark+"\n")
	r.put("/Other/x.txt", outsideMark+"\n")
	m, err := runner.New(runner.Options{
		Backend: func() (sandbox.Backend, error) { return sbtest.NewFakeBackend(GinkgoT().TempDir()), nil },
		Store:   r.f.Store(),
		Actor:   r.svc.CommandActor,
		View:    r.svc.CommandView,
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	r.m = m
	return r
}

func (r *commandRig) put(rel, content string) {
	GinkgoHelper()
	_, err := r.f.Store().Put(r.ctx, r.f.Actor(alice), r.home+rel, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

func (r *commandRig) exists(rel string) bool {
	_, err := r.f.Store().Stat(r.ctx, r.f.Actor(alice), r.home+rel)
	return err == nil
}

func (r *commandRig) scopedChat(mode string) session.ChatRecord {
	GinkgoHelper()
	a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Acme " + mode}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: mode})
	Expect(err).NotTo(HaveOccurred())
	rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
	Expect(err).NotTo(HaveOccurred())
	return rec
}

func (r *commandRig) bash(conv, cmd string) (string, error) {
	r.op++
	raw, _ := json.Marshal(map[string]any{"command": cmd})
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return r.m.Run(ctx, runner.Request{User: alice, Conversation: conv, Operation: fmt.Sprintf("op%d", r.op), Tool: "bash", Args: raw})
}

func shellGroup(svc *studio.Service, user, conv string) studio.BuiltinGroup {
	GinkgoHelper()
	panel, err := svc.Tools(context.Background(), user, conv)
	Expect(err).NotTo(HaveOccurred())
	for _, g := range panel.Builtins {
		if g.Group == builtins.GroupShell {
			return g
		}
	}
	Fail("the chat lists no shell group")
	return studio.BuiltinGroup{}
}

var _ = Describe("commands of a scoped chat", func() {
	It("does not offer the built-in host bash to a scoped chat, and the tool list says why", func() {
		r := newCommandRig()
		plain := create(r.svc, alice)
		sent(r.svc, alice, plain, "run")
		idle(r.svc, alice, plain)
		Expect(r.h.ToolNames(plain)).To(ContainElement("bash"), "an unscoped chat keeps the built-in bash")
		Expect(r.fx.commands()).To(HaveLen(1))

		rec := r.scopedChat(agents.FolderWrite)
		sent(r.svc, alice, rec.ID, "run")
		idle(r.svc, alice, rec.ID)
		Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("bash"))
		Expect(r.fx.commands()).To(HaveLen(1), "the scoped chat ran a command on this machine")
		Expect(r.h.TurnPrompt(rec.ID)).To(ContainSubstring("nothing in this chat runs code or commands"))
		g := shellGroup(r.svc, alice, rec.ID)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Reason).To(ContainSubstring("works in one folder"))
		Expect(g.Reason).To(ContainSubstring("only in an add-on that isolates them"))
		cmds, err := r.svc.CommandRunner(r.ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(cmds.State).NotTo(Equal(studio.CommandsAvailable))
	})

	It("tells a scoped chat with the Sandbox where its commands start and what they keep", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		r := newCommandRig(func(d *studio.Deps) { d.AddonTools = fa })
		plain := create(r.svc, alice)
		sent(r.svc, alice, plain, "hi")
		idle(r.svc, alice, plain)
		Expect(r.h.TurnPrompt(plain)).To(ContainSubstring("It starts in $WORK, the chat's work folder in the user's home"))

		for mode, kept := range map[string]string{agents.FolderWrite: "/workspace/home or /workspace/chat is kept", agents.FolderRead: "under /workspace/chat is kept"} {
			a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Acme " + mode, Addons: []string{"sandbox"}}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: mode})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "run")
			idle(r.svc, alice, rec.ID)
			Expect(r.fx.commands()).To(BeEmpty(), "the built-in bash ran")
			prompt := r.h.TurnPrompt(rec.ID)
			Expect(prompt).To(ContainSubstring("It starts in $WORK, the chat's work folder /workspace/chat/work"), mode)
			Expect(prompt).To(ContainSubstring("the chat's own folder is /workspace/chat, and shared folders are read only"), mode)
			Expect(prompt).To(ContainSubstring(kept), mode)
			Expect(prompt).NotTo(ContainSubstring("the user's home"), mode)
		}
	})

	It("runs a real command that sees and writes only the chat's mounts", func() {
		r := newCommandRig()
		rec := r.scopedChat(agents.FolderWrite)
		out, err := r.bash(rec.ID, "ls; echo ---; ls ..; echo ---; cat in.txt")
		Expect(err).NotTo(HaveOccurred())
		got, err := r.svc.Get(r.ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folder).NotTo(BeEmpty())
		chatDir := "/" + session.ChatsDir + "/" + got.Folder
		Expect(out).To(HavePrefix("in.txt\n---\n"))
		Expect(out).To(ContainSubstring("\nchat\n"))
		Expect(out).To(HaveSuffix("---\ninside\n"))

		out, err = r.bash(rec.ID, "cat ../secret.txt ../../secret.txt secret.txt ../Other/x.txt 2>&1; grep -r "+outsideMark+" .. 2>&1; ls -R .. 2>&1; true")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring(outsideMark))
		Expect(out).NotTo(ContainSubstring("secret.txt\n"))

		out, err = r.bash(rec.ID, "echo made > made.txt; echo kept > ../chat/kept.txt; echo x > ../escape.txt; mkdir -p ../Other; echo x > ../Other/y.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("/workspace/escape.txt was not saved"))
		Expect(r.exists("/Projects/Acme/made.txt")).To(BeTrue())
		Expect(r.exists(chatDir + "/kept.txt")).To(BeTrue())
		Expect(r.exists("/escape.txt")).To(BeFalse())
		Expect(r.exists("/made.txt")).To(BeFalse())
		Expect(r.exists("/Other/y.txt")).To(BeFalse())
	})

	It("names an attachment at the path its commands see, and keeps the home path in an unscoped chat", func() {
		r := newCommandRig()
		rec := r.scopedChat(agents.FolderWrite)
		_, err := r.svc.SendWithFiles(r.ctx, alice, rec.ID, "sum it", []session.Upload{upload("q1.csv", "1,2\n")})
		Expect(err).NotTo(HaveOccurred())
		idle(r.svc, alice, rec.ID)
		sentText := r.h.Sent(rec.ID)
		Expect(sentText).NotTo(BeEmpty())
		Expect(sentText[len(sentText)-1]).To(HaveSuffix("The user attached these files:\n- /workspace/chat/q1.csv"))
		out, err := r.bash(rec.ID, "cat /workspace/chat/q1.csv 2>/dev/null || cat ../chat/q1.csv")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("1,2\n"))
		lines := userLines(r.svc, alice, rec.ID)
		Expect(lines[len(lines)-1].Attachments[0].Path).To(HavePrefix("/"+session.ChatsDir+"/"), "the transcript keeps the path of the files API")

		plain := create(r.svc, alice)
		_, err = r.svc.SendWithFiles(r.ctx, alice, plain, "sum it", []session.Upload{upload("q2.csv", "3\n")})
		Expect(err).NotTo(HaveOccurred())
		idle(r.svc, alice, plain)
		sentText = r.h.Sent(plain)
		Expect(sentText[len(sentText)-1]).To(MatchRegexp(`The user attached these files:\n- /workspace/home/Chats/[^\n]+/q2\.csv$`))
	})

	It("cannot change a read-only folder, and the agent's new mode applies at the next command", func() {
		r := newCommandRig()
		a, err := r.svc.SaveAgentWithFolder(r.ctx, alice, agents.Agent{Name: "Acme"}, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(r.ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.bash(rec.ID, "echo one > one.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.exists("/Projects/Acme/one.txt")).To(BeTrue())

		_, err = r.svc.SaveAgentWithFolder(r.ctx, alice, a, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		out, err := r.bash(rec.ID, "echo two > two.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("/workspace/home/two.txt was not saved: that folder is read-only"))
		Expect(r.exists("/Projects/Acme/two.txt")).To(BeFalse())
	})

	It("fails the command of a chat whose agent's folder is gone, and never runs it on the home", func() {
		r := newCommandRig()
		rec := r.scopedChat(agents.FolderWrite)
		Expect(r.f.Store().Delete(r.ctx, r.f.Actor(alice), r.home+"/Projects/Acme")).To(Succeed())
		out, err := r.bash(rec.ID, "cat ../secret.txt; echo x > x.txt")
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
		Expect(out).NotTo(ContainSubstring(outsideMark))
		Expect(r.exists("/x.txt")).To(BeFalse())
	})

	It("refuses the view of an actor that is not the chat's", func() {
		r := newCommandRig()
		rec := r.scopedChat(agents.FolderWrite)
		a := files.UserActor(alice)
		a.ChatID = rec.ID
		_, err := r.svc.CommandView(r.ctx, a)
		Expect(err).To(MatchError(studio.ErrCommandAccessChanged))
	})
})
