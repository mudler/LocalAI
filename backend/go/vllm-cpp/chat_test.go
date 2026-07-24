package main

import (
	"encoding/json"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("useChatPath", func() {
	It("requires tokenizer templating AND structured messages", func() {
		Expect(useChatPath(&pb.PredictOptions{})).To(BeFalse())
		Expect(useChatPath(&pb.PredictOptions{UseTokenizerTemplate: true})).To(BeFalse())
		Expect(useChatPath(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "hi"}},
		})).To(BeFalse())
		Expect(useChatPath(&pb.PredictOptions{
			UseTokenizerTemplate: true,
			Messages:             []*pb.Message{{Role: "user", Content: "hi"}},
		})).To(BeTrue())
	})
})

var _ = Describe("chatRequestJSON", func() {
	It("lowers messages, tools, tool_choice and sampling onto one request", func() {
		out, err := chatRequestJSON(&pb.PredictOptions{
			UseTokenizerTemplate: true,
			Messages: []*pb.Message{
				{Role: "system", Content: "be brief"},
				{Role: "user", Content: "weather in Rome?"},
			},
			Tools:       `[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]`,
			ToolChoice:  "required",
			Temperature: 0.2,
			TopP:        0.9,
			Tokens:      64,
			StopPrompts: []string{"<|im_end|>"},
		}, false)
		Expect(err).NotTo(HaveOccurred())

		var req map[string]any
		Expect(json.Unmarshal([]byte(out), &req)).To(Succeed())
		Expect(req["messages"]).To(HaveLen(2))
		Expect(req["tools"]).To(HaveLen(1))
		Expect(req["tool_choice"]).To(Equal("required"))
		Expect(req["max_tokens"]).To(BeNumerically("==", 64))
		Expect(req["top_p"]).To(BeNumerically("~", 0.9, 1e-6))
		Expect(req["stop"]).To(ConsistOf("<|im_end|>"))
		Expect(req).NotTo(HaveKey("stream_options"))
	})

	It("parses a named-function tool_choice object and asks for stream usage", func() {
		out, err := chatRequestJSON(&pb.PredictOptions{
			Messages:   []*pb.Message{{Role: "user", Content: "hi"}},
			ToolChoice: `{"type":"function","function":{"name":"get_weather"}}`,
		}, true)
		Expect(err).NotTo(HaveOccurred())

		var req map[string]any
		Expect(json.Unmarshal([]byte(out), &req)).To(Succeed())
		choice, ok := req["tool_choice"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(choice["type"]).To(Equal("function"))
		Expect(req["stream_options"]).To(HaveKeyWithValue("include_usage", true))
	})

	It("rejects malformed tools JSON", func() {
		_, err := chatRequestJSON(&pb.PredictOptions{
			Messages: []*pb.Message{{Role: "user", Content: "hi"}},
			Tools:    "{not json",
		}, false)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("chatChunk.toReply", func() {
	It("maps a streaming tool-call delta onto ChatDelta/ToolCallDelta", func() {
		var chunk chatChunk
		payload := `{"object":"chat.completion.chunk","choices":[{"delta":{
			"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"city\":"}}]
		},"finish_reason":null}]}`
		Expect(json.Unmarshal([]byte(payload), &chunk)).To(Succeed())

		reply := chunk.toReply()
		Expect(reply.ChatDeltas).To(HaveLen(1))
		Expect(reply.ChatDeltas[0].ToolCalls).To(HaveLen(1))
		tc := reply.ChatDeltas[0].ToolCalls[0]
		Expect(tc.Name).To(Equal("get_weather"))
		Expect(tc.Id).To(Equal("call_1"))
		Expect(tc.Arguments).To(Equal(`{"city":`))
	})

	It("maps a non-stream response message and usage", func() {
		var chunk chatChunk
		payload := `{"object":"chat.completion","choices":[{"message":{
			"role":"assistant","content":"Sunny."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":3}}`
		Expect(json.Unmarshal([]byte(payload), &chunk)).To(Succeed())

		reply := chunk.toReply()
		Expect(string(reply.Message)).To(Equal("Sunny."))
		Expect(reply.ChatDeltas).To(HaveLen(1))
		Expect(reply.ChatDeltas[0].Content).To(Equal("Sunny."))
		Expect(reply.PromptTokens).To(BeNumerically("==", 12))
		Expect(reply.Tokens).To(BeNumerically("==", 3))
	})

	It("emits no ChatDelta for an empty role-only chunk", func() {
		var chunk chatChunk
		payload := `{"object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","content":""}}]}`
		Expect(json.Unmarshal([]byte(payload), &chunk)).To(Succeed())
		Expect(chunk.toReply().ChatDeltas).To(BeEmpty())
	})
})
