package galleryop_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// A variant chosen in the UI or over REST arrives here as ManagementOp.Variant.
// If it is not turned into a gallery.WithVariant install option the install
// still succeeds, just on the auto-selected build, so the user's choice
// disappears without a single error anywhere. These specs pin the threading.
var _ = Describe("LocalModelManager variant selection", func() {
	var (
		modelsDir   string
		mgr         *galleryop.LocalModelManager
		appConfig   *config.ApplicationConfig
		systemState *system.SystemState
	)

	// Every entry carries an inline config_file, so the whole install runs off
	// the local filesystem and never reaches the network.
	entry := func(name, backend string, variants ...gallery.Variant) gallery.GalleryModel {
		m := gallery.GalleryModel{ConfigFile: map[string]any{"backend": backend}}
		m.Name = name
		m.Description = "entry " + name
		m.Variants = variants
		return m
	}

	installedBackend := func(name string) string {
		GinkgoHelper()
		dat, err := os.ReadFile(filepath.Join(modelsDir, name+".yaml"))
		Expect(err).ToNot(HaveOccurred())
		content := map[string]any{}
		Expect(yaml.Unmarshal(dat, &content)).To(Succeed())
		return content["backend"].(string)
	}

	install := func(variant string) error {
		op := &galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			GalleryElementName: "qwen3-8b-q4",
			Variant:            variant,
			Galleries:          appConfig.Galleries,
		}
		return mgr.InstallModel(context.Background(), op, func(string, string, string, float64) {})
	}

	BeforeEach(func() {
		var err error
		modelsDir, err = os.MkdirTemp("", "variant-op-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(modelsDir)).To(Succeed()) })

		// "0GiB" always fits, so auto-selection would take the upgrade on any
		// machine. Only an honored pin can land the install on the base.
		entries := []gallery.GalleryModel{
			entry("qwen3-8b-q4", "base-backend", gallery.Variant{Model: "qwen3-8b-q8", MinMemory: "0GiB"}),
			entry("qwen3-8b-q8", "upgrade-backend"),
		}
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		galleryPath := filepath.Join(modelsDir, "gallery.yaml")
		Expect(os.WriteFile(galleryPath, out, 0o600)).To(Succeed())

		systemState, err = system.GetSystemState(system.WithModelPath(modelsDir))
		Expect(err).ToNot(HaveOccurred())
		appConfig = &config.ApplicationConfig{
			SystemState: systemState,
			Galleries:   []config.Gallery{{Name: "test", URL: "file://" + galleryPath}},
		}
		mgr = galleryop.NewLocalModelManager(appConfig, model.NewModelLoader(systemState))
	})

	It("installs the entry's own build when the op pins it", func() {
		Expect(install("qwen3-8b-q4")).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("base-backend"))
	})

	It("auto-selects when the op names no variant", func() {
		// The mirror of the spec above: same gallery, same host, only the
		// pin differs, so nothing but the pin can explain the different build.
		Expect(install("")).To(Succeed())
		Expect(installedBackend("qwen3-8b-q4")).To(Equal("upgrade-backend"))
	})

	It("fails the install when the op names a variant the entry does not declare", func() {
		err := install("qwen3-8b-q2")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
		Expect(err.Error()).To(ContainSubstring("qwen3-8b-q2"))
	})
})
