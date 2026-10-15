// SPDX-License-Identifier: MIT
package middleware

import (
	"encoding/json"
	"io"
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
	It("keeps legacy embedding traces and omits structured payloads including errors", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.EnableTracing, config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		trace := TraceMiddleware(app)
		ClearTraces()
		run := func(route, payload string, status int) {
			e := echo.New()
			req := httptest.NewRequest("POST", "/embeddings", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			c := e.NewContext(req, httptest.NewRecorder())
			c.SetPath(route)
			err := trace(func(c echo.Context) error {
				// The handler still sees the whole body.
				raw, readErr := io.ReadAll(c.Request().Body)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(string(raw)).To(Equal(payload))
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
		structured := `{"input":["private-text",{"content":[{"type":"image_url","image_url":{"url":"https://example.invalid/?private-query=secret"}},{"type":"input_audio","input_audio":{"data":"cHJpdmF0ZS1tZWRpYQ==","format":"wav"}}]}]}`
		legacy := `{"input":"hi"}`
		routes := []string{"/embeddings", "/v1/embeddings", "/v1/engines/:model/embeddings"}
		count := 0
		for _, route := range routes {
			for _, status := range []int{200, 400, 501} {
				run(route, structured, status)
				run(route, legacy, status)
				count += 2
			}
		}
		Eventually(func() int { return len(GetTraces()) }).Should(Equal(count))
		legacyTraces := 0
		for _, exchange := range GetTraces() {
			dump, err := json.Marshal(exchange)
			Expect(err).NotTo(HaveOccurred())
			for _, secret := range []string{"private-text", "private-query", "cHJpdmF0", "private-error", "private-response"} {
				if string(*exchange.Request.Body) == legacy {
					continue
				}
				Expect(string(dump)).NotTo(ContainSubstring(secret))
			}
			if string(*exchange.Request.Body) == legacy {
				legacyTraces++
			} else {
				Expect(string(*exchange.Request.Body)).NotTo(ContainSubstring("input"))
			}
		}
		Expect(legacyTraces).To(Equal(len(routes) * 3))
		store, err := tracepersist.New[APIExchange](filepath.Join(root, "traces", "api"), 100)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { records, err := store.Load(); Expect(err).NotTo(HaveOccurred()); return len(records) }).Should(Equal(count))
	})
})
