package provider_test

import (
	"context"
	"io"
	"net/http"

	"github.com/mudler/cogito"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/priority"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

var _ = Describe("LLMWith and the priority class", func() {
	type seen struct {
		class    priority.Class
		set      bool
		internal bool
	}
	send := func(user *auth.User, class priority.Class) seen {
		var s seen
		p := provider.New()
		p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.class, s.set = priority.FromContext(r.Context())
			_, s.internal = auth.InternalUser(r.Context())
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}))
		llm := p.LLMWith(user, "m", provider.Params{Priority: class})
		stream, err := llm.(cogito.StreamingLLM).CreateChatCompletionStream(context.Background(), userRequest("hello"))
		Expect(err).ToNot(HaveOccurred())
		for range stream {
		}
		return s
	}

	user := &auth.User{ID: "u", Role: auth.RoleUser, Status: auth.StatusActive}

	It("carries the chat's class into the chat route as the internal user", func() {
		got := send(user, priority.Interactive)
		Expect(got.set).To(BeTrue())
		Expect(got.class).To(Equal(priority.Interactive))
		Expect(got.internal).To(BeTrue())

		got = send(user, priority.Batch)
		Expect(got.class).To(Equal(priority.Batch))
	})

	It("sets no class for a default chat", func() {
		got := send(user, priority.Default)
		Expect(got.set).To(BeFalse())
	})
})
