package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The deferred tools of the add-ons are one more source behind the tool
// search. The broker is a fake: what is tried here is which tools the chat
// sends, and that tool_call reaches the very function a typed call reaches.
var _ = Describe("the deferred tools of the add-ons in the tool search", func() {
	ctx := context.Background()
	const (
		listNotes = "addon__notes__list-notes"
		addNote   = "addon__notes__add-note"
		ping      = "addon__example__ping"
	)
	schema := json.RawMessage(`{"type":"object","properties":{"folder":{"type":"string"}}}`)
	deferredTool := func(addon, tool, desc string, write bool) studio.AddonTool {
		t := studio.AddonTool{Name: "addon__" + addon + "__" + tool, AddonID: addon, Tool: tool, Description: desc, Schema: schema, Deferred: true, Action: write}
		return t
	}
	// bulk is a description whose schema costs about a thousand tokens.
	bulk := func(s string) string { return s + ". " + strings.Repeat("It works on one folder of notes. ", 60) }
	set := func() []studio.AddonTool {
		plain := addonTool("example", "ping")
		return []studio.AddonTool{
			deferredTool("notes", "list-notes", bulk("List the notes of a folder"), false),
			deferredTool("notes", "add-note", bulk("Add a note to a folder"), true),
			plain,
		}
	}
	chat := func(mode string, window int, tools []studio.AddonTool, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness, *fakeAddons) {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: tools}}
		svc, h := newToolService(script, func(d *studio.Deps) {
			d.AddonTools = fa
			withSearch(mode, window)(d)
		})
		return svc, h, fa
	}
	say := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }
	turnNames := func(svc *studio.Service, h *studiotest.ToolHarness, id string) []string {
		GinkgoHelper()
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		return h.ToolNames(id)
	}

	DescribeTable("is decided by the setting and the window, counting deferrable tools only",
		func(mode string, window int, defers bool) {
			svc, h, _ := chat(mode, window, set(), say)
			names := turnNames(svc, h, create(svc, alice))
			Expect(names).To(ContainElement(ping), "a tool that is not deferred stays typed")
			if defers {
				Expect(names).To(ContainElements(bridgeNames))
				Expect(names).NotTo(ContainElements(listNotes, addNote))
			} else {
				Expect(names).NotTo(ContainElement("tool_search"))
				Expect(names).To(ContainElements(listNotes, addNote))
			}
		},
		Entry("off, small window", config.ToolSearchOff, 4000, false),
		Entry("on, large window", config.ToolSearchOn, 1<<22, true),
		Entry("auto, small window", config.ToolSearchAuto, 4000, true),
		Entry("auto, large window", config.ToolSearchAuto, 1<<22, false),
		Entry("auto, unknown window", config.ToolSearchAuto, 0, false),
	)

	It("does not count a tool that is not deferred toward the threshold of auto", func() {
		heavy := addonTool("example", "heavy")
		heavy.Description = strings.Repeat("A very long description. ", 4000)
		small := deferredTool("notes", "list-notes", "List the notes of a folder", false)
		svc, h, _ := chat(config.ToolSearchAuto, 20000, []studio.AddonTool{small, heavy}, say)
		names := turnNames(svc, h, create(svc, alice))
		Expect(names).To(ContainElements(listNotes, "addon__example__heavy"))
		Expect(names).NotTo(ContainElement("tool_search"))
	})

	It("changes nothing for an add-on that declares no deferred tool, even with the setting on", func() {
		svc, h, _ := chat(config.ToolSearchOn, 0, []studio.AddonTool{addonTool("example", "ping")}, say)
		names := turnNames(svc, h, create(svc, alice))
		Expect(names).To(ContainElement(ping))
		Expect(names).NotTo(ContainElement("tool_search"))
	})

	It("sends the same tools at every turn of a chat", func() {
		svc, h, _ := chat(config.ToolSearchAuto, 4000, set(), say)
		id := create(svc, alice)
		first := slices.Clone(turnNames(svc, h, id))
		for i := 0; i < 3; i++ {
			Expect(turnNames(svc, h, id)).To(ConsistOf(first))
		}
	})

	It("keeps a tool that shadows a built-in tool typed, whatever the setting says", func() {
		fetch := deferredTool("searx", "fetch", "Fetch a page", false)
		fetch.Shadows = "web_fetch"
		svc, h, _ := chat(config.ToolSearchOn, 0, []studio.AddonTool{fetch}, say)
		names := turnNames(svc, h, create(svc, alice))
		Expect(names).To(ContainElement("web_fetch"))
		Expect(names).NotTo(ContainElement("tool_search"))
	})

	It("finds a deferred tool by words of its name or description, and cuts the line to 160 characters", func() {
		svc, h, _ := chat(config.ToolSearchOn, 0, set(), func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "tool_search", Args: `{"query":"list notes"}`}, {Tool: "tool_search", Args: `{"query":"ping"}`}, {Say: "done"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("tool_search")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Out).To(ContainSubstring(listNotes + ": List the notes of a folder."))
		for _, l := range strings.Split(res[0].Out, "\n") {
			desc := l[strings.Index(l, ": ")+2:]
			Expect([]rune(desc)).To(Satisfy(func(r []rune) bool { return len(r) <= 160 }), l)
		}
		Expect(res[1].Out).To(Equal("No tool matches. Try other words."), "a tool that is typed is not in the search")
	})

	It("describes a deferred tool with the schema the typed tool would have, under its add-on name", func() {
		svc, h, _ := chat(config.ToolSearchOn, 0, set(), viaBridge("tool_describe", `{"name":"`+listNotes+`"}`))
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("tool_describe")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(ContainSubstring(`"name":"` + listNotes + `"`))
		Expect(res[0].Out).To(ContainSubstring(`"folder"`))
	})

	Describe("tool_call", func() {
		call := func(mode, tool, args string) (*fakeAddons, []studiotest.ToolResult, *studio.Service, string) {
			svc, h, fa := chat(mode, 0, set(), func(workspace.Actor, string) []studiotest.ToolStep {
				if mode == config.ToolSearchOn {
					return []studiotest.ToolStep{{Tool: "tool_call", Args: `{"name":"` + tool + `","arguments":` + args + `}`}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Tool: tool, Args: args}, {Say: "done"}}
			})
			id := create(svc, alice)
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			name := "tool_call"
			if mode != config.ToolSearchOn {
				name = tool
			}
			return fa, h.ResultsFor(name), svc, id
		}

		It("reaches the broker as the same call a typed tool makes, for the same user and chat", func() {
			typed, typedRes, _, _ := call(config.ToolSearchOff, listNotes, `{"folder":"a"}`)
			viaSearch, res, _, _ := call(config.ToolSearchOn, listNotes, `{"folder":"a"}`)
			Expect(typed.calls).To(HaveLen(1))
			Expect(viaSearch.calls).To(HaveLen(1))
			// The chat ID differs between two services; the rest must be equal.
			Expect(strings.Split(viaSearch.calls[0], "|")[0]).To(Equal(strings.Split(typed.calls[0], "|")[0]))
			Expect(strings.Split(viaSearch.calls[0], "|")[2:]).To(Equal(strings.Split(typed.calls[0], "|")[2:]))
			Expect(res[0].Out).To(Equal(typedRes[0].Out))
		})

		It("leaves a deferred write tool as a proposal, as a typed call does", func() {
			held := addontools.HeldProposal{ID: "p1", Digest: "d1", AddonID: "notes", Tool: addNote, Title: "Add a note", Hold: "approval_always"}
			outs := map[string]string{}
			for _, mode := range []string{config.ToolSearchOff, config.ToolSearchOn} {
				fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: set()}, held: &held, heldTool: addNote}
				script := func(workspace.Actor, string) []studiotest.ToolStep {
					if mode == config.ToolSearchOn {
						return []studiotest.ToolStep{{Tool: "tool_call", Args: `{"name":"` + addNote + `","arguments":{"folder":"a"}}`}, {Say: "done"}}
					}
					return []studiotest.ToolStep{{Tool: addNote, Args: `{"folder":"a"}`}, {Say: "done"}}
				}
				svc, h := newToolService(script, func(d *studio.Deps) { d.AddonTools = fa; withSearch(mode, 0)(d) })
				id := create(svc, alice)
				sent(svc, alice, id, "go")
				idle(svc, alice, id)
				name := addNote
				if mode == config.ToolSearchOn {
					name = "tool_call"
				}
				res := h.ResultsFor(name)
				Expect(res).To(HaveLen(1))
				Expect(res[0].Err).NotTo(HaveOccurred())
				Expect(res[0].Out).To(ContainSubstring("Nothing has been changed"))
				outs[mode] = res[0].Out
				// The proposal is held for a person: nothing is confirmed here.
				Expect(fa.confirmed).To(BeEmpty())
			}
			Expect(outs[config.ToolSearchOn]).To(Equal(outs[config.ToolSearchOff]))
		})

		It("answers no such tool for a typed add-on tool, which is not behind the search", func() {
			_, res, _, _ := call(config.ToolSearchOn, ping, `{}`)
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("no such tool")))
		})

		It("answers no such tool for an add-on the chat switched off, and does not list its tools", func() {
			// A second add-on keeps the bridge in the chat, so the answer is the
			// bridge's and not a missing tool.
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: append(set(), deferredTool("example", "lookup", "Look a word up", false))}}
			svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "tool_search", Args: `{"query":"list notes"}`}, {Tool: "tool_call", Args: `{"name":"` + listNotes + `","arguments":{}}`}, {Say: "done"}}
			}, func(d *studio.Deps) { d.AddonTools = fa; withSearch(config.ToolSearchOn, 0)(d) })
			id := create(svc, alice)
			Expect(svc.SetAddonsOff(ctx, alice, id, []string{"notes"})).To(Succeed())
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			Expect(h.ResultsFor("tool_search")[0].Out).NotTo(ContainSubstring(listNotes))
			res := h.ResultsFor("tool_call")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(fa.calls).To(BeEmpty())
		})

		It("answers no such tool for an add-on the broker does not list for the user", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: set(), "bob": {deferredTool("example", "lookup", "Look a word up", false)}}}
			svc, h := newToolService(viaBridge("tool_call", `{"name":"`+listNotes+`","arguments":{}}`),
				func(d *studio.Deps) { d.AddonTools = fa; withSearch(config.ToolSearchOn, 0)(d) })
			id := create(svc, "bob")
			sent(svc, "bob", id, "go")
			idle(svc, "bob", id)
			Expect(h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(fa.calls).To(BeEmpty())
		})
	})

	It("keeps the add-on gate: an add-on that cannot work has only its stand-in tool, never a deferred one", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: set()}}
		svc, h := newToolService(say, func(d *studio.Deps) {
			d.AddonTools = gatedAddons{fakeAddons: fa, reasons: map[string]string{"notes": "its backend is not running"}}
			withSearch(config.ToolSearchOn, 0)(d)
		})
		names := turnNames(svc, h, create(svc, alice))
		Expect(names).To(ContainElement("addon__notes__unavailable"))
		Expect(names).NotTo(ContainElement("tool_search"))
		Expect(names).NotTo(ContainElements(listNotes, addNote))
	})
})
