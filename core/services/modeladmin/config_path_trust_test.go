package modeladmin

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A model config can be loaded from outside the models directory (for
// example with --config-file). The admin mutations write the config file
// back, so they must refuse a file outside the models directory rather than
// write wherever the loader found it.
var _ = Describe("ConfigService config file containment", func() {
	var (
		svc     *ConfigService
		ctx     context.Context
		outside string
		orig    []byte
	)

	BeforeEach(func() {
		svc, _ = newTestService()
		ctx = context.Background()
		outside = filepath.Join(GinkgoT().TempDir(), "external.yaml")
		orig = []byte("name: external\nbackend: llama-cpp\n")
		Expect(os.WriteFile(outside, orig, 0o644)).To(Succeed())
		Expect(svc.Loader.ReadModelConfig(outside, svc.AppConfig.ToConfigLoaderOptions()...)).To(Succeed())
		cfg, ok := svc.Loader.GetModelConfig("external")
		Expect(ok).To(BeTrue())
		Expect(cfg.GetModelConfigFile()).To(Equal(outside))
	})

	It("refuses to pin a model whose config file is outside the models directory", func() {
		_, err := svc.TogglePinned(ctx, "external", ActionPin, nil)
		Expect(err).To(MatchError(ErrPathNotTrusted))
		Expect(os.ReadFile(outside)).To(Equal(orig))
	})

	It("refuses to toggle the state of such a model", func() {
		_, err := svc.ToggleState(ctx, "external", ActionDisable)
		Expect(err).To(MatchError(ErrPathNotTrusted))
		Expect(os.ReadFile(outside)).To(Equal(orig))
	})

	It("refuses to patch such a model", func() {
		_, err := svc.PatchConfig(ctx, "external", map[string]any{"context_size": 4096})
		Expect(err).To(MatchError(ErrPathNotTrusted))
		Expect(os.ReadFile(outside)).To(Equal(orig))
	})

	It("refuses to read such a model's config", func() {
		_, err := svc.GetConfig(ctx, "external")
		Expect(err).To(MatchError(ErrPathNotTrusted))
	})
})
