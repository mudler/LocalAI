package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Mistral's chat completions API (docs.mistral.ai, AssistantMessage and
// DeltaMessage in its OpenAPI spec) types message.content and
// delta.content as string | null | ContentChunk[]. Reasoning models answer
// with a ThinkChunk ({"type":"thinking","thinking":[TextChunk...]}) followed
// by a TextChunk, and their stream sends chunk arrays first and plain strings
// once the answer starts.
var _ = Describe("OpenAI translate mode with array content", func() {
	var (
		srv *httptest.Server
		cp  *CloudProxy
	)

	serve := func(contentType, body string) {
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", contentType)
			_, _ = io.WriteString(w, body)
		}))
		DeferCleanup(srv.Close)
		GinkgoT().Setenv("CLOUD_PROXY_MISTRAL_FAKE", "fake-key")
		cp = NewCloudProxy()
		Expect(cp.Load(&pb.ModelOptions{
			Model: "mistral-local",
			Proxy: &pb.ProxyOptions{
				UpstreamUrl:   srv.URL,
				Mode:          modeTranslate,
				Provider:      providerOpenAI,
				ApiKeyEnv:     "CLOUD_PROXY_MISTRAL_FAKE",
				UpstreamModel: "mistral-large-latest",
			},
		})).To(Succeed())
	}

	stream := func() []*pb.Reply {
		results := make(chan *pb.Reply, 32)
		done := make(chan error, 1)
		go func() {
			done <- cp.PredictStreamRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "Hallo"}},
			}, results)
			close(results)
		}()
		var replies []*pb.Reply
		for r := range results {
			replies = append(replies, r)
		}
		Expect(<-done).To(Succeed())
		return replies
	}

	sse := func(frames ...string) string {
		var b strings.Builder
		for _, f := range frames {
			b.WriteString("data: " + f + "\n\n")
		}
		b.WriteString("data: [DONE]\n\n")
		return b.String()
	}

	Context("non-streaming", func() {
		It("puts text chunks in the answer and thinking chunks in the reasoning", func() {
			serve("application/json", `{
				"id":"m1",
				"choices":[{"index":0,"finish_reason":"stop","message":{
					"role":"assistant",
					"content":[
						{"type":"thinking","thinking":[{"type":"text","text":"The user greets. "},{"type":"text","text":"Greet back."}]},
						{"type":"text","text":"Hallo! "},
						{"type":"text","text":"Wie kann ich helfen?"}
					]
				}}],
				"usage":{"prompt_tokens":7,"completion_tokens":12,"total_tokens":19}
			}`)

			reply, err := cp.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "Hallo"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(Equal("Hallo! Wie kann ich helfen?"))
			Expect(reply.GetChatDeltas()).To(HaveLen(1))
			Expect(reply.GetChatDeltas()[0].GetContent()).To(Equal("Hallo! Wie kann ich helfen?"))
			Expect(reply.GetChatDeltas()[0].GetReasoningContent()).To(Equal("The user greets. Greet back."))
			Expect(reply.GetPromptTokens()).To(Equal(int32(7)))
			Expect(reply.GetTokens()).To(Equal(int32(12)))
		})

		It("keeps tool calls next to array content", func() {
			serve("application/json", `{
				"id":"m2",
				"choices":[{"index":0,"finish_reason":"tool_calls","message":{
					"role":"assistant",
					"content":[{"type":"thinking","thinking":[{"type":"text","text":"Need the weather."}]}],
					"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Berlin\"}"}}]
				}}]
			}`)

			reply, err := cp.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "weather?"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(BeEmpty())
			Expect(reply.GetChatDeltas()).To(HaveLen(1))
			Expect(reply.GetChatDeltas()[0].GetReasoningContent()).To(Equal("Need the weather."))
			Expect(reply.GetChatDeltas()[0].GetToolCalls()).To(HaveLen(1))
			Expect(reply.GetChatDeltas()[0].GetToolCalls()[0].GetName()).To(Equal("get_weather"))
		})

		It("ignores part types it cannot carry and accepts null content", func() {
			serve("application/json", `{
				"id":"m3",
				"choices":[{"index":0,"finish_reason":"stop","message":{
					"role":"assistant",
					"content":[{"type":"reference","reference_ids":[1]},{"type":"text","text":"ok"}]
				}}]
			}`)
			reply, err := cp.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "x"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(Equal("ok"))
			Expect(reply.GetChatDeltas()).To(BeEmpty())

			var c openAIContent
			Expect(json.Unmarshal([]byte(`null`), &c)).To(Succeed())
			Expect(c).To(Equal(openAIContent{}))
		})

		// Counter-check: the plain OpenAI shape behaves exactly as before,
		// with no ChatDelta added for a text-only answer.
		It("leaves plain string content unchanged", func() {
			serve("application/json", `{"id":"o1","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi there"}}]}`)
			reply, err := cp.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "hello"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(reply.GetMessage())).To(Equal("hi there"))
			Expect(reply.GetChatDeltas()).To(BeEmpty())
		})

		It("still fails on content that is neither a string nor an array", func() {
			serve("application/json", `{"id":"o2","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":42}}]}`)
			_, err := cp.PredictRich(&pb.PredictOptions{
				Messages: []*pb.Message{{Role: "user", Content: "hello"}},
			})
			Expect(err).To(MatchError(ContainSubstring("decode response")))
		})
	})

	Context("streaming", func() {
		It("streams thinking chunks as reasoning and text as content across both shapes", func() {
			serve("text/event-stream", sse(
				`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
				`{"choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"Greeting, "}]}]}}]}`,
				`{"choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"answer briefly."}]},{"type":"text","text":"Hallo"}]}}]}`,
				`{"choices":[{"index":0,"delta":{"content":"! Wie geht's?"},"finish_reason":"stop"}]}`,
			))

			var message, content, reasoning strings.Builder
			for _, r := range stream() {
				message.Write(r.GetMessage())
				for _, d := range r.GetChatDeltas() {
					content.WriteString(d.GetContent())
					reasoning.WriteString(d.GetReasoningContent())
				}
			}
			Expect(message.String()).To(Equal("Hallo! Wie geht's?"))
			Expect(content.String()).To(Equal("Hallo! Wie geht's?"))
			Expect(reasoning.String()).To(Equal("Greeting, answer briefly."))
		})

		It("leaves plain string deltas unchanged", func() {
			serve("text/event-stream", sse(
				`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`,
				`{"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
			))

			replies := stream()
			Expect(replies).To(HaveLen(2))
			for _, r := range replies {
				Expect(r.GetChatDeltas()).To(HaveLen(1))
				Expect(r.GetChatDeltas()[0].GetReasoningContent()).To(BeEmpty())
			}
			Expect(string(replies[0].GetMessage()) + string(replies[1].GetMessage())).To(Equal("hello world"))
		})
	})
})
