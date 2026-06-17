package chat

import (
	"context"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chat session", func() {
	It("keeps model switching and message history out of the terminal adapter", func() {
		client := &fakeChatClient{
			models: []string{"alpha", "beta"},
			answer: "pong",
		}

		session, err := newChatSession(context.Background(), client, "alpha")
		Expect(err).ToNot(HaveOccurred())
		Expect(session.CurrentModel()).To(Equal("alpha"))

		Expect(session.SwitchModel("beta")).To(Succeed())
		Expect(session.CurrentModel()).To(Equal("beta"))
		Expect(session.Send(context.Background(), "ping", io.Discard)).To(Succeed())

		Expect(client.requests).To(HaveLen(1))
		Expect(client.requests[0].model).To(Equal("beta"))
		Expect(client.requests[0].messages).To(HaveLen(1))
		Expect(client.requests[0].messages[0].Content).To(Equal("ping"))
	})
})

type fakeChatClient struct {
	models   []string
	answer   string
	requests []fakeChatRequest
}

type fakeChatRequest struct {
	model    string
	messages []chatMessage
}

func (c *fakeChatClient) ListModels(context.Context) ([]string, error) {
	return c.models, nil
}

func (c *fakeChatClient) StreamChat(_ context.Context, model string, messages []chatMessage, out io.Writer) (string, error) {
	copied := make([]chatMessage, len(messages))
	copy(copied, messages)
	c.requests = append(c.requests, fakeChatRequest{model: model, messages: copied})
	if _, err := io.WriteString(out, c.answer); err != nil {
		return "", err
	}
	return c.answer, nil
}
