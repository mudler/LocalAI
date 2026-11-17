package galleryop

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("deleting a model whose files cannot all be removed", func() {
	It("keeps the model deleted and reports the leftover files", func() {
		dir := GinkgoT().TempDir()
		state := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig := &config.ApplicationConfig{SystemState: state}
		stuck := filepath.Join(dir, "m", "w.gguf")
		Expect(os.MkdirAll(stuck, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(stuck, "keep"), []byte("x"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "m.yaml"), []byte("name: m\nbackend: llama-cpp\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, gallery.GalleryFileName("m")), []byte("files:\n  - filename: m/w.gguf\n"), 0o644)).To(Succeed())

		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPath(dir)).To(Succeed())
		service := NewGalleryService(appConfig, nil)
		service.SetModelManager(&realDeletingManager{state: state})
		op := &ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID: "d", GalleryElementName: "m", Delete: true, Context: context.Background(),
		}

		err := service.modelHandler(op, loader, state)

		Expect(err).To(HaveOccurred(), "the leftover files must be reported")
		Expect(filepath.Join(dir, "m.yaml")).ToNot(BeAnExistingFile(), "the deletion must not be rolled back")
		_, ok := loader.GetModelConfig("m")
		Expect(ok).To(BeFalse())
	})
})
