// SPDX-License-Identifier: MIT
package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Drives the real POST /v1/responses middleware chain (openResponsesMiddleware)
// in front of a stub handler, with an in-process pattern detector, to prove
// the PII policy that guards /v1/chat/completions also guards this endpoint.
var _ = Describe("Open Responses PII filtering", func() {
	var (
		e        *echo.Echo
		cancel   context.CancelFunc
		reached  bool
		received *schema.OpenResponsesRequest
	)

	BeforeEach(func() {
		tmp := GinkgoT().TempDir()
		modelsDir := filepath.Join(tmp, "models")
		Expect(os.MkdirAll(modelsDir, 0o755)).To(Succeed())
		st, err := system.GetSystemState(
			system.WithModelPath(modelsDir),
			system.WithBackendPath(filepath.Join(tmp, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(st))
		Expect(err).ToNot(HaveOccurred())

		// The pattern tier runs in-process; no backend is loaded.
		files := map[string]string{
			"secret-filter.yaml": `name: secret-filter
backend: pattern
pii_detection:
  default_action: mask
  patterns:
    - name: SECRET
      match: "sk-test-[A-Za-z0-9]+"
      action: block
    - name: TOKEN
      match: "tok-[A-Za-z0-9]+"
      action: mask
`,
			"guarded.yaml": `name: guarded
backend: test
pii:
  enabled: true
  detectors: [secret-filter]
`,
		}
		for name, body := range files {
			p := filepath.Join(modelsDir, name)
			Expect(os.WriteFile(p, []byte(body), 0o644)).To(Succeed())
			Expect(app.ModelConfigLoader().ReadModelConfig(p)).To(Succeed())
		}

		re := middleware.NewRequestExtractor(app.ModelConfigLoader(), app.ModelLoader(), app.ApplicationConfig())
		reached, received = false, nil
		e = echo.New()
		e.POST("/v1/responses", func(c echo.Context) error {
			reached = true
			received = c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenResponsesRequest)
			return c.NoContent(http.StatusNoContent)
		}, openResponsesMiddleware(re, app)...)
	})

	AfterEach(func() { cancel() })

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("treats a model named like a former agent as a model name, never as an agent", func() {
		rec := post(`{"model":"former-agent","input":"hello"}`)
		Expect(rec.Code).To(SatisfyAny(Equal(http.StatusNoContent), Equal(http.StatusNotFound)), rec.Body.String())
		if reached {
			Expect(received.Model).To(Equal("former-agent"))
		}
	})

	It("blocks a secret in a string input with pii_blocked", func() {
		rec := post(`{"model":"guarded","input":"my key is sk-test-ABC123"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("pii_blocked"))
		Expect(rec.Body.String()).ToNot(ContainSubstring("sk-test-ABC123"), "the block response must not echo the secret")
		Expect(reached).To(BeFalse(), "a blocked request must not reach the handler")
	})

	It("blocks a secret hidden in a content part, instructions or tool output", func() {
		for _, body := range []string{
			`{"model":"guarded","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"key sk-test-ABC123"}]}]}`,
			`{"model":"guarded","instructions":"use sk-test-ABC123","input":"hi"}`,
			`{"model":"guarded","input":[{"type":"function_call_output","call_id":"c1","output":"sk-test-ABC123"}]}`,
			`{"model":"guarded","input":[{"type":"function_call_output","call_id":"c1","output":["sk-test-ABC123"]}]}`,
			`{"model":"guarded","input":[{"type":"function_call_output","call_id":"c1","output":[{"token":"sk-test-ABC123"}]}]}`,
		} {
			reached = false
			rec := post(body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring("pii_blocked"), body)
			Expect(reached).To(BeFalse(), body)
		}
	})

	It("masks a maskable value before the handler sees it", func() {
		rec := post(`{"model":"guarded","input":[{"type":"message","role":"user","content":"token tok-XYZ789 please"}]}`)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(reached).To(BeTrue())
		content := received.Input.([]any)[0].(map[string]any)["content"].(string)
		Expect(content).ToNot(ContainSubstring("tok-XYZ789"))
		Expect(content).To(HavePrefix("token "))
		Expect(content).To(HaveSuffix(" please"))
	})

	It("passes clean input through unchanged", func() {
		rec := post(`{"model":"guarded","instructions":"be brief","input":"what is the capital of France?"}`)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(reached).To(BeTrue())
		Expect(received.Input).To(Equal("what is the capital of France?"))
		Expect(received.Instructions).To(Equal("be brief"))
	})
})
