package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubDetector is a fixed NER detector for the resolver-level unit tests.
type stubDetector struct {
	ents []pii.NEREntity
	err  error
}

func (s stubDetector) Detect(_ context.Context, _ string) ([]pii.NEREntity, error) {
	return s.ents, s.err
}

var _ = Describe("RunPIIScan (resolver + scan core)", func() {
	ctx := context.Background()

	resolver := func(name string) (pii.NERConfig, bool) {
		if name != "det" {
			return pii.NERConfig{}, false
		}
		return pii.NERConfig{
			Detector:      stubDetector{ents: []pii.NEREntity{{Group: "EMAIL", Start: 0, End: 5, Score: 0.9}}},
			EntityActions: map[string]pii.Action{"EMAIL": pii.ActionMask},
			Source:        pii.SourceNER,
		}, true
	}

	It("resolves named detectors and returns their spans", func() {
		res, err := RunPIIScan(ctx, resolver, nil, nil, []string{"det"}, "", "jane@acme.io")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].Pattern).To(Equal("ner:EMAIL"))
		Expect(res.Masked).To(BeTrue())
	})

	It("fails closed with ErrUnknownDetector for an unresolvable name", func() {
		_, err := RunPIIScan(ctx, resolver, nil, nil, []string{"nope"}, "", "x")
		Expect(errors.Is(err, ErrUnknownDetector)).To(BeTrue())
	})

	It("returns ErrNoDetectors when nothing is selected", func() {
		_, err := RunPIIScan(ctx, resolver, nil, nil, nil, "", "x")
		Expect(errors.Is(err, ErrNoDetectors)).To(BeTrue())
	})
})

var _ = Describe("PII analyze/redact endpoints", func() {
	var (
		app    *application.Application
		e      *echo.Echo
		tmp    string
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		var err error
		tmp, err = os.MkdirTemp("", "pii-api-test-*")
		Expect(err).ToNot(HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())

		modelsDir := filepath.Join(tmp, "models")
		Expect(os.MkdirAll(modelsDir, 0o755)).To(Succeed())

		st, err := system.GetSystemState(
			system.WithModelPath(modelsDir),
			system.WithBackendPath(filepath.Join(tmp, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = application.New(config.WithContext(ctx), config.WithSystemState(st))
		Expect(err).ToNot(HaveOccurred())

		// A pattern detector with two deterministic patterns: one blocks, one
		// masks. No backend is loaded — the pattern tier runs in-process.
		detYAML := `name: secret-filter
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
`
		// A consuming model that opts into the detector, for the model-fallback path.
		consumerYAML := `name: chatmodel
pii:
  enabled: true
  detectors: [secret-filter]
`
		// PII-enabled but names no detectors: scanned only when the
		// instance-wide default detectors are set, else a 400.
		defaultsYAML := `name: defaultsmodel
pii:
  enabled: true
`
		// Lists detectors but never enables PII — the middleware ignores it,
		// so the model path must too.
		disabledYAML := `name: disabledmodel
pii:
  detectors: [secret-filter]
`
		detPath := filepath.Join(modelsDir, "secret-filter.yaml")
		consumerPath := filepath.Join(modelsDir, "chatmodel.yaml")
		defaultsPath := filepath.Join(modelsDir, "defaultsmodel.yaml")
		disabledPath := filepath.Join(modelsDir, "disabledmodel.yaml")
		Expect(os.WriteFile(detPath, []byte(detYAML), 0o644)).To(Succeed())
		Expect(os.WriteFile(consumerPath, []byte(consumerYAML), 0o644)).To(Succeed())
		Expect(os.WriteFile(defaultsPath, []byte(defaultsYAML), 0o644)).To(Succeed())
		Expect(os.WriteFile(disabledPath, []byte(disabledYAML), 0o644)).To(Succeed())
		Expect(app.ModelConfigLoader().ReadModelConfig(detPath)).To(Succeed())
		Expect(app.ModelConfigLoader().ReadModelConfig(consumerPath)).To(Succeed())
		Expect(app.ModelConfigLoader().ReadModelConfig(defaultsPath)).To(Succeed())
		Expect(app.ModelConfigLoader().ReadModelConfig(disabledPath)).To(Succeed())

		e = echo.New()
		e.POST("/api/pii/analyze", PIIAnalyzeEndpoint(app))
		e.POST("/api/pii/redact", PIIRedactEndpoint(app))
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(tmp)).To(Succeed())
	})

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("analyze reports a block-class entity without mutating text (200)", func() {
		rec := post("/api/pii/analyze", `{"text":"my key sk-test-abc123 ok","detectors":["secret-filter"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp struct {
			Entities []struct {
				EntityType string `json:"entity_type"`
				Source     string `json:"source"`
				Action     string `json:"action"`
			} `json:"entities"`
			Blocked bool `json:"blocked"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Blocked).To(BeTrue())
		Expect(resp.Entities).To(HaveLen(1))
		Expect(resp.Entities[0].EntityType).To(Equal("SECRET"))
		Expect(resp.Entities[0].Source).To(Equal("pattern"))
		Expect(resp.Entities[0].Action).To(Equal("block"))
	})

	It("redact masks a mask-class match and returns redacted text (200)", func() {
		rec := post("/api/pii/redact", `{"text":"here is tok-xyz789 done","detectors":["secret-filter"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var resp struct {
			RedactedText string `json:"redacted_text"`
			Masked       bool   `json:"masked"`
			Blocked      bool   `json:"blocked"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Masked).To(BeTrue())
		Expect(resp.Blocked).To(BeFalse())
		Expect(resp.RedactedText).To(ContainSubstring("[REDACTED:pattern:TOKEN]"))
		Expect(resp.RedactedText).ToNot(ContainSubstring("tok-xyz789"))
	})

	It("redact returns 400 pii_blocked for a block-class match", func() {
		rec := post("/api/pii/redact", `{"text":"key sk-test-abc123","detectors":["secret-filter"]}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("pii_blocked"))
		// The raw secret must never appear in the block response.
		Expect(rec.Body.String()).ToNot(ContainSubstring("sk-test-abc123"))
	})

	It("400s when no detector is selected", func() {
		rec := post("/api/pii/redact", `{"text":"sk-test-abc123"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("resolves detectors from a consuming model via the model field", func() {
		rec := post("/api/pii/analyze", `{"text":"tok-aaa111","model":"chatmodel"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp struct {
			Entities []struct {
				EntityType string `json:"entity_type"`
			} `json:"entities"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Entities).To(HaveLen(1))
		Expect(resp.Entities[0].EntityType).To(Equal("TOKEN"))
	})

	It("400s for a PII-enabled model with no detectors and no instance default", func() {
		rec := post("/api/pii/analyze", `{"text":"tok-aaa111","model":"defaultsmodel"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("falls back to the instance-wide default detectors for an enabled model", func() {
		defaults := []string{"secret-filter"}
		app.ApplicationConfig().ApplyRuntimeSettings(&config.RuntimeSettings{PIIDefaultDetectors: &defaults})

		rec := post("/api/pii/analyze", `{"text":"tok-aaa111","model":"defaultsmodel"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp struct {
			Entities []struct {
				EntityType string `json:"entity_type"`
			} `json:"entities"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp.Entities).To(HaveLen(1))
		Expect(resp.Entities[0].EntityType).To(Equal("TOKEN"))
	})

	It("400s for a model that lists detectors but has PII disabled, like the middleware", func() {
		rec := post("/api/pii/analyze", `{"text":"tok-aaa111","model":"disabledmodel"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("invalid_request"))
	})

	It("records redact-API events with origin pii_redact", func() {
		_ = post("/api/pii/redact", `{"text":"here is tok-xyz789 done","detectors":["secret-filter"]}`)
		events, err := app.PIIEvents().List(context.Background(), pii.ListQuery{Origin: pii.OriginRedactAPI})
		Expect(err).ToNot(HaveOccurred())
		Expect(len(events)).To(BeNumerically(">=", 1))
		Expect(events[0].PatternID).To(Equal("pattern:TOKEN"))
		// Regression: API-recorded events must carry a real timestamp, not the
		// zero value (the handler, unlike the middleware, originally omitted it).
		Expect(events[0].CreatedAt.IsZero()).To(BeFalse())
	})
})
