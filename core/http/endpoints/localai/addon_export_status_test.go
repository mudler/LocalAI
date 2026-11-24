// SPDX-License-Identifier: MIT
package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// exportGate stands for the application: the add-on gate and the models path
// check, the two questions the status routes depend on.
type exportGate struct{ addons, exports bool }

func (g exportGate) AddonsFeature(context.Context) bool       { return g.addons }
func (g exportGate) AddonsEnabled() bool                      { return g.addons }
func (g exportGate) ModelExportsAllowed(context.Context) bool { return g.exports }

var _ = Describe("Export status routes", func() {
	call := func(g exportGate, path string) *httptest.ResponseRecorder {
		e := echo.New()
		gate := httpMiddleware.RequireAddonsFeature(g)
		e.GET("/api/fine-tuning/status", localai.ModelExportsStatusEndpoint(g), gate)
		e.GET("/api/quantization/status", localai.ModelExportsStatusQuantizationEndpoint(g), gate)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	for _, path := range []string{"/api/fine-tuning/status", "/api/quantization/status"} {
		It("reports false when the models probe fails on "+path, func() {
			w := call(exportGate{addons: true, exports: false}, path)
			Expect(w.Code).To(Equal(http.StatusOK))
			var got map[string]bool
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(got).To(Equal(map[string]bool{"model_exports": false}))
		})

		It("reports true when the models probe passes on "+path, func() {
			w := call(exportGate{addons: true, exports: true}, path)
			Expect(w.Code).To(Equal(http.StatusOK))
			var got map[string]bool
			Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
			Expect(got).To(Equal(map[string]bool{"model_exports": true}))
		})

		It("answers the typed 503 when add-ons are off on "+path, func() {
			w := call(exportGate{addons: false, exports: true}, path)
			Expect(w.Code).To(Equal(http.StatusServiceUnavailable))
			Expect(w.Body.String()).To(ContainSubstring(gateway.ReasonAddonsOff))
			Expect(w.Body.String()).To(ContainSubstring(`"code":"unavailable"`))
		})
	}
})
