package pii

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mediaPIIConfig adds pii.block_unscanned_media to the fake model config.
type mediaPIIConfig struct {
	fakeModelPIIConfig
	blockMedia bool
}

func (m mediaPIIConfig) PIIBlockUnscannedMedia() bool { return m.blockMedia }

// serveAny is serve() for a config value of any type.
func serveAny(body *fakeRequest, cfg any, mw echo.MiddlewareFunc) (*httptest.ResponseRecorder, *bool) {
	called := new(bool)
	e := echo.New()
	setCfg := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(ctxKeyModelConfig, cfg)
			return next(c)
		}
	}
	e.POST("/chat", func(c echo.Context) error {
		*called = true
		return c.JSON(http.StatusOK, map[string]string{"ok": "yes"})
	}, setRequestOnContext(body), setCfg, mw)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{}`)))
	return w, called
}

func mediaAdapter(kinds ...string) Adapter {
	a := fakeAdapter()
	a.Unscanned = func(any) []string { return kinds }
	return a
}

var _ = Describe("RequestMiddleware (block_unscanned_media)", func() {
	clean := map[string]NERConfig{"privacy-filter": nerCfg(ActionBlock)}

	run := func(cfg mediaPIIConfig, adapter Adapter, st EventStore) (int, map[string]any, bool) {
		body := &fakeRequest{Messages: []string{"what is on this picture?"}}
		mw := RequestMiddleware(&Redactor{}, st, adapter, nil, WithNERResolver(resolverFor(clean)))
		w, called := serveAny(body, cfg, mw)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, *called
	}

	It("blocks a request with media the detectors cannot inspect and records an event", func() {
		st := NewMemoryEventStore(0)
		code, out, called := run(mediaPIIConfig{fakeModelPIIConfig{enabled: true, detectors: []string{"privacy-filter"}}, true},
			mediaAdapter("image_url"), st)
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(called).To(BeFalse(), "the handler (and so the upstream) must not see the request")
		Expect(out["error"].(map[string]any)["type"]).To(Equal("pii_blocked"))
		Expect(out["error"].(map[string]any)["message"]).To(ContainSubstring("image_url"))
		events, _ := st.List(context.Background(), ListQuery{Limit: 10})
		Expect(events).To(HaveLen(1))
		Expect(events[0].PatternID).To(Equal(unscannedMediaPattern))
		Expect(events[0].Action).To(Equal(ActionBlock))
	})

	It("blocks even when the model lists no detectors", func() {
		code, _, called := run(mediaPIIConfig{fakeModelPIIConfig{enabled: true}, true}, mediaAdapter("input_audio"), nil)
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(called).To(BeFalse())
	})

	It("lets text-only requests through", func() {
		code, _, called := run(mediaPIIConfig{fakeModelPIIConfig{enabled: true, detectors: []string{"privacy-filter"}}, true},
			mediaAdapter(), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(called).To(BeTrue())
	})

	It("does not block media when the option is off (default)", func() {
		code, _, called := run(mediaPIIConfig{fakeModelPIIConfig{enabled: true, detectors: []string{"privacy-filter"}}, false},
			mediaAdapter("image_url"), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(called).To(BeTrue())
	})

	It("does not block media when PII is disabled for the model", func() {
		code, _, called := run(mediaPIIConfig{fakeModelPIIConfig{enabled: false}, true}, mediaAdapter("image_url"), nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(called).To(BeTrue())
	})
})
