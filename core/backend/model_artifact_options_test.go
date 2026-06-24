package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("managed model runtime options", func() {
	It("estimates weights from the committed manifest without repository fallback", func() {
		const cacheKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		modelsPath := GinkgoT().TempDir()
		spec := modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: cacheKey,
			},
		}
		relative, err := modelartifacts.RelativeSnapshotPath(cacheKey)
		Expect(err).NotTo(HaveOccurred())
		manifest := modelartifacts.Manifest{
			Version:  modelartifacts.ManifestVersion,
			Artifact: spec,
			Files: []modelartifacts.ManifestFile{{
				Path: "weights/model.safetensors", Size: 12, SHA256: strings.Repeat("a", 64),
			}},
		}
		encoded, err := json.Marshal(manifest)
		Expect(err).NotTo(HaveOccurred())
		manifestPath := filepath.Join(modelsPath, filepath.Dir(relative), "manifest.json")
		Expect(os.MkdirAll(filepath.Dir(manifestPath), 0750)).To(Succeed())
		Expect(os.WriteFile(manifestPath, encoded, 0644)).To(Succeed())

		cfg := config.ModelConfig{Artifacts: []modelartifacts.Spec{spec}}
		Expect(estimateModelSizeBytes(cfg, modelsPath)).To(Equal(int64(12)))
	})
})
