package galleryop_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

type galleryOpArtifactMaterializer struct {
	seen []modelartifacts.Spec
}

func (f *galleryOpArtifactMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	f.seen = append(f.seen, spec)
	spec.Resolved = &modelartifacts.Resolved{
		Endpoint: "https://huggingface.co",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	return modelartifacts.Result{Spec: spec}, nil
}

var _ = Describe("local model manager artifact materializer", func() {
	It("passes the controller materializer to direct gallery installs", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		materializer := &galleryOpArtifactMaterializer{}
		manager := galleryop.NewLocalModelManager(&config.ApplicationConfig{
			SystemState:               state,
			ModelArtifactMaterializer: materializer,
		}, nil)
		definition := &gallery.ModelConfig{Name: "managed", ConfigFile: `
backend: transformers
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`}
		op := &galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			GalleryElement: definition,
		}
		Expect(manager.InstallModel(context.Background(), op, nil)).To(Succeed())
		Expect(materializer.seen).To(HaveLen(1))
	})
})
