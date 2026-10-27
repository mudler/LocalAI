package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"
)

// fakeAssistant stands in for the LocalAI assistant tool set.
type fakeAssistant struct {
	mu      sync.Mutex
	enabled bool
	calls   []string
}

func (f *fakeAssistant) Enabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enabled
}
func (f *fakeAssistant) set(on bool) { f.mu.Lock(); f.enabled = on; f.mu.Unlock() }
func (f *fakeAssistant) SystemPrompt() string {
	return "You manage this LocalAI instance."
}
func (f *fakeAssistant) Executor() mcpendpoint.ToolExecutor { return f }

func (f *fakeAssistant) DiscoverTools(context.Context) ([]functions.Function, error) {
	return []functions.Function{{Name: "install_model", Description: "install a model", Parameters: map[string]any{"type": "object"}}}, nil
}
func (f *fakeAssistant) IsTool(n string) bool { return n == "install_model" }
func (f *fakeAssistant) HasTools() bool       { return true }
func (f *fakeAssistant) ExecuteTool(_ context.Context, name, args string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+"|"+args)
	return "installed", nil
}

func assistantUsers(_ context.Context, id string) (*auth.User, error) {
	role := auth.RoleUser
	if id == "root" {
		role = auth.RoleAdmin
	}
	return &auth.User{ID: id, Role: role, Status: auth.StatusActive}, nil
}

func newAssistantService(t *testing.T, fa *fakeAssistant, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	t.Helper()
	return newToolService(t, script, func(d *studio.Deps) {
		d.Assistant, d.Users = fa, assistantUsers
	})
}

func TestAnAssistantChatGetsTheAssistantToolsAndPrompt(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, h := newAssistantService(t, fa, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "install_model", Args: `{"name":"x"}`}, {Say: "done"}}
	})
	rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
	if err != nil || !rec.Assistant {
		t.Fatalf("Create = %+v, %v; want an assistant chat", rec, err)
	}
	sent(t, svc, "root", rec.ID, "go")
	idle(t, svc, "root", rec.ID)
	if !slices.Contains(h.ToolNames(rec.ID), "install_model") {
		t.Errorf("tools = %v, want install_model", h.ToolNames(rec.ID))
	}
	if p := h.TurnPrompt(rec.ID); !strings.Contains(p, "manage this LocalAI instance") {
		t.Errorf("turn prompt = %q, want the assistant prompt", p)
	}
	if rs := h.ResultsFor("install_model"); len(rs) != 1 || rs[0].Out != "installed" {
		t.Errorf("results = %+v", rs)
	}
	if got, err := svc.Get(context.Background(), "root", rec.ID); err != nil || !got.Assistant {
		t.Errorf("Get = %+v, %v; the flag must persist", got, err)
	}
}

func TestAPlainChatNeverGetsTheAssistantTools(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, h := newAssistantService(t, fa, sayOK)
	id := create(t, svc, "root")
	sent(t, svc, "root", id, "hi")
	idle(t, svc, "root", id)
	if slices.Contains(h.ToolNames(id), "install_model") || strings.Contains(h.TurnPrompt(id), "manage this LocalAI") {
		t.Errorf("a plain chat got the assistant: tools %v prompt %q", h.ToolNames(id), h.TurnPrompt(id))
	}
}

func TestANonAdminCannotStartAnAssistantChat(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, _ := newAssistantService(t, fa, sayOK)
	_, err := svc.Create(context.Background(), "bob", studio.CreateInput{Model: "m", Assistant: true})
	if !errors.Is(err, studio.ErrAssistantForbidden) {
		t.Fatalf("err = %v, want ErrAssistantForbidden", err)
	}
	if recs, _ := svc.List(context.Background(), "bob", 10); len(recs) != 0 {
		t.Errorf("a refused chat was stored: %v", recs)
	}
}

func TestAnAssistantChatIsRefusedWhenTheSettingIsOff(t *testing.T) {
	fa := &fakeAssistant{enabled: false}
	svc, _ := newAssistantService(t, fa, sayOK)
	if _, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true}); !errors.Is(err, studio.ErrAssistantOff) {
		t.Fatalf("err = %v, want ErrAssistantOff", err)
	}
	// Without a host at all it is off too.
	svc2, _ := newToolService(t, sayOK, func(d *studio.Deps) { d.Users = assistantUsers })
	if _, err := svc2.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true}); !errors.Is(err, studio.ErrAssistantOff) {
		t.Fatalf("err = %v, want ErrAssistantOff without a host", err)
	}
}

func TestTheAssistantToolsGoWhenTheSettingIsSwitchedOffLater(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, h := newAssistantService(t, fa, sayOK)
	rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
	if err != nil {
		t.Fatal(err)
	}
	fa.set(false)
	sent(t, svc, "root", rec.ID, "hi")
	idle(t, svc, "root", rec.ID)
	if slices.Contains(h.ToolNames(rec.ID), "install_model") || strings.Contains(h.TurnPrompt(rec.ID), "manage this LocalAI") {
		t.Errorf("a switched-off assistant still gave tools %v prompt %q", h.ToolNames(rec.ID), h.TurnPrompt(rec.ID))
	}
}

func TestAssistantToolsAreNotHandedToSubAgents(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, h := newAssistantService(t, fa, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "seen"}}
	})
	rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
	if err != nil {
		t.Fatal(err)
	}
	sent(t, svc, "root", rec.ID, "go")
	idle(t, svc, "root", rec.ID)
	for _, k := range childrenOf(t, svc, "root", rec.ID) {
		if slices.Contains(h.ToolNames(k.ID), "install_model") {
			t.Errorf("a sub-agent got the assistant tools: %v", h.ToolNames(k.ID))
		}
	}
}

func TestAnAssistantChatCannotNameAnAgent(t *testing.T) {
	fa := &fakeAssistant{enabled: true}
	svc, _ := newAssistantService(t, fa, sayOK)
	_, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true, AgentID: "a1"})
	if !errors.Is(err, studio.ErrAssistantAgent) {
		t.Fatalf("err = %v, want ErrAssistantAgent", err)
	}
}
