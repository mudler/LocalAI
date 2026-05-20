package modeladmin

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
)

// newTestService stands up a ConfigService backed by a tmp dir so the file IO
// is real but isolated. The model loader is loaded against the same tmp path
// so GetModelConfig works.
func newTestService() (*ConfigService, string) {
	dir := GinkgoT().TempDir()
	loader := config.NewModelConfigLoader(dir)
	appConfig := &config.ApplicationConfig{
		SystemState: &system.SystemState{Model: system.Model{ModelsPath: dir}},
	}
	return NewConfigService(loader, appConfig), dir
}

// writeModelYAML creates a model YAML on disk and reloads the loader so the
// new entry is visible.
func writeModelYAML(svc *ConfigService, dir, name string, body map[string]any) {
	body["name"] = name
	data, err := yaml.Marshal(body)
	Expect(err).ToNot(HaveOccurred())
	path := filepath.Join(dir, name+".yaml")
	Expect(os.WriteFile(path, data, 0644)).To(Succeed())
	Expect(svc.Loader.LoadModelConfigsFromPath(dir, svc.AppConfig.ToConfigLoaderOptions()...)).To(Succeed())
}

var _ = Describe("ConfigService", func() {
	var (
		svc *ConfigService
		dir string
		ctx context.Context
	)

	BeforeEach(func() {
		svc, dir = newTestService()
		ctx = context.Background()
	})

	Describe("GetConfig", func() {
		It("round-trips YAML from disk and exposes the parsed JSON", func() {
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			view, err := svc.GetConfig(ctx, "qwen")
			Expect(err).ToNot(HaveOccurred())
			Expect(view.Name).To(Equal("qwen"))
			Expect(view.JSON).To(HaveKeyWithValue("backend", "llama-cpp"))
		})

		It("returns ErrNotFound for an unknown model", func() {
			_, err := svc.GetConfig(ctx, "missing")
			Expect(err).To(MatchError(ErrNotFound))
		})

		It("returns ErrNameRequired when name is empty", func() {
			_, err := svc.GetConfig(ctx, "")
			Expect(err).To(MatchError(ErrNameRequired))
		})
	})

	Describe("PatchConfig", func() {
		It("deep-merges the patch and preserves untouched siblings", func() {
			writeModelYAML(svc, dir, "qwen", map[string]any{
				"backend":      "llama-cpp",
				"context_size": 4096,
				"parameters":   map[string]any{"temperature": 0.7, "top_p": 0.9},
			})

			updated, err := svc.PatchConfig(ctx, "qwen", map[string]any{
				"context_size": 8192,
				"parameters":   map[string]any{"temperature": 0.5},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.Name).To(Equal("qwen"))

			raw, err := os.ReadFile(filepath.Join(dir, "qwen.yaml"))
			Expect(err).ToNot(HaveOccurred())
			var got map[string]any
			Expect(yaml.Unmarshal(raw, &got)).To(Succeed())
			Expect(got).To(HaveKeyWithValue("context_size", 8192))

			params, ok := got["parameters"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(params).To(HaveKeyWithValue("temperature", 0.5))
			// top_p must still be there: deep-merge should NOT clobber siblings.
			Expect(params).To(HaveKeyWithValue("top_p", 0.9))
		})

		It("returns ErrNotFound for an unknown model", func() {
			_, err := svc.PatchConfig(ctx, "ghost", map[string]any{"x": 1})
			Expect(err).To(MatchError(ErrNotFound))
		})

		It("rejects an empty patch with ErrEmptyBody", func() {
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp"})
			_, err := svc.PatchConfig(ctx, "qwen", map[string]any{})
			Expect(err).To(MatchError(ErrEmptyBody))
		})
	})

	Describe("EditYAML", func() {
		It("renames the on-disk file and reindexes the loader", func() {
			writeModelYAML(svc, dir, "old-name", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: new-name\nbackend: llama-cpp\n")
			result, err := svc.EditYAML(ctx, "old-name", body, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Renamed).To(BeTrue())
			Expect(result.OldName).To(Equal("old-name"))
			Expect(result.NewName).To(Equal("new-name"))

			_, err = os.Stat(filepath.Join(dir, "old-name.yaml"))
			Expect(os.IsNotExist(err)).To(BeTrue(), "old YAML should be removed")
			_, err = os.Stat(filepath.Join(dir, "new-name.yaml"))
			Expect(err).ToNot(HaveOccurred(), "new YAML should exist")

			_, ok := svc.Loader.GetModelConfig("new-name")
			Expect(ok).To(BeTrue(), "loader should have the renamed model")
			_, ok = svc.Loader.GetModelConfig("old-name")
			Expect(ok).To(BeFalse(), "loader should not retain the old name")
		})

		It("refuses a rename that would clobber an existing model", func() {
			writeModelYAML(svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})
			writeModelYAML(svc, dir, "beta", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: beta\nbackend: llama-cpp\n")
			_, err := svc.EditYAML(ctx, "alpha", body, nil)
			Expect(err).To(MatchError(ErrConflict))
		})

		It("rejects path-separator characters in the new name", func() {
			writeModelYAML(svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: ../escape\nbackend: llama-cpp\n")
			_, err := svc.EditYAML(ctx, "alpha", body, nil)
			Expect(err).To(MatchError(ErrPathSeparator))
		})

		It("returns ErrEmptyBody when the body is nil", func() {
			writeModelYAML(svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})
			_, err := svc.EditYAML(ctx, "alpha", nil, nil)
			Expect(err).To(MatchError(ErrEmptyBody))
		})
	})
})
