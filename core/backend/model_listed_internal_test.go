package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func loaderWithConfigs(dir string, n int) *config.ModelConfigLoader {
	for i := range n {
		body := fmt.Sprintf("name: model-%d\nbackend: llama-cpp\nparameters:\n  model: model-%d.gguf\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("model-%d.yaml", i)), []byte(body), 0o644); err != nil {
			panic(err)
		}
	}
	cl := config.NewModelConfigLoader(dir)
	if err := cl.LoadModelConfigsFromPath(dir); err != nil {
		panic(err)
	}
	return cl
}

var _ = Describe("modelListed", func() {
	It("answers as the model list does", func() {
		dir := GinkgoT().TempDir()
		cl := loaderWithConfigs(dir, 20)
		loader := model.NewModelLoader(&system.SystemState{})
		names, err := galleryop.ListModels(cl, loader, nil, galleryop.SKIP_ALWAYS)
		Expect(err).ToNot(HaveOccurred())
		for _, name := range []string{"model-0", "model-7", "model-19", "model-20", "missing", "model-7.gguf", ""} {
			listed, err := modelListed(cl, loader, name)
			Expect(err).ToNot(HaveOccurred())
			Expect(listed).To(Equal(slices.Contains(names, name)), name)
		}
	})
})

// BenchmarkModelListed checks whether a configured model is listed, as
// ModelInference does for every request with gallery autoload on, on a host
// with 100 model configs.
func BenchmarkModelListed(b *testing.B) {
	cl := loaderWithConfigs(b.TempDir(), 100)
	loader := model.NewModelLoader(&system.SystemState{})
	b.Run("list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			names, _ := galleryop.ListModels(cl, loader, nil, galleryop.SKIP_ALWAYS)
			_ = slices.Contains(names, "model-42")
		}
	})
	b.Run("lookup", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = modelListed(cl, loader, "model-42")
		}
	})
}
