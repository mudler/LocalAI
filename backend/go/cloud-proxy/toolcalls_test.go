package main

import (
	"strings"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/gomega"
)

// OpenAI: non-streaming tool call response. Verify the response is
// mapped to Reply.ChatDeltas[].ToolCalls with id/name/arguments intact,
// and usage tokens land on Reply.PromptTokens / Reply.Tokens.
func TestPredictRich_OpenAI_ToolCalls(t *testing.T) {
	srv, _ := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, `{
			"id":"resp-1",
			"choices":[{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"",
					"tool_calls":[
						{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"SF\"}"}},
						{"id":"call_def","type":"function","function":{"name":"get_time","arguments":"{\"tz\":\"PT\"}"}}
					]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":42,"completion_tokens":18,"total_tokens":60}
		}`, "application/json"
	})
	defer srv.Close()
	g := NewWithT(t)
	cp := newTranslateCloudProxy(t, srv.URL)

	reply, err := cp.PredictRich(&pb.PredictOptions{
		Messages: []*pb.Message{{Role: "user", Content: "what's the weather?"}},
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(reply.GetMessage())).To(Equal(""))
	g.Expect(reply.GetPromptTokens()).To(Equal(int32(42)))
	g.Expect(reply.GetTokens()).To(Equal(int32(18)))
	g.Expect(reply.GetChatDeltas()).To(HaveLen(1))
	tcs := reply.GetChatDeltas()[0].GetToolCalls()
	g.Expect(tcs).To(HaveLen(2))
	g.Expect(tcs[0].GetId()).To(Equal("call_abc"))
	g.Expect(tcs[0].GetName()).To(Equal("get_weather"))
	g.Expect(tcs[0].GetArguments()).To(ContainSubstring(`"location":"SF"`))
	g.Expect(tcs[1].GetId()).To(Equal("call_def"))
	g.Expect(tcs[1].GetName()).To(Equal("get_time"))
}

// OpenAI: streaming tool call. Arguments arrive as a sequence of
// delta chunks; the consumer is expected to concatenate by tool index.
// Verify each chunk reaches the channel and the assembled arguments
// match the input.
func TestPredictStreamRich_OpenAI_ToolCallDeltas(t *testing.T) {
	chunks := []string{
		// Frame 0: announce the tool call (id + name, no args yet).
		`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_xyz","type":"function","function":{"name":"search"}}]}}]}`,
		// Frames 1-3: arguments arrive in fragments.
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"clo"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"uds\"}"}}]}}]}`,
		// Stop frame.
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	body := ""
	for _, c := range chunks {
		body += "data: " + c + "\n\n"
	}
	body += "data: [DONE]\n\n"

	srv, _ := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, body, "text/event-stream"
	})
	defer srv.Close()
	g := NewWithT(t)
	cp := newTranslateCloudProxy(t, srv.URL)

	results := make(chan *pb.Reply, 16)
	done := make(chan error, 1)
	go func() {
		done <- cp.PredictStreamRich(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "find something"}},
		}, results)
		close(results)
	}()

	var (
		toolName  string
		toolID    string
		toolIndex int32 = -1
		argsBuf   strings.Builder
	)
	for reply := range results {
		for _, cd := range reply.GetChatDeltas() {
			for _, tc := range cd.GetToolCalls() {
				if tc.GetName() != "" {
					toolName = tc.GetName()
				}
				if tc.GetId() != "" {
					toolID = tc.GetId()
				}
				if toolIndex == -1 {
					toolIndex = tc.GetIndex()
				}
				argsBuf.WriteString(tc.GetArguments())
			}
		}
	}
	err := <-done
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(toolID).To(Equal("call_xyz"))
	g.Expect(toolName).To(Equal("search"))
	g.Expect(toolIndex).To(Equal(int32(0)))
	g.Expect(argsBuf.String()).To(Equal(`{"q":"clouds"}`))
}

// Anthropic: non-streaming tool_use block. The block appears in
// Content[] alongside text blocks; the input field is a structured
// JSON object. Map to ToolCallDelta with arguments as serialised JSON
// so downstream OpenAI-shaped consumers see a familiar format.
func TestPredictRich_Anthropic_ToolUse(t *testing.T) {
	srv, _ := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, `{
			"id":"msg_1","type":"message","role":"assistant",
			"content":[
				{"type":"text","text":"Let me check that."},
				{"type":"tool_use","id":"toolu_01","name":"weather","input":{"location":"SF"}}
			],
			"model":"claude","usage":{"input_tokens":12,"output_tokens":34}
		}`, "application/json"
	})
	defer srv.Close()
	g := NewWithT(t)
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	reply, err := cp.PredictRich(&pb.PredictOptions{
		Messages: []*pb.Message{{Role: "user", Content: "what's the weather?"}},
		Tokens:   64,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(reply.GetMessage())).To(Equal("Let me check that."))
	g.Expect(reply.GetPromptTokens()).To(Equal(int32(12)))
	g.Expect(reply.GetTokens()).To(Equal(int32(34)))
	g.Expect(reply.GetChatDeltas()).To(HaveLen(1))
	g.Expect(reply.GetChatDeltas()[0].GetToolCalls()).To(HaveLen(1))
	tc := reply.GetChatDeltas()[0].GetToolCalls()[0]
	g.Expect(tc.GetId()).To(Equal("toolu_01"))
	g.Expect(tc.GetName()).To(Equal("weather"))
	g.Expect(tc.GetArguments()).To(ContainSubstring(`"location":"SF"`))
}

// Anthropic: streaming tool_use. content_block_start announces the
// tool's id + name; input_json_delta events carry argument fragments
// which the consumer accumulates. message_delta carries final usage.
func TestPredictStreamRich_Anthropic_InputJSONDelta(t *testing.T) {
	frames := []string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		// Block 0 is a tool_use; consumer should allocate a slot.
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_42\",\"name\":\"lookup\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"rain\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	body := strings.Join(frames, "")

	srv, _ := fakeAnthropicUpstream(t, func(_ anthropicRequest) (int, string, string) {
		return 200, body, "text/event-stream"
	})
	defer srv.Close()
	g := NewWithT(t)
	cp := newAnthropicTranslateCloudProxy(t, srv.URL)

	results := make(chan *pb.Reply, 16)
	done := make(chan error, 1)
	go func() {
		done <- cp.PredictStreamRich(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "rain?"}},
			Tokens:   64,
		}, results)
		close(results)
	}()

	var (
		toolID, toolName string
		argsBuf          strings.Builder
		finalTokens      int32
	)
	for reply := range results {
		if reply.GetTokens() > 0 && len(reply.GetChatDeltas()) == 0 {
			finalTokens = reply.GetTokens()
			continue
		}
		for _, cd := range reply.GetChatDeltas() {
			for _, tc := range cd.GetToolCalls() {
				if tc.GetId() != "" {
					toolID = tc.GetId()
				}
				if tc.GetName() != "" {
					toolName = tc.GetName()
				}
				argsBuf.WriteString(tc.GetArguments())
			}
		}
	}
	err := <-done
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(toolID).To(Equal("toolu_42"))
	g.Expect(toolName).To(Equal("lookup"))
	g.Expect(argsBuf.String()).To(Equal(`{"q":"rain"}`))
	g.Expect(finalTokens).To(Equal(int32(7)))
}

// Sanity: the legacy Predict() (string, error) signature still works
// — it delegates to PredictRich and extracts Message.
func TestPredict_LegacyWrapper_OpenAI(t *testing.T) {
	srv, _ := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`, "application/json"
	})
	defer srv.Close()
	g := NewWithT(t)
	cp := newTranslateCloudProxy(t, srv.URL)

	got, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "hi"}}})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(got).To(Equal("hello"))
}
