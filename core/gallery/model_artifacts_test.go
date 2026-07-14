package gallery_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

type fakeArtifactMaterializer struct {
	result modelartifacts.Result
	err    error
	seen   []modelartifacts.Spec
}

func (f *fakeArtifactMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	f.seen = append(f.seen, spec)
	return f.result, f.err
}

var _ = Describe("gallery artifact installation", func() {
	It("persists resolved state only after materialization succeeds", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		resolved := modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		fake := &fakeArtifactMaterializer{result: modelartifacts.Result{
			Spec:         resolved,
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
		}}
		definition := &gallery.ModelConfig{Name: "managed", ConfigFile: `
backend: transformers
artifacts:
  - name: model
    target: model
    source:
      type: huggingface
      repo: owner/repo
parameters:
  model: owner/repo
unknown_extension:
  keep: true
`}

		installed, err := gallery.InstallModel(context.Background(), state, "managed", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.seen).To(HaveLen(1))
		Expect(installed.Model).To(Equal("owner/repo"))
		Expect(installed.ModelFileName()).To(Equal(fake.result.RelativePath))

		data, err := os.ReadFile(filepath.Join(modelsPath, "managed.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var persisted map[string]any
		Expect(yaml.Unmarshal(data, &persisted)).To(Succeed())
		Expect(persisted).To(HaveKey("unknown_extension"))
		parameters := persisted["parameters"].(map[string]any)
		Expect(parameters["model"]).To(Equal("owner/repo"))
		artifacts := persisted["artifacts"].([]any)
		resolvedMap := artifacts[0].(map[string]any)["resolved"].(map[string]any)
		Expect(resolvedMap["revision"]).To(Equal(resolved.Resolved.Revision))
	})

	It("does not replace an existing config when materialization fails", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		configPath := filepath.Join(modelsPath, "managed.yaml")
		Expect(os.WriteFile(configPath, []byte("name: old\n"), 0644)).To(Succeed())
		fake := &fakeArtifactMaterializer{err: errors.New("acquisition failed")}
		definition := &gallery.ModelConfig{Name: "managed", ConfigFile: `
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`}
		_, err = gallery.InstallModel(context.Background(), state, "managed", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).To(MatchError(ContainSubstring("acquisition failed")))
		Expect(os.ReadFile(configPath)).To(Equal([]byte("name: old\n")))
	})

	It("does not replace an existing config when a legacy file download fails", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		configPath := filepath.Join(modelsPath, "legacy.yaml")
		Expect(os.WriteFile(configPath, []byte("name: old\n"), 0644)).To(Succeed())
		definition := &gallery.ModelConfig{
			Name:       "legacy",
			ConfigFile: "parameters: {model: owner/legacy}\n",
			Files: []gallery.File{{
				Filename: "missing.bin",
				URI:      "file:///definitely-not-a-localai-test-file",
			}},
		}

		_, err = gallery.InstallModel(context.Background(), state, "legacy", definition, nil, nil, false)
		Expect(err).To(HaveOccurred())
		Expect(os.ReadFile(configPath)).To(Equal([]byte("name: old\n")))
	})

	It("blocks an explicitly unsafe repository before materialization", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/api/models/owner/repo/scan"))
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"hasUnsafeFile":true}`))
			Expect(err).NotTo(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		originalEndpoint := downloader.HF_ENDPOINT
		downloader.HF_ENDPOINT = server.URL
		DeferCleanup(func() { downloader.HF_ENDPOINT = originalEndpoint })

		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeArtifactMaterializer{err: errors.New("unsafe install must not materialize")}
		definition := &gallery.ModelConfig{Name: "managed", ConfigFile: `
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`}

		_, err = gallery.InstallModel(context.Background(), state, "managed", definition, nil, nil, true,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).To(MatchError(downloader.ErrUnsafeFilesFound))
		Expect(fake.seen).To(BeEmpty())
	})

	It("leaves a legacy file-only definition on the existing path", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		fake := &fakeArtifactMaterializer{err: errors.New("legacy install must not materialize")}
		definition := &gallery.ModelConfig{Name: "legacy", ConfigFile: `
backend: transformers
parameters:
  model: owner/legacy
`}
		installed, err := gallery.InstallModel(
			context.Background(), state, "legacy", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.seen).To(BeEmpty())
		Expect(installed.Model).To(Equal("owner/legacy"))
		Expect(filepath.Join(modelsPath, "legacy.yaml")).To(BeAnExistingFile())
	})

	It("passes the controller materializer through named gallery installs", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		definitionPath := filepath.Join(modelsPath, "definition.yaml")
		definition, err := yaml.Marshal(gallery.ModelConfig{Name: "managed", ConfigFile: `
backend: transformers
artifacts:
  - source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(definitionPath, definition, 0644)).To(Succeed())
		galleryPath := filepath.Join(modelsPath, "index.yaml")
		index, err := yaml.Marshal([]gallery.GalleryModel{{Metadata: gallery.Metadata{
			Name: "managed",
			URL:  "file://" + definitionPath,
		}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, index, 0644)).To(Succeed())
		galleries := []config.Gallery{{Name: "test", URL: "file://" + galleryPath}}
		fake := &fakeArtifactMaterializer{result: modelartifacts.Result{Spec: modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}}}

		Expect(gallery.InstallModelFromGallery(
			context.Background(), galleries, nil, state, nil, "test@managed",
			gallery.GalleryModel{}, nil, false, false, false,
			gallery.WithArtifactMaterializer(fake),
		)).To(Succeed())
		Expect(fake.seen).To(HaveLen(1))
	})
})
