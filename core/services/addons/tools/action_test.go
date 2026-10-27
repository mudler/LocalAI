// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// actFake adds the owner side of action tools to the fake owner.
type actFake struct {
	*fake
	acts   []call
	result contract.Result
	err    error
}

func (f *actFake) Act(_ context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acts = append(f.acts, call{a, r})
	return f.result, f.err
}

func sandboxApp() apps.App {
	return apps.App{ID: "sandbox", Name: "sandbox", Generation: 2, Enabled: true, State: "running", Tools: []string{"bash", "status"},
		ToolDefinitions: []contract.ToolDefinition{
			{Name: "bash", Kind: contract.ToolKindAction, Description: "Run a script.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)},
			{Name: "status", Kind: contract.ToolKindRead, Description: "Say whether the sandbox works.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		}}
}

var _ = Describe("action tools in the adapter", func() {
	ctx := context.Background()
	build := func() (*actFake, *tools.ToolAdapter, *tools.ToolAdapter) {
		f := &actFake{fake: &fake{list: apps.List{Apps: []apps.App{sandboxApp()}}}, result: contract.Result{Payload: json.RawMessage(`{"stdout":"hi\n","exit":0}`)}}
		return f, tools.NewToolAdapter(f), tools.NewChatToolAdapter(f)
	}

	It("offers an action tool to the chats adapter and to nobody else", func() {
		_, plain, chats := build()
		have := func(a *tools.ToolAdapter) []string {
			defs, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			var names []string
			for _, d := range defs {
				names = append(names, d.Name)
			}
			return names
		}
		Expect(have(plain)).To(ConsistOf(tools.ToolName("sandbox", "status")))
		Expect(have(chats)).To(ConsistOf(tools.ToolName("sandbox", "status"), tools.ToolName("sandbox", "bash")))
	})

	It("runs an action tool for the chats adapter as the actor, with the act action, and returns its payload", func() {
		f, _, chats := build()
		res, err := chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"echo hi"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultData))
		Expect(res.JSON()).To(ContainSubstring("hi"))
		Expect(f.acts).To(HaveLen(1))
		Expect(f.acts[0].Actor).To(Equal(member))
		Expect(f.acts[0].Request.Action).To(Equal("act"))
		Expect(f.proposed).To(BeEmpty(), "an action is never a proposal")
		Expect(f.dispatched).To(BeEmpty())
		var body map[string]any
		Expect(json.Unmarshal(f.acts[0].Request.Arguments, &body)).To(Succeed())
		Expect(body).To(HaveKeyWithValue("tool", "bash"))
	})

	It("tells an action tool which conversation it runs in, from the chat and never from the model", func() {
		f, _, chats := build()
		_, err := chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"echo hi"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.acts[0].Request.ResourceID).To(Equal("conv1"))
		// The arguments cannot name it: the schema has no such property.
		_, err = chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"x","resourceId":"conv2"}`))
		Expect(f.acts).To(HaveLen(1), "the second call must not reach the owner")
		Expect(err).To(HaveOccurred())
		// A call with no conversation has none to give.
		_, err = chats.ExecuteIn(ctx, member, "", tools.ToolName("sandbox", "bash"), args(`{"command":"echo hi"}`))
		Expect(err).To(HaveOccurred())
		Expect(f.acts).To(HaveLen(1))
	})

	It("does not run an action tool for any other adapter, and says the tool is unknown", func() {
		f, plain, _ := build()
		_, err := plain.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"echo hi"}`))
		Expect(err).To(MatchError(tools.ErrUnknownTool))
		Expect(f.acts).To(BeEmpty())
		Expect(f.calls()).To(BeZero())
	})

	It("is unavailable when the owner cannot act, and never falls back to a read or a proposal", func() {
		f := &fake{list: apps.List{Apps: []apps.App{sandboxApp()}}}
		chats := tools.NewChatToolAdapter(f)
		_, err := chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"x"}`))
		Expect(err).To(HaveOccurred())
		Expect(f.calls()).To(BeZero())
	})

	It("keeps the argument rules: an identity argument is refused before anything runs", func() {
		f, _, chats := build()
		_, err := chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "bash"), args(`{"command":"x","userId":"root"}`))
		Expect(err).To(HaveOccurred())
		Expect(f.acts).To(BeEmpty())
	})
})
