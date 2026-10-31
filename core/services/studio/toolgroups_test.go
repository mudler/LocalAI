package studio_test

import (
	"context"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// groupsScript spawns a sub-agent on "spawn", else answers.
func groupsScript(_ workspace.Actor, text string) []studiotest.ToolStep {
	if text == "spawn" {
		return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","wait":true}`}, {Say: "done"}}
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

// newGroupsChat is a chat service with files, the image and audio helpers,
// collections and a broker, so that every switchable group exists.
func newGroupsChat(broker studio.AddonTools, opts ...func(*studio.Deps)) (*studio.Service, *studiotest.ToolHarness) {
	f := newFiles()
	m, _ := newMediaService()
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"docs": nil}}, blocked: map[string]bool{bob: true}}
	return newToolService(groupsScript, append([]func(*studio.Deps){func(d *studio.Deps) {
		d.Files, d.Media, d.Collections = f, m, fc
		if broker != nil {
			d.AddonTools = broker
		}
	}}, opts...)...)
}

// without returns the names of all that are not in some.
func without(all, some []string) []string {
	return slices.DeleteFunc(slices.Clone(all), func(n string) bool { return slices.Contains(some, n) })
}

var _ = Describe("the switchable groups of built-in tools", func() {
	fileTools := []string{"list_files", "read_file", "write_file", "edit_file", "move_file", "delete_file", "search_files", "read_document"}
	groups := map[string][]string{
		builtins.GroupFiles:       fileTools,
		builtins.GroupMedia:       {"describe_image", "transcribe_audio"},
		builtins.GroupCollections: {"list_collections", "search_collection"},
		builtins.GroupSubAgents:   {"spawn_agent"},
	}

	It("takes exactly the tools of the group out of the turn, and puts them back when switched on", func() {
		svc, h := newGroupsChat(nil)
		id := create(svc, alice)
		turnPrompt(svc, h, id)
		all := h.ToolNames(id)
		for _, names := range groups {
			Expect(all).To(ContainElements(names))
		}
		for g, names := range groups {
			Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{g})).To(Succeed())
			turnPrompt(svc, h, id)
			Expect(h.ToolNames(id)).To(ConsistOf(without(all, names)), "group %s", g)
		}
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, nil)).To(Succeed())
		turnPrompt(svc, h, id)
		Expect(h.ToolNames(id)).To(ConsistOf(all))
	})

	It("gives a sub-agent no tool of a group its parent switched off", func() {
		svc, h := newGroupsChat(nil)
		id := create(svc, alice)
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{builtins.GroupFiles})).To(Succeed())
		sent(svc, alice, id, "spawn")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1))
		Expect(h.ToolNames(kids[0].ID)).NotTo(ContainElement(BeElementOf(fileTools)))
		Expect(h.ToolNames(kids[0].ID)).NotTo(BeEmpty(), "the other tools stay")
	})

	It("refuses spawn_agent of a chat that switched the sub-agents off", func() {
		svc, h := newGroupsChat(nil)
		id := create(svc, alice)
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{builtins.GroupSubAgents})).To(Succeed())
		sent(svc, alice, id, "spawn")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).NotTo(ContainElement("spawn_agent"))
		Expect(childrenOf(svc, alice, id)).To(BeEmpty())
	})

	It("tells a chat without the file tools nothing about them, and keeps load_skill", func() {
		svc, h := newGroupsChat(nil)
		f := svc.Files()
		writeExecSkill(f, "xlsx")
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "look", []session.Upload{upload("notes.txt", "hello")})
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		prompt := h.TurnPrompt(id)
		Expect(prompt).To(ContainSubstring("write_file"), "with the file tools on, the commands block offers write_file")
		Expect(prompt).To(ContainSubstring("Skills you write yourself"))
		Expect(prompt).To(ContainSubstring("Working files:"))

		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{builtins.GroupFiles})).To(Succeed())
		prompt = turnPrompt(svc, h, id)
		for _, name := range fileTools {
			Expect(prompt).NotTo(ContainSubstring(name))
		}
		Expect(prompt).NotTo(ContainSubstring("same files as the file tools"))
		Expect(prompt).NotTo(ContainSubstring("Skills you write yourself"), "nothing can save a skill")
		Expect(prompt).NotTo(ContainSubstring("Working files:"), "nothing can save in a work folder")
		Expect(prompt).To(ContainSubstring("notes.txt"), "the chat still lists its files")
		Expect(prompt).To(ContainSubstring("file tools are switched off for this chat"))
		Expect(prompt).To(ContainSubstring("load_skill"), "the skills stay loadable")
		Expect(h.ToolNames(id)).To(ContainElement("load_skill"))
	})

	It("does not name describe_image to a chat that switched the image helpers off", func() {
		svc, h := newGroupsChat(nil)
		id := create(svc, alice)
		Expect(turnPrompt(svc, h, id)).To(ContainSubstring("describe_image"))
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{builtins.GroupMedia})).To(Succeed())
		prompt := turnPrompt(svc, h, id)
		Expect(prompt).NotTo(ContainSubstring("describe_image"))
		Expect(prompt).To(ContainSubstring("image helper is switched off"))
	})

	It("lists the groups in the panel and the draft, with usable and the reason", func() {
		svc, _ := newGroupsChat(nil)
		id := create(svc, alice)
		p, err := svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(builtinGroupNames(p.Builtins)).To(ContainElements("shell", "files", "media", "collections", "subagents"))
		for _, g := range []string{"files", "media", "collections", "subagents"} {
			Expect(builtinGroup(p.Builtins, g).Usable).To(BeTrue(), g)
			Expect(builtinGroup(p.Builtins, g).Off).To(BeFalse(), g)
			Expect(builtinGroup(p.Builtins, g).Tools).To(Equal(groups[g]), g)
		}
		shell := builtinGroup(p.Builtins, "shell")
		Expect(shell.Usable).To(BeFalse(), "nothing runs commands here")
		Expect(shell.Reason).To(ContainSubstring("nothing here runs commands"))

		draft, err := svc.DraftTools(context.Background(), alice, "m", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(builtinGroupNames(draft.Builtins)).To(Equal(builtinGroupNames(p.Builtins)))

		// bob may not use collections: the group is there, not usable.
		bid := create(svc, bob)
		p, err = svc.Tools(context.Background(), bob, bid)
		Expect(err).NotTo(HaveOccurred())
		c := builtinGroup(p.Builtins, "collections")
		Expect(c.Usable).To(BeFalse())
		Expect(c.Reason).To(ContainSubstring("not enabled for this account"))
	})

	It("keeps one user's switch to their own chat", func() {
		svc, h := newGroupsChat(nil)
		mine, theirs := create(svc, alice), create(svc, bob)
		Expect(svc.SetBuiltinsOff(context.Background(), bob, mine, []string{"files"})).To(MatchError(session.ErrNotFound))
		Expect(svc.SetBuiltinsOff(context.Background(), alice, mine, []string{"files"})).To(Succeed())
		p, err := svc.Tools(context.Background(), bob, theirs)
		Expect(err).NotTo(HaveOccurred())
		Expect(builtinGroup(p.Builtins, "files").Off).To(BeFalse())
		sent(svc, bob, theirs, "hi")
		idle(svc, bob, theirs)
		Expect(h.ToolNames(theirs)).To(ContainElement("read_file"))
	})

	It("accepts the new group names and refuses an unknown one", func() {
		svc, _ := newGroupsChat(nil)
		id := create(svc, alice)
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{"shell", "files", "media", "collections", "subagents"})).To(Succeed())
		rec, err := svc.Get(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.BuiltinsOff).To(Equal([]string{"shell", "files", "media", "collections", "subagents"}))
		Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{"files", "load_skill"})).To(MatchError(studio.ErrBadBuiltinGroup))
		Expect(svc.SetBuiltinsOn(context.Background(), alice, id, []string{"files"})).To(MatchError(studio.ErrBadBuiltinGroup), "files is on unless switched off")
	})

	Context("the shell group", func() {
		It("hides the Sandbox's bash and says so to the model", func() {
			svc, h := newGroupsChat(isolatedAddons{&fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}, "isolated"})
			id := create(svc, alice)
			turnPrompt(svc, h, id)
			Expect(h.ToolNames(id)).To(ContainElement("bash"))
			p, err := svc.Tools(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			shell := builtinGroup(p.Builtins, "shell")
			Expect(shell.Usable).To(BeTrue())
			Expect(shell.Provider).To(Equal("sandbox"))

			Expect(svc.SetBuiltinsOff(context.Background(), alice, id, []string{"shell"})).To(Succeed())
			prompt := turnPrompt(svc, h, id)
			Expect(h.ToolNames(id)).NotTo(ContainElement("bash"))
			Expect(h.ToolNames(id)).To(ContainElement("addon__sandbox__read"), "the add-on's other tool stays")
			Expect(prompt).To(ContainSubstring("switched running commands off for this chat"))
			c, err := svc.CommandRunner(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.State).To(Equal(studio.CommandsSwitchedOff))
			p, err = svc.Tools(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			shell = builtinGroup(p.Builtins, "shell")
			Expect(shell.Off).To(BeTrue())
			Expect(shell.Provider).To(Equal("sandbox"), "the row still says who would serve")
		})

		It("hides the built-in bash on this machine", func() {
			c := newHostChat("on", nil)
			id := create(c.svc, alice)
			sent(c.svc, alice, id, "hi")
			idle(c.svc, alice, id)
			Expect(c.h.ToolNames(id)).To(ContainElement("bash"))
			p, err := c.svc.Tools(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(builtinGroup(p.Builtins, "shell").Provider).To(Equal(studio.BuiltinProvider))

			Expect(c.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"shell"})).To(Succeed())
			sent(c.svc, alice, id, "run")
			idle(c.svc, alice, id)
			Expect(c.h.ToolNames(id)).NotTo(ContainElement("bash"))
			Expect(c.fx.commands()).To(BeEmpty())
			Expect(c.h.TurnPrompt(id)).NotTo(ContainSubstring("runs commands directly on this machine"))
		})

		It("is listed but not usable when the built-in bash is off and no add-on runs commands", func() {
			c := newHostChat("off", nil)
			id := create(c.svc, alice)
			p, err := c.svc.Tools(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			shell := builtinGroup(p.Builtins, "shell")
			Expect(shell.Group).To(Equal("shell"))
			Expect(shell.Usable).To(BeFalse())
			Expect(shell.Reason).NotTo(BeEmpty())
		})
	})
})
