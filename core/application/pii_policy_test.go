package application

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolvePIIPolicy", func() {
	chat := config.FLAG_CHAT
	bp := func(b bool) *bool { return &b }
	mk := func(c *config.ApplicationConfig) *Application {
		return &Application{applicationConfig: c}
	}

	It("lets an explicit pii.enabled=false win over every global default", func() {
		app := mk(&config.ApplicationConfig{
			PIIDefaultDetectors: []string{"pf"},
			PIIDefaultUsecases:  []string{"FLAG_CHAT"},
		})
		cfg := &config.ModelConfig{Backend: "cloud-proxy", KnownUsecases: &chat}
		cfg.PII.Enabled = bp(false)
		enabled, dets := app.ResolvePIIPolicy(cfg)
		Expect(enabled).To(BeFalse())
		Expect(dets).To(BeNil())
	})

	It("enables a cloud-proxy model with the global default detector (closes the no-op gap)", func() {
		// cloud-proxy defaults PIIIsEnabled()==true but lists no detectors, so
		// without a global default it scans with nothing.
		app := mk(&config.ApplicationConfig{PIIDefaultDetectors: []string{"pf"}})
		cfg := &config.ModelConfig{Backend: "cloud-proxy"}
		enabled, dets := app.ResolvePIIPolicy(cfg)
		Expect(enabled).To(BeTrue())
		Expect(dets).To(Equal([]string{"pf"}))
	})

	It("enables a model whose usecase is in the global default-on set", func() {
		app := mk(&config.ApplicationConfig{
			PIIDefaultDetectors: []string{"pf"},
			PIIDefaultUsecases:  []string{"FLAG_CHAT"},
		})
		cfg := &config.ModelConfig{Backend: "llama-cpp", KnownUsecases: &chat}
		enabled, dets := app.ResolvePIIPolicy(cfg)
		Expect(enabled).To(BeTrue())
		Expect(dets).To(Equal([]string{"pf"}))
	})

	It("leaves a model disabled when its usecase is not in the default-on set", func() {
		app := mk(&config.ApplicationConfig{PIIDefaultUsecases: []string{"FLAG_EMBEDDINGS"}})
		cfg := &config.ModelConfig{Backend: "llama-cpp", KnownUsecases: &chat}
		enabled, _ := app.ResolvePIIPolicy(cfg)
		Expect(enabled).To(BeFalse())
	})

	It("prefers the model's own detectors over the global default", func() {
		app := mk(&config.ApplicationConfig{PIIDefaultDetectors: []string{"global-pf"}})
		cfg := &config.ModelConfig{Backend: "cloud-proxy"}
		cfg.PII.Detectors = []string{"own-pf"}
		enabled, dets := app.ResolvePIIPolicy(cfg)
		Expect(enabled).To(BeTrue())
		Expect(dets).To(Equal([]string{"own-pf"}))
	})
})
