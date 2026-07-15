package config

import (
	"strings"

	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func poolingTestConfig(pooling string, halfLife int, options ...string) *ModelConfig {
	return &ModelConfig{
		Name: "pool-test",
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest:     schema.BasicModelRequest{Model: "foo.gguf"},
			Pooling:               pooling,
			PoolingHalfLifeTokens: halfLife,
		},
		Options: options,
	}
}

var _ = Describe("Go-side pooling model config", func() {
	Describe("SetDefaults", func() {
		It("auto-appends pooling:none for a Go-side scheme with no explicit pooling option", func() {
			cfg := poolingTestConfig(PoolingDecayedMean, 0)
			cfg.SetDefaults()
			Expect(cfg.Options).To(ContainElement("pooling:none"))
		})

		It("leaves an explicit pooling option alone", func() {
			cfg := poolingTestConfig(PoolingMean, 0, "pooling:none")
			cfg.SetDefaults()
			// Other defaults (e.g. hardware-driven options) may append
			// unrelated entries; the pooling option must stay single.
			poolingOptions := []string{}
			for _, o := range cfg.Options {
				if strings.HasPrefix(o, "pooling:") {
					poolingOptions = append(poolingOptions, o)
				}
			}
			Expect(poolingOptions).To(Equal([]string{"pooling:none"}))
		})

		It("does not touch options when pooling is delegated to the backend", func() {
			for _, scheme := range []string{"", PoolingBackend} {
				cfg := poolingTestConfig(scheme, 0)
				cfg.SetDefaults()
				Expect(cfg.Options).ToNot(ContainElement("pooling:none"))
			}
		})
	})

	Describe("Validate", func() {
		It("accepts every known scheme", func() {
			for _, scheme := range []string{"", PoolingBackend, PoolingMean, PoolingLast, PoolingDecayedMean} {
				cfg := poolingTestConfig(scheme, 0)
				valid, err := cfg.Validate()
				Expect(err).ToNot(HaveOccurred(), "scheme %q", scheme)
				Expect(valid).To(BeTrue(), "scheme %q", scheme)
			}
		})

		It("rejects unknown schemes", func() {
			cfg := poolingTestConfig("sideways", 0)
			valid, err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(err.Error()).To(ContainSubstring("pooling"))
		})

		It("rejects a negative half-life", func() {
			cfg := poolingTestConfig(PoolingDecayedMean, -1)
			valid, err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(err.Error()).To(ContainSubstring("pooling_half_life_tokens"))
		})

		It("rejects a half-life on non-decayed schemes", func() {
			cfg := poolingTestConfig(PoolingMean, 256)
			valid, err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(err.Error()).To(ContainSubstring("decayed_mean"))
		})

		It("accepts a half-life on decayed_mean", func() {
			cfg := poolingTestConfig(PoolingDecayedMean, 256)
			valid, err := cfg.Validate()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeTrue())
		})

		It("rejects Go-side pooling combined with a non-none backend pooling option", func() {
			cfg := poolingTestConfig(PoolingMean, 0, "pooling:mean")
			valid, err := cfg.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(err.Error()).To(ContainSubstring("pooling:none"))
		})

		It("accepts Go-side pooling with an explicit pooling:none option", func() {
			cfg := poolingTestConfig(PoolingLast, 0, "pooling:none")
			valid, err := cfg.Validate()
			Expect(err).ToNot(HaveOccurred())
			Expect(valid).To(BeTrue())
		})
	})
})
