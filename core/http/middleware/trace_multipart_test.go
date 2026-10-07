// SPDX-License-Identifier: MIT
package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("multipart transcription traces", func() {
	DescribeTable("leaves upload consumption to the handler", func(route string, enabled, expected bool) {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		app.ApplicationConfig().EnableTracing = enabled
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		f, err := form.CreateFormFile("file", "secret.wav")
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write([]byte("private upload"))
		Expect(err).NotTo(HaveOccurred())
		Expect(form.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, route, &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		originalBody := req.Body
		originalLen := body.Len()
		e := echo.New()
		trace := TraceMiddleware(app)
		ClearTraces()
		e.POST(route, func(c echo.Context) error {
			Expect(c.Request().Body).To(BeIdenticalTo(originalBody))
			Expect(body.Len()).To(Equal(originalLen))
			f, err := c.FormFile("file")
			Expect(err).NotTo(HaveOccurred())
			reader, err := f.Open()
			Expect(err).NotTo(HaveOccurred())
			defer reader.Close()
			audio, err := io.ReadAll(reader)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(audio)).To(Equal("private upload"))
			return c.String(http.StatusOK, "transcript")
		}, trace)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		if !expected {
			Expect(GetTraces()).To(BeEmpty())
			return
		}
		Eventually(GetTraces).Should(ConsistOf(HaveField("Response.Status", http.StatusOK)))
		exchange := GetTraces()[0]
		Expect(string(*exchange.Request.Body)).NotTo(ContainSubstring("private upload"))
		Expect(string(*exchange.Request.Body)).To(ContainSubstring("multipart upload omitted"))
		Expect(string(*exchange.Response.Body)).To(Equal("transcript"))
	},
		Entry("v1", "/v1/audio/transcriptions", true, true),
		Entry("legacy", "/audio/transcriptions", true, true),
		Entry("disabled", "/v1/audio/transcriptions", false, false),
		Entry("diarization excluded", "/v1/audio/diarization", true, false),
		Entry("registration excluded", "/v1/voice/register", true, false),
		Entry("other multipart unchanged", "/other", true, false),
	)
})
