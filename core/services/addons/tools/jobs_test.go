// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// bindingFake records the chat binding each act carried.
type bindingFake struct {
	*actFake
	bindings []hostops.Binding
}

func (f *bindingFake) Act(ctx context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	b, _ := hostops.BindingOf(ctx)
	f.mu.Lock()
	f.bindings = append(f.bindings, b)
	f.mu.Unlock()
	return f.actFake.Act(ctx, a, r)
}

func jobsApp() apps.App {
	a := sandboxApp()
	a.Tools = append(a.Tools, "job")
	a.JobsTool = "job"
	a.ToolDefinitions = append(a.ToolDefinitions, contract.ToolDefinition{Name: "job", Kind: contract.ToolKindAction, UIOnly: true, Description: "Jobs.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"verb":{"type":"string"},"id":{"type":"string"},"command":{"type":"string"},"label":{"type":"string"},"since":{"type":"integer"},"maxBytes":{"type":"integer"},"observe":{"type":"boolean"},"input":{"type":"string"},"closeInput":{"type":"boolean"},"signal":{"type":"string"}},"required":["verb"],"additionalProperties":false}`)})
	return a
}

var _ = Describe("the jobs tool of an add-on", func() {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	running := contract.JobInfo{ID: "job-abcdef", State: "running", Exit: -1, Started: now, LastOutput: now, Total: 5}
	payload := func(r contract.JobReply) json.RawMessage { b, _ := json.Marshal(r); return b }
	build := func(app apps.App, r contract.JobReply) (*bindingFake, *tools.ToolAdapter) {
		f := &bindingFake{actFake: &actFake{fake: &fake{list: apps.List{Apps: []apps.App{app}}}, result: contract.Result{Payload: payload(r)}}}
		return f, tools.NewChatToolAdapter(f)
	}

	It("calls the page-only jobs tool for the chat, bound to it, with the host's execution call", func() {
		f, chats := build(jobsApp(), contract.JobReply{OK: true, Job: &running})
		cctx := contract.WithExecutionCall(ctx, contract.ExecutionCall{Profile: "base"})
		r, err := chats.ExecuteJobs(cctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobStart, Command: "make", Label: "build"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Job.ID).To(Equal("job-abcdef"))
		Expect(f.acts).To(HaveLen(1))
		req := f.acts[0].Request
		Expect(req.Action).To(Equal("act"))
		Expect(req.ResourceID).To(Equal(toolargs.ActionResourceID("conv1")))
		var body map[string]any
		Expect(json.Unmarshal(req.Arguments, &body)).To(Succeed())
		Expect(body).To(HaveKeyWithValue("tool", "job"))
		Expect(body).To(HaveKeyWithValue("verb", "start"))
		Expect(body).To(HaveKey(contract.ExecutionCallArgKey))
		Expect(f.bindings).To(ConsistOf(hostops.Binding{Principal: "bob", ChatID: "conv1"}))
	})

	It("is never offered to a model, and a model cannot call it by name", func() {
		_, chats := build(jobsApp(), contract.JobReply{OK: true, Job: &running})
		defs, err := chats.Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.Name).NotTo(Equal(tools.ToolName("sandbox", "job")))
		}
		_, err = chats.ExecuteChat(ctx, member, tools.NewChatCall("bob", "conv1"), tools.ToolName("sandbox", "job"), args(`{"verb":"list"}`))
		Expect(err).To(MatchError(tools.ErrUnknownTool))
	})

	It("says an add-on without a jobs tool cannot run jobs, and sends nothing", func() {
		f, chats := build(sandboxApp(), contract.JobReply{OK: true})
		_, err := chats.ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobList})
		Expect(err).To(MatchError(tools.ErrNoJobs))
		// A jobs tool a model could see is not a jobs tool either.
		app := jobsApp()
		app.ToolDefinitions[2].UIOnly = false
		f2, chats2 := build(app, contract.JobReply{OK: true})
		_, err = chats2.ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobList})
		Expect(err).To(MatchError(tools.ErrNoJobs))
		Expect(f.acts).To(BeEmpty())
		Expect(f2.acts).To(BeEmpty())
	})

	It("refuses invalid arguments before it sends anything, and a plain adapter never calls it", func() {
		f, chats := build(jobsApp(), contract.JobReply{OK: true})
		_, err := chats.ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobRead, ID: "../etc"})
		Expect(err).To(MatchError(tools.ErrInvalidArguments))
		_, err = chats.ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobInput, ID: "job-abcdef", Input: strings.Repeat("a", contract.MaxJobInputBytes+1)})
		Expect(err).To(MatchError(tools.ErrInvalidArguments))
		_, err = tools.NewToolAdapter(f).ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobList})
		Expect(err).To(MatchError(tools.ErrInvalidArguments))
		Expect(f.acts).To(BeEmpty())
	})

	It("refuses an answer that is too large, malformed, or about another job", func() {
		for _, raw := range []json.RawMessage{
			json.RawMessage(`{"ok":true,"job":` + string(mustJSON(running)) + `,"output":"` + strings.Repeat("a", tools.MaxResultBytes) + `"}`),
			json.RawMessage(`{"ok":true,"extra":1}`),
			payload(contract.JobReply{OK: true, Job: &contract.JobInfo{ID: "job-zzzzzz", State: "running", Exit: -1}}),
		} {
			f := &bindingFake{actFake: &actFake{fake: &fake{list: apps.List{Apps: []apps.App{jobsApp()}}}, result: contract.Result{Payload: raw}}}
			_, err := tools.NewChatToolAdapter(f).ExecuteJobs(ctx, member, tools.NewChatCall("bob", "conv1"), "sandbox", contract.JobArgs{Verb: contract.JobRead, ID: "job-abcdef"})
			Expect(err).To(HaveOccurred())
		}
	})
})

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
