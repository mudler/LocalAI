package cloudproxy

import (
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/pii"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildStreamFilter", func() {
	var (
		c  echo.Context
		cfg *config.ModelConfig
	)

	BeforeEach(func() {
		e := echo.New()
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		c = e.NewContext(req, rec)
		piiOn := true
		cfg = &config.ModelConfig{
			Backend: "cloud-proxy",
			PII:     config.PIIConfig{Enabled: &piiOn},
		}
	})

	// Three guards must each independently force a nil return — proves
	// the gate is a logical AND, not an order-dependent short-circuit
	// that silently activates one branch.
	It("returns nil when isStream is false", func() {
		patterns, err := pii.Compile(pii.DefaultPatterns())
		Expect(err).NotTo(HaveOccurred())
		r := pii.NewRedactor(patterns)
		Expect(BuildStreamFilter(c, cfg, false, r, nil, "corr-1")).To(BeNil())
	})

	It("returns nil when piiRedactor is nil", func() {
		Expect(BuildStreamFilter(c, cfg, true, nil, nil, "corr-1")).To(BeNil())
	})

	It("returns nil when the model has PII disabled", func() {
		piiOff := false
		cfg.PII.Enabled = &piiOff
		patterns, err := pii.Compile(pii.DefaultPatterns())
		Expect(err).NotTo(HaveOccurred())
		r := pii.NewRedactor(patterns)
		Expect(BuildStreamFilter(c, cfg, true, r, nil, "corr-1")).To(BeNil())
	})

	It("returns a configured filter when all preconditions hold", func() {
		patterns, err := pii.Compile(pii.DefaultPatterns())
		Expect(err).NotTo(HaveOccurred())
		r := pii.NewRedactor(patterns)
		store := pii.NewMemoryEventStore(8)
		filter := BuildStreamFilter(c, cfg, true, r, store, "corr-xyz")
		Expect(filter).NotTo(BeNil())
	})

	// Empty correlationID is allowed — some entry points don't have one.
	// The filter must still construct so the stream can flow.
	It("constructs a filter even when correlationID is empty", func() {
		patterns, err := pii.Compile(pii.DefaultPatterns())
		Expect(err).NotTo(HaveOccurred())
		r := pii.NewRedactor(patterns)
		Expect(BuildStreamFilter(c, cfg, true, r, nil, "")).NotTo(BeNil())
	})
})
