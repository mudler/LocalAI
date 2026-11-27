package studio_test

import (
	"context"
	"encoding/json"
	"sync"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// pluginsAssistant is an assistant tool set with the plugins tool only.
type pluginsAssistant struct {
	mu    sync.Mutex
	calls []string
	users []string
}

func (f *pluginsAssistant) Enabled() bool                      { return true }
func (f *pluginsAssistant) SystemPrompt() string               { return "You manage this LocalAI instance." }
func (f *pluginsAssistant) Executor() mcpendpoint.ToolExecutor { return f }
func (f *pluginsAssistant) DiscoverTools(context.Context) ([]functions.Function, error) {
	return []functions.Function{{Name: localaitools.ToolPlugins, Description: "plugins", Parameters: map[string]any{"type": "object"}}}, nil
}
func (f *pluginsAssistant) IsTool(n string) bool { return n == localaitools.ToolPlugins }
func (f *pluginsAssistant) HasTools() bool       { return true }
func (f *pluginsAssistant) ExecuteTool(ctx context.Context, name, args string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	who := ""
	if u := localaitools.UserFromContext(ctx); u != nil {
		who = u.ID
	}
	f.users = append(f.users, who)
	f.calls = append(f.calls, name+"|"+args)
	return `{"ok":true}`, nil
}

func (f *pluginsAssistant) seen() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]string(nil), f.users...)
}

var _ = Describe("the plugins tool in an assistant chat", func() {
	ctx := context.Background()

	run := func(args string) (*pluginsAssistant, *studio.Service, *studiotest.ToolHarness) {
		fa := &pluginsAssistant{}
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: localaitools.ToolPlugins, Args: args}, {Say: "done"}}
		}, func(d *studio.Deps) { d.Assistant, d.Users, d.Files = fa, assistantUsers, newFiles() })
		rec, err := svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, "root", rec.ID, "go")
		idle(svc, "root", rec.ID)
		return fa, svc, h
	}

	It("turns an install into a card the person confirms, and installs only then, as the owner", func() {
		args := `{"action":"install","plugin":"fmt@acme","scope":"instance"}`
		fa, svc, h := run(args)
		res := h.ResultsFor(localaitools.ToolPlugins)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(ContainSubstring("Nothing changed yet"))
		calls, _ := fa.seen()
		Expect(calls).To(BeEmpty(), "nothing is installed before a person confirms")

		list, err := svc.Changes().List(ctx, "root", contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Kind).To(Equal(contract.ChangePlugin))
		Expect(list[0].Summary).To(ContainSubstring("fmt@acme"))

		done, err := svc.Changes().Confirm(ctx, "root", list[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(done.Status).To(Equal(contract.ProposalApplied))
		calls, users := fa.seen()
		Expect(calls).To(HaveLen(1))
		Expect(users).To(Equal([]string{"root"}))
		var got localaitools.PluginsRequest
		Expect(json.Unmarshal([]byte(calls[0][len(localaitools.ToolPlugins)+1:]), &got)).To(Succeed())
		Expect(got).To(Equal(localaitools.PluginsRequest{Action: "install", Plugin: "fmt@acme", Scope: "instance"}))
	})

	DescribeTable("asks for a confirmation of every action that changes the server",
		func(args string) {
			fa, svc, _ := run(args)
			calls, _ := fa.seen()
			Expect(calls).To(BeEmpty())
			list, err := svc.Changes().List(ctx, "root", contract.ProposalPending)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
		},
		Entry("enable", `{"action":"enable","id":"i1"}`),
		Entry("disable", `{"action":"disable","id":"i1"}`),
		Entry("remove", `{"action":"remove","id":"i1"}`),
		Entry("add a marketplace", `{"action":"add_marketplace","source":"acme/plugins"}`),
	)

	It("runs a read action at once", func() {
		fa, svc, _ := run(`{"action":"browse","query":"fmt"}`)
		calls, _ := fa.seen()
		Expect(calls).To(HaveLen(1))
		list, err := svc.Changes().List(ctx, "root", contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})
})
