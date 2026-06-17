package pii

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeRequest is the simplest possible parsed-request shape: a list of
// strings that the adapter scans and writes back. Lets us drive the
// middleware without dragging the real schema package in.
type fakeRequest struct {
	Messages []string
}

func fakeAdapter() Adapter {
	return Adapter{
		Scan: func(parsed any) []ScannedText {
			r, ok := parsed.(*fakeRequest)
			if !ok {
				return nil
			}
			out := make([]ScannedText, len(r.Messages))
			for i, m := range r.Messages {
				out[i] = ScannedText{Index: i, Text: m}
			}
			return out
		},
		Apply: func(parsed any, updates []ScannedText) {
			r, ok := parsed.(*fakeRequest)
			if !ok {
				return
			}
			for _, u := range updates {
				r.Messages[u.Index] = u.Text
			}
		},
	}
}

func setRequestOnContext(req *fakeRequest) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ctxKeyParsedRequest, req)
			return next(c)
		}
	}
}

// fakeModelPIIConfig satisfies the duck-typed ModelPIIConfig interface
// the middleware expects on the echo context. The real implementation
// lives on *config.ModelConfig; using a fake here keeps these tests
// out of the core/config import graph.
type fakeModelPIIConfig struct {
	enabled   bool
	overrides map[string]string
}

func (f fakeModelPIIConfig) PIIIsEnabled() bool                     { return f.enabled }
func (f fakeModelPIIConfig) PIIPatternOverrides() map[string]string { return f.overrides }

// withModelConfig wires a ModelPIIConfig onto the context so the
// middleware's per-model gate doesn't fail-closed during tests. Pass
// enabled=true for the default test path; explicit-false tests should
// use the gating spec further down instead.
func withModelConfig(cfg fakeModelPIIConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ctxKeyModelConfig, cfg)
			return next(c)
		}
	}
}

func newTestRedactor(ids ...string) *Redactor {
	patterns, err := Compile(pick(DefaultPatterns(), ids))
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "compile")
	return NewRedactor(patterns)
}

var _ = Describe("RequestMiddleware", func() {
	It("masks email", func() {
		red := newTestRedactor("email")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()
		user := &auth.User{ID: "user-1", Name: "alice"}

		body := &fakeRequest{Messages: []string{"contact me at alice@example.com"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: true}), mw, func(next echo.HandlerFunc) echo.HandlerFunc {
			// Inject the user as if upstream auth ran.
			return func(c echo.Context) error {
				c.Set("auth_user", user)
				return next(c)
			}
		})

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK), "body=%s", w.Body.String())
		Expect(body.Messages[0]).NotTo(ContainSubstring("alice@example.com"), "request body should be redacted in place")
		Expect(body.Messages[0]).To(ContainSubstring("[REDACTED:email]"))

		events, err := store.List(context.Background(), ListQuery{Limit: 100})
		Expect(err).NotTo(HaveOccurred(), "list events")
		Expect(events).To(HaveLen(1))
		Expect(events[0].PatternID).To(Equal("email"))
		Expect(events[0].Direction).To(Equal(DirectionIn))
	})

	It("blocks api key", func() {
		red := newTestRedactor("api_key_prefix")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"my key is sk-abcdefghijklmnopqrstuvwxyz0123456789"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		handlerCalled := false
		e.POST("/chat", func(c echo.Context) error {
			handlerCalled = true
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: true}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest), "expected 400 on block; body=%s", w.Body.String())
		Expect(handlerCalled).To(BeFalse(), "handler must not run when request is blocked")
		// Ensure the matched value never appears in the response body.
		Expect(w.Body.String()).NotTo(ContainSubstring("abcdefghijklmnopqrstuvwxyz0123456789"), "blocked response leaks the matched value")

		var resp map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		errBlock, ok := resp["error"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(errBlock["type"]).To(Equal("pii_blocked"))
	})

	It("allow leaves text intact but still records an event", func() {
		patterns, _ := Compile([]Pattern{{
			ID: "email", Description: "Email", Action: ActionAllow, MaxMatchLength: 254,
		}})
		red := NewRedactor(patterns)
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"hi at alice@example.com"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: true}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		// allow does NOT mutate the body — the model still sees the email.
		Expect(body.Messages[0]).To(ContainSubstring("alice@example.com"), "allow should leave text intact")
		// ...but the detection is still recorded for audit.
		events, _ := store.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1), "allow should still record a PIIEvent")
		Expect(events[0].Action).To(Equal(ActionAllow))
	})

	It("no match passes through", func() {
		red := newTestRedactor()
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"perfectly innocent text"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: true}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("perfectly innocent text"), "body should be untouched")
		events, _ := store.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(BeEmpty(), "expected 0 events on no-match input")
	})

	It("skips when model config disabled", func() {
		// Per-model gating is the new contract: a model with PIIIsEnabled
		// returning false must bypass redaction entirely, even if the
		// global redactor has matching patterns.
		red := newTestRedactor("email")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"contact alice@example.com"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: false}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(ContainSubstring("alice@example.com"), "disabled model must not redact")
		events, _ := store.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(BeEmpty(), "disabled model must produce no events")
	})

	It("fails closed without model config", func() {
		// Routes that wire the middleware before SetModelAndConfig, or
		// non-chat routes lacking a model, hit this path. The contract
		// is fail-closed: pass through without redaction so a missing
		// model can't accidentally leak through global defaults.
		red := newTestRedactor("email")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"contact alice@example.com"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		// Note: no withModelConfig in the chain.
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(ContainSubstring("alice@example.com"), "missing ModelPIIConfig should fail-closed (no redaction)")
	})

	It("applies per-model override", func() {
		// email defaults to mask. A per-model override upgrades it to
		// block. The middleware short-circuits with 400, the request
		// body is never touched, and the events log records action=block.
		red := newTestRedactor("email")
		store := NewMemoryEventStore(0)
		defer func() { _ = store.Close() }()

		body := &fakeRequest{Messages: []string{"contact alice@example.com"}}
		mw := RequestMiddleware(red, store, fakeAdapter(), nil)

		e := echo.New()
		handlerCalled := false
		e.POST("/chat", func(c echo.Context) error {
			handlerCalled = true
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body),
			withModelConfig(fakeModelPIIConfig{
				enabled:   true,
				overrides: map[string]string{"email": "block"},
			}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusBadRequest), "expected 400 from override-block; body=%s", w.Body.String())
		Expect(handlerCalled).To(BeFalse(), "handler must not run when override blocks")
		events, _ := store.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1))
		Expect(events[0].Action).To(Equal(ActionBlock), "event must record the resolved (override) action")
	})

	It("nil redactor is passthrough", func() {
		body := &fakeRequest{Messages: []string{"alice@example.com"}}
		mw := RequestMiddleware(nil, nil, fakeAdapter(), nil)

		e := echo.New()
		e.POST("/chat", func(c echo.Context) error {
			return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
		}, setRequestOnContext(body), withModelConfig(fakeModelPIIConfig{enabled: true}), mw)

		req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("alice@example.com"), "nil redactor must be a no-op")
	})
})
