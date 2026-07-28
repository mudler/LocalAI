package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// captureBackend collects records the recorder forwards. We assert on
// it directly rather than going through StatsBackend.Aggregate because
// these tests verify the middleware -> recorder hop, not aggregation
// (which has its own tests in routing/billing).
type captureBackend struct {
	records []*auth.UsageRecord
}

func (c *captureBackend) Record(_ context.Context, r *auth.UsageRecord) error {
	c.records = append(c.records, r)
	return nil
}
func (c *captureBackend) Aggregate(_ context.Context, _ billing.AggregateQuery) ([]auth.UsageBucket, error) {
	return nil, nil
}
func (c *captureBackend) Close() error { return nil }

var _ = Describe("UsageMiddleware", func() {
	mockChat := func(usage string) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Content-Type", "application/json")
			body := fmt.Sprintf(`{"model":"qwen-7b","usage":%s}`, usage)
			return c.String(http.StatusOK, body)
		}
	}

	It("records under the synthetic local user when auth is off", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)
		fallback := &auth.User{ID: "local-uuid", Name: "local", Provider: auth.ProviderLocal}

		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}`),
			httpMiddleware.UsageMiddleware(rec, fallback),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		r := cap.records[0]
		Expect(r.UserID).To(Equal("local-uuid"))
		Expect(r.UserName).To(Equal("local"))
		Expect(r.Model).To(Equal("qwen-7b"))
		Expect(r.PromptTokens).To(Equal(int64(12)))
		Expect(r.CompletionTokens).To(Equal(int64(8)))
		Expect(r.TotalTokens).To(Equal(int64(20)))
	})

	It("does nothing when recorder is nil (--disable-stats)", func() {
		fallback := &auth.User{ID: "local-uuid", Name: "local"}
		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}`),
			httpMiddleware.UsageMiddleware(nil, fallback),
		)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		// no panic, no record — recorder=nil is the disable-stats path
	})

	It("skips when neither auth nor fallback user is available", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)

		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}`),
			httpMiddleware.UsageMiddleware(rec, nil),
		)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(BeEmpty())
	})

	It("ignores 5xx responses (no usage to attribute)", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)
		fallback := &auth.User{ID: "local-uuid", Name: "local"}

		e := echo.New()
		e.POST("/v1/chat/completions",
			func(c echo.Context) error {
				return c.String(http.StatusInternalServerError, `{"error":"boom"}`)
			},
			httpMiddleware.UsageMiddleware(rec, fallback),
		)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusInternalServerError))
		Expect(cap.records).To(BeEmpty())
	})

	It("records via context-stamped tokens when handler called StampUsage (streaming-safe path)", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)
		fallback := &auth.User{ID: "local-uuid", Name: "local"}

		// Simulate a streaming chat handler that emits SSE chunks WITHOUT a
		// terminal usage block (the common case — clients rarely set
		// stream_options.include_usage). The handler stamps the canonical
		// counts on the context just before returning. UsageMiddleware
		// must record from the stamp, not from body parsing.
		streamingHandler := func(c echo.Context) error {
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(c.Response().Writer, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = fmt.Fprint(c.Response().Writer, "data: [DONE]\n\n")
			httpMiddleware.StampUsage(c, "qwen-7b", 9, 5)
			return nil
		}

		e := echo.New()
		e.POST("/v1/chat/completions",
			streamingHandler,
			httpMiddleware.UsageMiddleware(rec, fallback),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		Expect(cap.records[0].PromptTokens).To(Equal(int64(9)))
		Expect(cap.records[0].CompletionTokens).To(Equal(int64(5)))
		Expect(cap.records[0].TotalTokens).To(Equal(int64(14)))
		Expect(cap.records[0].Model).To(Equal("qwen-7b"))
	})

	It("falls back to Anthropic body shape when no stamp is present", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)
		fallback := &auth.User{ID: "local-uuid", Name: "local"}

		// Simulates a passthrough proxy / foreign endpoint: no handler stamp,
		// so the middleware must parse the response body. Anthropic's shape
		// uses input_tokens / output_tokens, not the OpenAI names.
		anthropicHandler := func(c echo.Context) error {
			c.Response().Header().Set("Content-Type", "application/json")
			body := `{"model":"claude-sonnet","usage":{"input_tokens":15,"output_tokens":7}}`
			return c.String(http.StatusOK, body)
		}

		e := echo.New()
		e.POST("/v1/messages",
			anthropicHandler,
			httpMiddleware.UsageMiddleware(rec, fallback),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		Expect(cap.records[0].PromptTokens).To(Equal(int64(15)))
		Expect(cap.records[0].CompletionTokens).To(Equal(int64(7)))
		Expect(cap.records[0].TotalTokens).To(Equal(int64(22)))
		Expect(cap.records[0].Model).To(Equal("claude-sonnet"))
	})

	It("populates RequestedModel/ServedModel from echo context when set", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)
		fallback := &auth.User{ID: "local-uuid", Name: "local"}

		// A pre-handler stand-in for the future router middleware: it
		// rewrites Served and remembers the original Requested. Once the
		// real router lands, this is exactly the contract it must keep.
		setRouterContext := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set(httpMiddleware.ContextKeyRequestedModel, "auto")
				c.Set(httpMiddleware.ContextKeyServedModel, "qwen-7b")
				return next(c)
			}
		}

		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}`),
			httpMiddleware.UsageMiddleware(rec, fallback),
			setRouterContext,
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		Expect(cap.records[0].RequestedModel).To(Equal("auto"))
		Expect(cap.records[0].ServedModel).To(Equal("qwen-7b"))
	})

	// stampAuth is a stand-in for the auth middleware: it sets the
	// echo-context keys UsageMiddleware reads. Pass source=="" to
	// simulate the unauthenticated/legacy path; pass key=nil to skip
	// the API-key snapshot.
	stampAuth := func(user *auth.User, source string, key *auth.UserAPIKey) echo.MiddlewareFunc {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if user != nil {
					c.Set("auth_user", user)
				}
				if source != "" {
					c.Set("auth_source", source)
				}
				if key != nil {
					c.Set("auth_apikey", key)
				}
				return next(c)
			}
		}
	}

	It("records source=web when auth_source is web and snapshots no API key", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)

		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`),
			httpMiddleware.UsageMiddleware(rec, nil),
			stampAuth(&auth.User{ID: "alice", Name: "Alice"}, auth.UsageSourceWeb, nil),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		r := cap.records[0]
		Expect(r.UserID).To(Equal("alice"))
		Expect(r.Source).To(Equal(auth.UsageSourceWeb))
		Expect(r.APIKeyID).To(BeNil())
		Expect(r.APIKeyName).To(BeEmpty())
	})

	It("records source=apikey with snapshotted name when auth_apikey is set", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)

		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}`),
			httpMiddleware.UsageMiddleware(rec, nil),
			stampAuth(
				&auth.User{ID: "alice", Name: "Alice"},
				auth.UsageSourceAPIKey,
				&auth.UserAPIKey{ID: "key-1", Name: "ci-runner"},
			),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		r := cap.records[0]
		Expect(r.Source).To(Equal(auth.UsageSourceAPIKey))
		Expect(r.APIKeyID).ToNot(BeNil())
		Expect(*r.APIKeyID).To(Equal("key-1"))
		Expect(r.APIKeyName).To(Equal("ci-runner"))
	})

	It("defaults source=web when auth_source is empty", func() {
		cap := &captureBackend{}
		rec := billing.NewRecorder(cap)

		// Only user set, no source — the middleware must classify the
		// row as web rather than dropping it from per-source aggregates.
		e := echo.New()
		e.POST("/v1/chat/completions",
			mockChat(`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}`),
			httpMiddleware.UsageMiddleware(rec, nil),
			stampAuth(&auth.User{ID: "alice", Name: "Alice"}, "", nil),
		)

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(cap.records).To(HaveLen(1))
		Expect(cap.records[0].Source).To(Equal(auth.UsageSourceWeb))
	})
})
