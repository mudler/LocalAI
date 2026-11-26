package modeladmin

import (
	"errors"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("runtimeOptions", func() {
	intp := func(n int) *int { return &n }

	BeforeEach(func() {
		// The host default of the batch depends on the GPU; turn the
		// hardware defaults off so the default is the conservative one.
		GinkgoT().Setenv("LOCALAI_DISABLE_HARDWARE_DEFAULTS", "true")
	})

	It("uses f16 and the default batch for a model with nothing set", func() {
		got, err := runtimeOptions(VRAMRequest{}, config.ModelConfig{}, 8192)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.ContextSize).To(Equal(uint32(8192)))
		Expect(got.CacheTypeK).To(Equal("f16"))
		Expect(got.CacheTypeV).To(Equal("f16"))
		Expect(got.UBatch).To(Equal(uint32(config.DefaultPhysicalBatch)))
	})

	It("reads the model's cache types, batch and ubatch option", func() {
		cfg := config.ModelConfig{Options: []string{"use_jinja:true", "n_ubatch:256"}}
		cfg.CacheTypeK, cfg.CacheTypeV, cfg.Batch = "q8_0", "q4_0", 2048
		got, err := runtimeOptions(VRAMRequest{}, cfg, 8192)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.CacheTypeK).To(Equal("q8_0"))
		Expect(got.CacheTypeV).To(Equal("q4_0"))
		Expect(got.UBatch).To(Equal(uint32(256)))
	})

	It("lets the request replace the model's values, for an unsaved draft", func() {
		cfg := config.ModelConfig{Options: []string{"ubatch:128"}}
		cfg.CacheTypeK, cfg.Batch = "q8_0", 2048
		got, err := runtimeOptions(VRAMRequest{CacheTypeK: "f16", Batch: intp(0), UBatch: intp(0)}, cfg, 8192)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.CacheTypeK).To(Equal("f16"))
		// batch 0 is the host default; ubatch 0 follows the batch.
		Expect(got.UBatch).To(Equal(uint32(config.DefaultPhysicalBatch)))
	})

	It("caps the physical batch at the logical batch", func() {
		got, err := runtimeOptions(VRAMRequest{Batch: intp(1024), UBatch: intp(4096)}, config.ModelConfig{}, 8192)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.UBatch).To(Equal(uint32(1024)))
	})

	It("refuses an unknown cache type and a negative batch", func() {
		_, err := runtimeOptions(VRAMRequest{CacheTypeV: "q3_k"}, config.ModelConfig{}, 8192)
		Expect(errors.Is(err, ErrInvalidEstimate)).To(BeTrue())
		_, err = runtimeOptions(VRAMRequest{Batch: intp(-1)}, config.ModelConfig{}, 8192)
		Expect(errors.Is(err, ErrInvalidEstimate)).To(BeTrue())
		_, err = runtimeOptions(VRAMRequest{UBatch: intp(-1)}, config.ModelConfig{}, 8192)
		Expect(errors.Is(err, ErrInvalidEstimate)).To(BeTrue())
	})
})
