package provider_test

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func get(c *http.Client, ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	Expect(err).ToNot(HaveOccurred())
	return c.Do(req)
}

var _ = Describe("the transport", func() {
	It("serves requests without a network", func() {
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Seen-Path", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			_, _ = io.WriteString(w, "from the router")
		}))
		// The host name resolves nowhere, so only the transport can answer.
		resp, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/v1/ping")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		Expect(resp.StatusCode).To(Equal(http.StatusTeapot))
		Expect(string(body)).To(Equal("from the router"))
		Expect(resp.Header.Get("X-Seen-Path")).To(Equal("/v1/ping"))
	})

	It("without a handler fails clearly", func() {
		_, err := get(provider.NewTransport().Client(nil), context.Background(), "http://studio.invalid/x")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no handler"))
	})

	// Tokens must reach the caller as the handler writes them, not when it ends.
	It("streams before the handler returns", func() {
		release := make(chan struct{})
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "first ")
			w.(http.Flusher).Flush()
			<-release
			_, _ = io.WriteString(w, "second")
		}))
		resp, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/stream")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 6)
		_, err = io.ReadFull(resp.Body, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("first "))
		close(release)
		rest, _ := io.ReadAll(resp.Body)
		Expect(string(rest)).To(Equal("second"))
	})

	It("stops the handler when the caller gives up", func() {
		stopped := make(chan struct{})
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "x")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(stopped)
		}))
		resp, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/slow")
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close() // the caller stops reading
		Eventually(stopped, 5*time.Second).Should(BeClosed(), "the handler kept running after the caller closed the body")
	})

	// The transport carries the user into the auth middleware, which then lets the
	// request through; a plain client still needs a credential.
	It("acts as the given user", func() {
		e := echo.New()
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{ApiKeys: []string{"a-key-nobody-sends"}}))
		e.GET("/v1/whoami", func(c echo.Context) error { return c.String(http.StatusOK, auth.GetUser(c).ID) })
		tr := provider.NewTransport()
		tr.SetHandler(e)

		user := &auth.User{ID: "u-1", Role: auth.RoleUser, Status: auth.StatusActive}
		resp, err := get(tr.Client(user), context.Background(), "http://studio.invalid/v1/whoami")
		Expect(err).ToNot(HaveOccurred())
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "as the user: %q", body)
		Expect(string(body)).To(Equal("u-1"))

		anon, err := get(tr.Client(nil), context.Background(), "http://studio.invalid/v1/whoami")
		Expect(err).ToNot(HaveOccurred())
		_ = anon.Body.Close()
		Expect(anon.StatusCode).To(Equal(http.StatusUnauthorized), "without a user")
	})

	// End to end with cogito's client: the model call goes through the router.
	It("serves the LLM chat route", func() {
		e := echo.New()
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]any{"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "hello from the router"}},
			}})
		})
		p := provider.New()
		p.Transport().SetHandler(e)

		llm := p.LLM(nil, "some-model")
		reply, _, err := llm.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
			Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "hi"}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(reply.ChatCompletionResponse.Choices[0].Message.Content).To(Equal("hello from the router"))
	})
})
