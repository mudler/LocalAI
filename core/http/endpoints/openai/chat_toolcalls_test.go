package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveNonStreamToolCalls", func() {
	declared := functions.Functions{{
		Name: "bash",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"script": map[string]any{"type": "string"}},
			"required":   []any{"script"},
		},
	}}
	const badCall = `{"name": "bash", "arguments": {"command": "ls"}}`
	const goodCall = `{"name": "bash", "arguments": {"script": "ls"}}`

	Context("Go-side text parsing (no chat deltas)", func() {
		It("drops a call that does not fit, leaving the text as the answer", func() {
			got := resolveNonStreamToolCalls(nil, badCall, "", &config.ModelConfig{}, declared, "answer")
			Expect(got.calls).To(BeEmpty())
			Expect(got.content).To(BeEmpty(), "empty content makes the handler answer with raw")
			Expect(got.raw).To(ContainSubstring(`"command"`))
		})

		It("keeps a call that fits", func() {
			got := resolveNonStreamToolCalls(nil, goodCall, "", &config.ModelConfig{}, declared, "answer")
			Expect(got.calls).To(HaveLen(1))
			Expect(got.calls[0].Name).To(Equal("bash"))
		})

		It("keeps every call with disable_tool_call_validation", func() {
			cfg := &config.ModelConfig{}
			cfg.FunctionsConfig.DisableToolCallValidation = true
			Expect(resolveNonStreamToolCalls(nil, badCall, "", cfg, declared, "answer").calls).To(HaveLen(1))
		})

		It("does not check calls produced under LocalAI's grammar", func() {
			cfg := &config.ModelConfig{}
			cfg.Grammar = "root ::= ..."
			Expect(resolveNonStreamToolCalls(nil, badCall, "", cfg, declared, "answer").calls).To(HaveLen(1))
		})
	})

	Context("content-based fallback (autoparser returned content, no calls)", func() {
		It("drops a call parsed from the raw text that does not fit", func() {
			deltas := []*pb.ChatDelta{{Content: "let me run it"}}
			got := resolveNonStreamToolCalls(deltas, badCall, "", &config.ModelConfig{}, declared, "answer")
			Expect(got.calls).To(BeEmpty())
			Expect(got.content).To(BeEmpty())
		})
	})

	Context("C++ autoparser (ChatDeltas)", func() {
		// The autoparser checks the tool name but parses the arguments as any
		// JSON when no grammar was built, so its calls are checked too.
		autoparserCall := func(args string) []*pb.ChatDelta {
			return []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Name: "bash", Arguments: args}}}}
		}

		It("drops a call with an unknown argument and answers with it as text", func() {
			got := resolveNonStreamToolCalls(autoparserCall(`{"command":"ls"}`), "", "", &config.ModelConfig{}, declared, "answer")
			Expect(got.calls).To(BeEmpty())
			Expect(got.content).To(Equal(`{"name":"bash","arguments":{"command":"ls"}}`))
		})

		It("keeps a call that fits", func() {
			got := resolveNonStreamToolCalls(autoparserCall(`{"script":"ls"}`), "", "", &config.ModelConfig{}, declared, "answer")
			Expect(got.calls).To(HaveLen(1))
		})

		It("keeps every call with disable_tool_call_validation", func() {
			cfg := &config.ModelConfig{}
			cfg.FunctionsConfig.DisableToolCallValidation = true
			Expect(resolveNonStreamToolCalls(autoparserCall(`{"command":"ls"}`), "", "", cfg, declared, "answer").calls).To(HaveLen(1))
		})
	})
})
