package studio

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"
)

// Each chat's harness gets the context window of that chat's own model, from
// the host, so a small model compacts early and a large one does not.
var _ = Describe("context internal", func() {
	It("each chat gets the context size of its model", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		sizes := map[string]int{"small": 4096, "large": 131072}
		svc, err := New(Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
			ContextTokens: func(model string) int { return sizes[model] },
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })

		for model, want := range sizes {
			rec, err := svc.Create(context.Background(), "alice", CreateInput{Model: model})
			Expect(err).NotTo(HaveOccurred())
			m, err := svc.llmFor(workspace.Actor{Principal: "alice", ChatID: rec.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Name).To(Equal(model), "model %q", model)
			Expect(m.ContextTokens).To(Equal(want), "model %q", model)
			Expect(m.LLM).NotTo(BeNil(), "model %q", model)
		}
	})

	It("a chat's model requests carry the images of the last user message only", func() {
		var body string
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		}))
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: p, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		rec, err := svc.Create(context.Background(), "alice", CreateInput{Model: "cloud-vl"})
		Expect(err).NotTo(HaveOccurred())
		m, err := svc.llmFor(workspace.Actor{Principal: "alice", ChatID: rec.ID})
		Expect(err).NotTo(HaveOccurred())

		image := func(uri string) openai.ChatCompletionMessage {
			return openai.ChatCompletionMessage{Role: "user", MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: uri}},
			}}
		}
		_, _, err = m.LLM.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{
			image("data:image/png;base64,T0xE"), {Role: "assistant", Content: "a cat"}, image("data:image/png;base64,TkVX"),
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(body).NotTo(ContainSubstring("T0xE"), "the image of an earlier message was sent again")
		Expect(body).To(ContainSubstring("TkVX"))
		Expect(strings.Count(body, `"type":"image_url"`)).To(Equal(1), body)
	})
})
