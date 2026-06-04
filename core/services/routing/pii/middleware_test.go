package pii

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"

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
// the middleware expects on the echo context (PIIIsEnabled + PIIDetectors).
type fakeModelPIIConfig struct {
	enabled   bool
	detectors []string
}

func (f fakeModelPIIConfig) PIIIsEnabled() bool     { return f.enabled }
func (f fakeModelPIIConfig) PIIDetectors() []string { return f.detectors }

func withModelConfig(cfg fakeModelPIIConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ctxKeyModelConfig, cfg)
			return next(c)
		}
	}
}

// resolverFor returns a NERDetectorResolver that maps each named model to
// the supplied NERConfig. Names absent from the map resolve to (zero,
// false) so the middleware fails closed — mirroring an unresolvable model.
func resolverFor(byName map[string]NERConfig) NERDetectorResolver {
	return func(name string) (NERConfig, bool) {
		cfg, ok := byName[name]
		return cfg, ok
	}
}

func serve(body *fakeRequest, cfg fakeModelPIIConfig, mw echo.MiddlewareFunc, withConfig bool) (*httptest.ResponseRecorder, *bool) {
	called := new(bool)
	e := echo.New()
	chain := []echo.MiddlewareFunc{setRequestOnContext(body)}
	if withConfig {
		chain = append(chain, withModelConfig(cfg))
	}
	chain = append(chain, mw)
	e.POST("/chat", func(c echo.Context) error {
		*called = true
		return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
	}, chain...)
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w, called
}

func nerCfg(action Action, entities ...NEREntity) NERConfig {
	return NERConfig{
		Detector:      &stubNERDetector{entities: entities},
		DefaultAction: action,
	}
}

var _ = Describe("RequestMiddleware (NER)", func() {
	store := func() EventStore { return NewMemoryEventStore(0) }

	It("masks a detected entity end-to-end", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice today"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"privacy-filter": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 6, End: 11, Score: 0.95}),
			})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"privacy-filter"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK), "body=%s", w.Body.String())
		Expect(body.Messages[0]).To(ContainSubstring("[REDACTED:ner:PER]"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1))
		Expect(events[0].PatternID).To(Equal("ner:PER"))
		Expect(events[0].Direction).To(Equal(DirectionIn))
	})

	It("blocks (400) when a detected entity's action is block", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"my password is hunter2 ok"}}
		cfg := NERConfig{
			Detector:      &stubNERDetector{entities: []NEREntity{{Group: "PASSWORD", Start: 15, End: 22, Score: 0.99}}},
			EntityActions: map[string]Action{"PASSWORD": ActionBlock},
		}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{"pf": cfg})))
		w, called := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusBadRequest), "body=%s", w.Body.String())
		Expect(*called).To(BeFalse(), "handler must not run when blocked")
		var resp map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		errBlock, _ := resp["error"].(map[string]any)
		Expect(errBlock["type"]).To(Equal("pii_blocked"))
	})

	It("allow leaves text intact but records an event", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"hi at alice@example.com"}}
		cfg := NERConfig{
			Detector:      &stubNERDetector{entities: []NEREntity{{Group: "EMAIL", Start: 6, End: 23, Score: 0.9}}},
			EntityActions: map[string]Action{"EMAIL": ActionAllow},
		}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{"pf": cfg})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(ContainSubstring("alice@example.com"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1))
		Expect(events[0].Action).To(Equal(ActionAllow))
	})

	It("passes through on no match", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"perfectly innocent text"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{"pf": nerCfg(ActionMask)})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("perfectly innocent text"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(BeEmpty())
	})

	It("skips when the model has PII disabled", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"pf": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 6, End: 11, Score: 0.95}),
			})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: false, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("Hi I'm Alice"), "disabled model must not redact")
	})

	It("passes through when the model lists no detectors", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("Hi I'm Alice"))
	})

	It("fails closed without a model config", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"pf": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 6, End: 11, Score: 0.95}),
			})))
		w, _ := serve(body, fakeModelPIIConfig{}, mw, false) // no model config on context

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("Hi I'm Alice"), "missing ModelPIIConfig should pass through")
	})

	It("unions multiple detectors", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Alice at acme"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"names": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 0, End: 5, Score: 0.9}),
				"orgs":  nerCfg(ActionMask, NEREntity{Group: "ORG", Start: 9, End: 13, Score: 0.9}),
			})))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"names", "orgs"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(ContainSubstring("[REDACTED:ner:PER]"))
		Expect(body.Messages[0]).To(ContainSubstring("[REDACTED:ner:ORG]"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(2))
	})

	It("fails closed (503) when a detector errors", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"contact alice@example.com"}}
		cfg := NERConfig{Detector: &stubNERDetector{err: errors.New("backend offline")}, DefaultAction: ActionMask}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{"pf": cfg})))
		w, called := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusServiceUnavailable), "body=%s", w.Body.String())
		Expect(*called).To(BeFalse())
		Expect(body.Messages[0]).To(ContainSubstring("alice@example.com"), "request body must be untouched on a fail-closed block")
		var resp map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		errBlock, _ := resp["error"].(map[string]any)
		Expect(errBlock["type"]).To(Equal("pii_ner_unavailable"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1))
		Expect(events[0].PatternID).To(Equal(nerUnavailablePattern))
	})

	It("fails closed (503) when a configured detector can't be resolved", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"contact alice@example.com"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{}))) // "missing" not present
		w, called := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"missing"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(*called).To(BeFalse())
		events, _ := st.List(context.Background(), ListQuery{Limit: 100})
		Expect(events).To(HaveLen(1))
		Expect(events[0].PatternID).To(Equal(nerUnavailablePattern))
	})

	It("nil redactor is passthrough", func() {
		body := &fakeRequest{Messages: []string{"alice@example.com"}}
		mw := RequestMiddleware(nil, nil, fakeAdapter(), nil)
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("alice@example.com"), "nil redactor must be a no-op")
	})

	It("WithPolicyResolver enables a model the per-model config left off (global default)", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice today"}}
		// The per-model config is disabled with no detectors; the policy
		// resolver (instance-wide default) turns it on and supplies one.
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"global-pf": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 6, End: 11, Score: 0.95}),
			})),
			WithPolicyResolver(func(_ any) (bool, []string) { return true, []string{"global-pf"} }))
		w, _ := serve(body, fakeModelPIIConfig{enabled: false}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK), "body=%s", w.Body.String())
		Expect(body.Messages[0]).To(ContainSubstring("[REDACTED:ner:PER]"))
	})

	It("WithPolicyResolver returning disabled short-circuits an otherwise-enabled model", func() {
		st := store()
		body := &fakeRequest{Messages: []string{"Hi I'm Alice today"}}
		mw := RequestMiddleware(&Redactor{}, st, fakeAdapter(), nil,
			WithNERResolver(resolverFor(map[string]NERConfig{
				"pf": nerCfg(ActionMask, NEREntity{Group: "PER", Start: 6, End: 11, Score: 0.95}),
			})),
			WithPolicyResolver(func(_ any) (bool, []string) { return false, nil }))
		w, _ := serve(body, fakeModelPIIConfig{enabled: true, detectors: []string{"pf"}}, mw, true)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(body.Messages[0]).To(Equal("Hi I'm Alice today"), "resolver disabled => no redaction")
	})
})
