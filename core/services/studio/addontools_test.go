package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

func newAddonChat(t *testing.T, fa *fakeAddons, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	t.Helper()
	return newToolService(t, script, func(d *studio.Deps) { d.AddonTools = fa })
}

func TestAChatGetsTheAddonToolsOfItsUserOnly(t *testing.T) {
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{
		alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")},
		"bob": {addonTool("office", "read-doc")},
	}}
	svc, h := newAddonChat(t, fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
	a, b := create(t, svc, alice), create(t, svc, "bob")
	sent(t, svc, alice, a, "hi")
	idle(t, svc, alice, a)
	sent(t, svc, "bob", b, "hi")
	idle(t, svc, "bob", b)
	if got := h.ToolNames(a); !slices.Contains(got, "addon__office__read-doc") || !slices.Contains(got, "addon__sandbox__bash") {
		t.Errorf("alice's tools = %v, want both add-on tools", got)
	}
	if got := h.ToolNames(b); slices.Contains(got, "addon__sandbox__bash") || !slices.Contains(got, "addon__office__read-doc") {
		t.Errorf("bob's tools = %v, want the office tool only", got)
	}
}

func TestAnAddonToolRunsAsTheUserInTheirChat(t *testing.T) {
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc")}}}
	svc, h := newAddonChat(t, fa, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "addon__office__read-doc", Args: `{"path":"a.docx"}`}, {Say: "done"}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	rs := h.ResultsFor("addon__office__read-doc")
	if len(rs) != 1 || rs[0].Out != "result of addon__office__read-doc" || rs[0].Err != nil {
		t.Fatalf("results = %+v", rs)
	}
	want := fmt.Sprintf("%s|%s|addon__office__read-doc|{\"path\":\"a.docx\"}", alice, id)
	if len(fa.calls) != 1 || fa.calls[0] != want {
		t.Fatalf("calls = %v, want %q", fa.calls, want)
	}
}

func TestASwitchedOffAddonLeavesTheChatAtTheNextTurn(t *testing.T) {
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
	svc, h := newAddonChat(t, fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "one")
	idle(t, svc, alice, id)
	if err := svc.SetAddonsOff(context.Background(), alice, id, []string{"sandbox", "sandbox"}); err != nil {
		t.Fatal(err)
	}
	sent(t, svc, alice, id, "two")
	idle(t, svc, alice, id)
	got := h.ToolNames(id)
	if slices.Contains(got, "addon__sandbox__bash") || !slices.Contains(got, "addon__office__read-doc") {
		t.Fatalf("tools after switching sandbox off = %v", got)
	}
	list, err := svc.Addons(context.Background(), alice, id)
	if err != nil || len(list) != 2 {
		t.Fatalf("Addons = %+v, %v", list, err)
	}
	for _, a := range list {
		if a.Off != (a.ID == "sandbox") || a.Tools != 1 {
			t.Errorf("addon %+v, want only sandbox off and one tool each", a)
		}
	}
}

func TestAddonSwitchesAreCheckedAndBelongToTheOwner(t *testing.T) {
	svc, _ := newAddonChat(t, &fakeAddons{}, nil)
	id := create(t, svc, alice)
	ctx := context.Background()
	for _, bad := range [][]string{{"Not Valid"}, {"../x"}, {""}, {"a__b"}} {
		if err := svc.SetAddonsOff(ctx, alice, id, bad); !errors.Is(err, studio.ErrBadAddonID) {
			t.Errorf("SetAddonsOff(%q) = %v, want ErrBadAddonID", bad, err)
		}
	}
	if err := svc.SetAddonsOff(ctx, "bob", id, []string{"office"}); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("another user's SetAddonsOff = %v, want ErrNotFound", err)
	}
}

func TestASubAgentGetsOnlyTheAddonToolsItsParentNames(t *testing.T) {
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
	svc, h := newAddonChat(t, fa, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","tools":["addon__office__read-doc"],"wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "child done"}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 {
		t.Fatalf("children = %d", len(kids))
	}
	got := h.ToolNames(kids[0].ID)
	if slices.Contains(got, "addon__sandbox__bash") || !slices.Contains(got, "addon__office__read-doc") {
		t.Fatalf("sub-agent tools = %v, want only the office tool of the add-ons", got)
	}
}

func TestABrokenAddonHostDoesNotFailTheChat(t *testing.T) {
	fa := &fakeAddons{err: errors.New("owner unreachable")}
	svc, _ := newAddonChat(t, fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	idle(t, svc, alice, id)
	if got := turn(t, svc, alice, id).Last; got != "done" {
		t.Fatalf("the turn ended %q, want done", got)
	}
}

func TestAnAddonThatCannotWorkIsReplacedByAToolThatSaysSo(t *testing.T) {
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {
		addonTool("office", "read-doc"),
		{Name: "addon__sandbox__bash", AddonID: "sandbox", Action: true, Description: "bash", Schema: json.RawMessage(`{"type":"object"}`)},
		addonTool("sandbox", "status"),
	}}}
	gate := gatedAddons{fakeAddons: fa, reasons: map[string]string{"sandbox": "no sandbox backend works on this host"}}
	svc, h := newToolService(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "addon__sandbox__unavailable", Args: `{}`}, {Say: "ok"}}
	}, func(d *studio.Deps) { d.AddonTools = gate })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "run it")
	idle(t, svc, alice, id)
	got := h.ToolNames(id)
	if slices.Contains(got, "addon__sandbox__bash") || slices.Contains(got, "addon__sandbox__status") {
		t.Errorf("tools = %v, the sandbox tools are still offered", got)
	}
	if !slices.Contains(got, "addon__sandbox__unavailable") || !slices.Contains(got, "addon__office__read-doc") {
		t.Errorf("tools = %v, want the stand-in tool and the other add-on", got)
	}
	rs := h.ResultsFor("addon__sandbox__unavailable")
	if len(rs) != 1 || rs[0].Err != nil || !strings.Contains(rs[0].Out, "no sandbox backend works on this host") {
		t.Fatalf("results = %+v", rs)
	}
	if len(fa.calls) != 0 {
		t.Errorf("the stand-in tool called the add-on: %v", fa.calls)
	}
}
