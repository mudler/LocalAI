package studio_test

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func newAssistantService(fa *fakeAssistant, script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
	return newToolService(script, func(d *studio.Deps) {
		d.Assistant, d.Users = fa, assistantUsers
	})
}

var _ = Describe("assistant", func() {
	It("an assistant chat gets the assistant tools and prompt", func() {
		fa := &fakeAssistant{enabled: true}
		svc, h := newAssistantService(fa, func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "install_model", Args: `{"name":"x"}`}, {Say: "done"}}
		})
		rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).NotTo(HaveOccurred(), "Create = %+v, %v; want an assistant chat", rec, err)
		Expect(rec.Assistant).To(BeTrue(), "Create = %+v, %v; want an assistant chat", rec, err)
		sent(svc, "root", rec.ID, "go")
		idle(svc, "root", rec.ID)
		Expect(h.ToolNames(rec.ID)).To(ContainElement("install_model"), "tools = %v, want install_model", h.ToolNames(rec.ID))
		Expect(h.TurnPrompt(rec.ID)).To(ContainSubstring("manage this LocalAI instance"))
		rs := h.ResultsFor("install_model")
		Expect(rs).To(HaveLen(1), "results = %+v", rs)
		Expect(rs[0].Out).To(Equal("installed"), "results = %+v", rs)
		got, err := svc.Get(context.Background(), "root", rec.ID)
		Expect(err).NotTo(HaveOccurred(), "Get = %+v, %v; the flag must persist", got, err)
		Expect(got.Assistant).To(BeTrue(), "Get = %+v, %v; the flag must persist", got, err)
	})

	It("a plain chat never gets the assistant tools", func() {
		fa := &fakeAssistant{enabled: true}
		svc, h := newAssistantService(fa, sayOK)
		id := create(svc, "root")
		sent(svc, "root", id, "hi")
		idle(svc, "root", id)
		Expect(h.ToolNames(id)).NotTo(ContainElement("install_model"), "a plain chat got the assistant: tools %v prompt %q", h.ToolNames(id), h.TurnPrompt(id))
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("manage this LocalAI"), "a plain chat got the assistant: tools %v prompt %q", h.ToolNames(id), h.TurnPrompt(id))
	})

	It("a non admin cannot start an assistant chat", func() {
		fa := &fakeAssistant{enabled: true}
		svc, _ := newAssistantService(fa, sayOK)
		_, err := svc.Create(context.Background(), "bob", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).To(MatchError(studio.ErrAssistantForbidden), "err = %v, want ErrAssistantForbidden", err)
		recs, _ := svc.List(context.Background(), "bob", 10)
		Expect(recs).To(BeEmpty(), "a refused chat was stored: %v", recs)
	})

	It("an assistant chat is refused when the setting is off", func() {
		fa := &fakeAssistant{enabled: false}
		svc, _ := newAssistantService(fa, sayOK)
		_, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).To(MatchError(studio.ErrAssistantOff), "err = %v, want ErrAssistantOff", err)
		// Without a host at all it is off too.
		svc2, _ := newToolService(sayOK, func(d *studio.Deps) { d.Users = assistantUsers })
		_, err = svc2.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).To(MatchError(studio.ErrAssistantOff), "err = %v, want ErrAssistantOff without a host", err)
	})

	It("the assistant tools go when the setting is switched off later", func() {
		fa := &fakeAssistant{enabled: true}
		svc, h := newAssistantService(fa, sayOK)
		rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).NotTo(HaveOccurred())
		fa.set(false)
		sent(svc, "root", rec.ID, "hi")
		idle(svc, "root", rec.ID)
		Expect(h.ToolNames(rec.ID)).NotTo(ContainElement("install_model"), "a switched-off assistant still gave tools %v prompt %q", h.ToolNames(rec.ID), h.TurnPrompt(rec.ID))
		Expect(h.TurnPrompt(rec.ID)).NotTo(ContainSubstring("manage this LocalAI"), "a switched-off assistant still gave tools %v prompt %q", h.ToolNames(rec.ID), h.TurnPrompt(rec.ID))
	})

	It("assistant tools are not handed to sub agents", func() {
		fa := &fakeAssistant{enabled: true}
		svc, h := newAssistantService(fa, func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "seen"}}
		})
		rec, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, "root", rec.ID, "go")
		idle(svc, "root", rec.ID)
		for _, k := range childrenOf(svc, "root", rec.ID) {
			Expect(h.ToolNames(k.ID)).NotTo(ContainElement("install_model"), "a sub-agent got the assistant tools: %v", h.ToolNames(k.ID))
		}
	})

	It("an assistant chat cannot name an agent", func() {
		fa := &fakeAssistant{enabled: true}
		svc, _ := newAssistantService(fa, sayOK)
		_, err := svc.Create(context.Background(), "root", studio.CreateInput{Model: "m", Assistant: true, AgentID: "a1"})
		Expect(err).To(MatchError(studio.ErrAssistantAgent), "err = %v, want ErrAssistantAgent", err)
	})
})
