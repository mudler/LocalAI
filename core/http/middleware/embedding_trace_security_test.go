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

	It("omits a top-level array body that the embedding binder rejects as structured", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.EnableTracing, config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		trace := TraceMiddleware(app)
		ClearTraces()
		payload := `[{"model":"m","input":{"content":[{"type":"input_audio","input_audio":{"data":"SECRETB64==","format":"wav"}}]}}]`
		for _, status := range []int{200, 400} {
			e := echo.New()
			req := httptest.NewRequest("POST", "/v1/embeddings", strings.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			c := e.NewContext(req, httptest.NewRecorder())
			c.SetPath("/v1/embeddings")
			_ = trace(func(c echo.Context) error {
				if status != 200 {
					return echo.NewHTTPError(status, "SECRETB64 echoed in error")
				}
				return c.String(200, "SECRETB64 echoed in response")
			})(c)
		}
		Eventually(func() int { return len(GetTraces()) }).Should(Equal(2))
		for _, exchange := range GetTraces() {
			dump, err := json.Marshal(exchange)
			Expect(err).NotTo(HaveOccurred())
			// The request body is stored as base64 in the JSON dump, so check the
			// raw bytes as well as the dump to catch the payload in either form.
			Expect(string(*exchange.Request.Body)).To(ContainSubstring("embedding payload omitted"))
			Expect(string(*exchange.Request.Body)).NotTo(ContainSubstring("SECRETB64"))
			Expect(string(dump)).NotTo(ContainSubstring("SECRETB64"))
		}
		// Exchanges are collected asynchronously. Flush them so a late write
		// cannot land in the shared buffer after this spec, as the pagination
		// specs seed that buffer and expect exact counts.
		ClearTraces()
	})
})
