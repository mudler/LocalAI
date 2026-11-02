// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

var _ = Describe("text-only memory summary model", func() {
	DescribeTable("never consumes answering media, including on failure",
		func(outcome string) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st, err := store.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "owner", Model: "resolved-model"})).To(Succeed())
			p := provider.New()
			current := &auth.User{ID: "owner", Email: "current@example.com", Status: auth.StatusActive}
			lookups := 0
			s := &Service{d: Deps{Store: st, Provider: p, Users: func(ctx context.Context, id string) (*auth.User, error) {
				Expect(ctx.Err()).NotTo(HaveOccurred())
				Expect(id).To(Equal("owner"))
				lookups++
				return current, nil
			}}, bus: NewLocalBus(), videos: map[string][]string{}}
			video := "data:video/mp4;base64,cGVuZGluZw=="
			rest := s.keepVideos(key("owner", "chat"), []session.Media{
				{Kind: session.MediaVideo, DataURI: video},
				{Kind: session.MediaImage, DataURI: "data:image/png;base64,aW1hZ2U="},
			})
			Expect(rest).To(HaveLen(1))
			var requests []map[string]any
			p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				user, ok := auth.InternalUser(req.Context())
				Expect(ok).To(BeTrue())
				Expect(user).To(BeIdenticalTo(current))
				var body map[string]any
				Expect(json.NewDecoder(req.Body).Decode(&body)).To(Succeed())
				requests = append(requests, body)
				if len(requests) == 1 && outcome == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if len(requests) == 1 && outcome == "cancelled" {
					cancel()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"summary only"},"finish_reason":"stop"}]}`))
			}))
			text, err := s.SummarizeMemory(ctx, SummaryRequest{Principal: "owner", ChatID: "chat", Content: "text only"})
			if outcome == "success" {
				Expect(err).NotTo(HaveOccurred())
				Expect(text).To(Equal("summary only"))
			} else {
				Expect(err).To(HaveOccurred())
				Expect(text).To(BeEmpty())
			}
			// takeVideos deletes this entry even if the upstream rejects the request.
			// Keeping it proves the summary never invoked the consuming callback.
			Expect(s.videos[key("owner", "chat")]).To(Equal([]string{video}))
			Expect(lookups).To(Equal(1))
			Expect(requests).To(HaveLen(1))
			body := requests[0]
			Expect(body["model"]).To(Equal("resolved-model"))
			Expect(body["tools"]).To(BeNil())
			Expect(body["functions"]).To(BeNil())
			messages := body["messages"].([]any)
			Expect(messages).To(HaveLen(2))
			Expect(messages[1]).To(Equal(map[string]any{"role": "user", "content": "text only"}))
			for _, message := range messages {
				Expect(message.(map[string]any)["content"]).To(BeAssignableToTypeOf(""))
			}
			raw, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			for _, field := range []string{"video_url", "image_url", "input_audio", "data:video/", "data:image/", "data:audio/"} {
				Expect(string(raw)).NotTo(ContainSubstring(field))
			}
			// The normal answering client must still attach the same pending video.
			model, err := s.llmForContext(context.Background(), userChatActor("owner", "chat"))
			Expect(err).NotTo(HaveOccurred())
			_, _, err = model.LLM.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model.Name, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "answer"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.videos).To(BeEmpty())
			raw, err = json.Marshal(requests[1])
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).To(ContainSubstring(video))
		},
		Entry("success", "success"),
		Entry("provider failure", "forbidden"),
		Entry("context cancellation", "cancelled"),
	)
})
