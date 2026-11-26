package provider_test

import (
	"context"
	"io"
	"net/http"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

var _ = Describe("LLMWith and the privacy level", func() {
	levelSeen := func(level privacy.Level) privacy.Level {
		var seen privacy.Level
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = privacy.FromContext(r.Context())
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}))
		llm := p.LLMWith(nil, "m", provider.Params{Privacy: level})
		stream, err := llm.(cogito.StreamingLLM).CreateChatCompletionStream(context.Background(), userRequest("hello"))
		Expect(err).ToNot(HaveOccurred())
		for range stream {
		}
		return seen
	}

	It("carries the chat's level into the chat route", func() {
		Expect(levelSeen(privacy.ZeroRetention)).To(Equal(privacy.ZeroRetention))
		Expect(levelSeen(privacy.MemoryOff)).To(Equal(privacy.MemoryOff))
	})

	It("leaves a normal chat's requests normal", func() {
		Expect(levelSeen(privacy.Normal)).To(Equal(privacy.Normal))
	})
})
