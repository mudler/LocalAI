package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeAddons stands in for the broker: each user sees the tools listed for them.
type fakeAddons struct {
	mu    sync.Mutex
	tools map[string][]studio.AddonTool
	calls []string
	err   error
}

func (f *fakeAddons) Tools(_ context.Context, user string) ([]studio.AddonTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tools[user], f.err
}

func (f *fakeAddons) Call(_ context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s|%s|%s|%s", user, conv, name, args))
	return "result of " + name, nil
}

// gatedAddons is a fakeAddons that also knows which add-ons cannot work now.
type gatedAddons struct {
	*fakeAddons
	reasons map[string]string
}

func (g gatedAddons) Unavailable(_ context.Context, _, addon string, _ bool) string {
	return g.reasons[addon]
}

func addonTool(addon, tool string) studio.AddonTool {
	return studio.AddonTool{Name: "addon__" + addon + "__" + tool, AddonID: addon, Description: tool, Schema: json.RawMessage(`{"type":"object"}`)}
}

func newAddonChat(fa *fakeAddons, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	return newToolService(script, func(d *studio.Deps) { d.AddonTools = fa })
}

var _ = Describe("addontools", func() {
	It("a chat gets the addon tools of its user only", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{
			alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")},
			"bob": {addonTool("office", "read-doc")},
		}}
		svc, h := newAddonChat(fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		a, b := create(svc, alice), create(svc, "bob")
		sent(svc, alice, a, "hi")
		idle(svc, alice, a)
		sent(svc, "bob", b, "hi")
		idle(svc, "bob", b)
		got := h.ToolNames(a)
		Expect(got).To(ContainElement("addon__office__read-doc"), "alice's tools = %v, want both add-on tools", got)
		Expect(got).To(ContainElement("addon__sandbox__bash"), "alice's tools = %v, want both add-on tools", got)
		got = h.ToolNames(b)
		Expect(got).NotTo(ContainElement("addon__sandbox__bash"), "bob's tools = %v, want the office tool only", got)
		Expect(got).To(ContainElement("addon__office__read-doc"), "bob's tools = %v, want the office tool only", got)
	})

	It("an addon tool runs as the user in their chat", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc")}}}
		svc, h := newAddonChat(fa, func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "addon__office__read-doc", Args: `{"path":"a.docx"}`}, {Say: "done"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		rs := h.ResultsFor("addon__office__read-doc")
		Expect(rs).To(HaveLen(1), "results = %+v", rs)
		Expect(rs[0].Out).To(Equal("result of addon__office__read-doc"), "results = %+v", rs)
		Expect(rs[0].Err).To(BeNil(), "results = %+v", rs)
		want := fmt.Sprintf("%s|%s|addon__office__read-doc|{\"path\":\"a.docx\"}", alice, id)
		Expect(fa.calls).To(HaveLen(1), "calls = %v, want %q", fa.calls, want)
		Expect(fa.calls[0]).To(Equal(want), "calls = %v, want %q", fa.calls, want)
	})

	It("a switched off addon leaves the chat at the next turn", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
		svc, h := newAddonChat(fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id := create(svc, alice)
		sent(svc, alice, id, "one")
		idle(svc, alice, id)
		Expect(svc.SetAddonsOff(context.Background(), alice, id, []string{"sandbox", "sandbox"})).To(Succeed())
		sent(svc, alice, id, "two")
		idle(svc, alice, id)
		got := h.ToolNames(id)
		Expect(got).NotTo(ContainElement("addon__sandbox__bash"), "tools after switching sandbox off = %v", got)
		Expect(got).To(ContainElement("addon__office__read-doc"), "tools after switching sandbox off = %v", got)
		list, err := svc.Addons(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred(), "Addons = %+v, %v", list, err)
		Expect(list).To(HaveLen(2), "Addons = %+v, %v", list, err)
		for _, a := range list {
			Expect(a.Off).To(Equal(a.ID == "sandbox"), "addon %+v, want only sandbox off and one tool each", a)
			Expect(a.Tools).To(BeNumerically("==", 1), "addon %+v, want only sandbox off and one tool each", a)
		}
	})

	It("addon switches are checked and belong to the owner", func() {
		svc, _ := newAddonChat(&fakeAddons{}, nil)
		id := create(svc, alice)
		ctx := context.Background()
		for _, bad := range [][]string{{"Not Valid"}, {"../x"}, {""}, {"a__b"}} {
			Expect(svc.SetAddonsOff(ctx, alice, id, bad)).To(MatchError(studio.ErrBadAddonID))
		}
		Expect(svc.SetAddonsOff(ctx, "bob", id, []string{"office"})).To(MatchError(session.ErrNotFound))
	})

	It("a sub agent gets only the addon tools its parent names", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
		svc, h := newAddonChat(fa, func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","tools":["addon__office__read-doc"],"wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "child done"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %d", len(kids))
		got := h.ToolNames(kids[0].ID)
		Expect(got).NotTo(ContainElement("addon__sandbox__bash"), "sub-agent tools = %v, want only the office tool of the add-ons", got)
		Expect(got).To(ContainElement("addon__office__read-doc"), "sub-agent tools = %v, want only the office tool of the add-ons", got)
	})

	It("a broken addon host does not fail the chat", func() {
		fa := &fakeAddons{err: errors.New("owner unreachable")}
		svc, _ := newAddonChat(fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})

	It("an addon that cannot work is replaced by a tool that says so", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {
			addonTool("office", "read-doc"),
			{Name: "addon__sandbox__bash", AddonID: "sandbox", Action: true, Description: "bash", Schema: json.RawMessage(`{"type":"object"}`)},
			addonTool("sandbox", "status"),
		}}}
		gate := gatedAddons{fakeAddons: fa, reasons: map[string]string{"sandbox": "no sandbox backend works on this host"}}
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "addon__sandbox__unavailable", Args: `{}`}, {Say: "ok"}}
		}, func(d *studio.Deps) { d.AddonTools = gate })
		id := create(svc, alice)
		sent(svc, alice, id, "run it")
		idle(svc, alice, id)
		got := h.ToolNames(id)
		Expect(got).NotTo(ContainElement("addon__sandbox__bash"), "tools = %v, the sandbox tools are still offered", got)
		Expect(got).NotTo(ContainElement("addon__sandbox__status"), "tools = %v, the sandbox tools are still offered", got)
		Expect(got).To(ContainElement("addon__sandbox__unavailable"), "tools = %v, want the stand-in tool and the other add-on", got)
		Expect(got).To(ContainElement("addon__office__read-doc"), "tools = %v, want the stand-in tool and the other add-on", got)
		rs := h.ResultsFor("addon__sandbox__unavailable")
		Expect(rs).To(HaveLen(1), "results = %+v", rs)
		Expect(rs[0].Err).To(BeNil(), "results = %+v", rs)
		Expect(rs[0].Out).To(ContainSubstring("no sandbox backend works on this host"), "results = %+v", rs)
		Expect(fa.calls).To(BeEmpty(), "the stand-in tool called the add-on: %v", fa.calls)
	})
})
