package provider_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sashabaranov/go-openai"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func get(t *testing.T, c *http.Client, ctx context.Context, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

func TestTransportServesRequestsWithoutANetwork(t *testing.T) {
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Path", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "from the router")
	}))
	// The host name resolves nowhere, so only the transport can answer.
	resp, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTeapot || string(body) != "from the router" || resp.Header.Get("X-Seen-Path") != "/v1/ping" {
		t.Fatalf("got %d %q header=%v", resp.StatusCode, body, resp.Header)
	}
}

func TestTransportWithoutAHandlerFailsClearly(t *testing.T) {
	_, err := get(t, provider.NewTransport().Client(nil), context.Background(), "http://studio.invalid/x")
	if err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("err = %v, want a 'no handler' error", err)
	}
}

// Tokens must reach the caller as the handler writes them, not when it ends.
func TestTransportStreamsBeforeTheHandlerReturns(t *testing.T) {
	release := make(chan struct{})
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first ")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second")
	}))
	resp, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 6)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "first " {
		t.Fatalf("first chunk = %q, %v", buf, err)
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "second" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestTransportStopsTheHandlerWhenTheCallerGivesUp(t *testing.T) {
	stopped := make(chan struct{})
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "x")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	resp, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/slow")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // the caller stops reading
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler kept running after the caller closed the body")
	}
}

// The transport carries the user into the auth middleware, which then lets the
// request through; a plain client still needs a credential.
func TestTransportActsAsTheGivenUser(t *testing.T) {
	e := echo.New()
	e.Use(auth.Middleware(nil, &config.ApplicationConfig{ApiKeys: []string{"a-key-nobody-sends"}}))
	e.GET("/v1/whoami", func(c echo.Context) error { return c.String(http.StatusOK, auth.GetUser(c).ID) })
	tr := provider.NewTransport()
	tr.SetHandler(e)

	user := &auth.User{ID: "u-1", Role: auth.RoleUser, Status: auth.StatusActive}
	resp, err := get(t, tr.Client(user), context.Background(), "http://studio.invalid/v1/whoami")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "u-1" {
		t.Fatalf("as the user: %d %q", resp.StatusCode, body)
	}

	anon, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/v1/whoami")
	if err != nil {
		t.Fatal(err)
	}
	anon.Body.Close()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a user: %d, want 401", anon.StatusCode)
	}
}

// End to end with cogito's client: the model call goes through the router.
func TestLLMCallsTheChatRoute(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if got := reply.ChatCompletionResponse.Choices[0].Message.Content; got != "hello from the router" {
		t.Fatalf("content = %q", got)
	}
}
