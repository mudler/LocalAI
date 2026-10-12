// SPDX-License-Identifier: MIT
package middleware

import (
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace/tracepersist"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Embedding trace security", func() {
	It("omits embedding exchanges before inflight completed and persisted capture including errors", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.EnableTracing, config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		trace := TraceMiddleware(app)
		ClearTraces()
		for _, route := range []string{"/embeddings", "/v1/embeddings", "/v1/engines/:model/embeddings"} {
			for _, status := range []int{200, 400, 501} {
				e := echo.New()
				req := httptest.NewRequest("POST", "/embeddings", strings.NewReader(`{"input":["private-text",{"content":[{"type":"image_url","image_url":{"url":"https://example.invalid/?private-query=secret"}},{"type":"input_audio","input_audio":{"data":"cHJpdmF0ZS1tZWRpYQ==","format":"wav"}}]}]}`))
				req.Header.Set("Content-Type", "application/json")
				c := e.NewContext(req, httptest.NewRecorder())
				c.SetPath(route)
				original := req.Body
				err = trace(func(c echo.Context) error {
					Expect(req.Body).To(BeIdenticalTo(original))
					Expect(GetTraces()).To(BeEmpty())
					if status != 200 {
						return echo.NewHTTPError(status, "private-error-must-not-be-traced")
					}
					return c.String(200, "private-response")
				})(c)
				if status == 200 {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			}
		}
		// A nonembedding exchange is a FIFO barrier for the asynchronous persistence queue.
		e := echo.New()
		req := httptest.NewRequest("POST", "/other", strings.NewReader(`{"input":"ordinary-trace"}`))
		req.Header.Set("Content-Type", "application/json")
		c := e.NewContext(req, httptest.NewRecorder())
		c.SetPath("/other")
		Expect(trace(func(c echo.Context) error { return c.String(200, "ordinary-response") })(c)).To(Succeed())
		store, err := tracepersist.New[APIExchange](filepath.Join(root, "traces", "api"), 100)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { records, err := store.Load(); Expect(err).NotTo(HaveOccurred()); return len(records) }).Should(Equal(1))
		Expect(GetTraces()).To(HaveLen(1))
		Expect(GetTraces()[0].Request.Path).To(Equal("/other"))
		Expect(string(*GetTraces()[0].Request.Body)).To(ContainSubstring("ordinary-trace"))
	})
})
