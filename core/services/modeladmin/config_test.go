package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

type failingConfigMaterializer struct{ err error }

func (f *failingConfigMaterializer) Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error) {
	return modelartifacts.Result{}, f.err
}

type fakeRevisionLifecycle struct {
	calls   []revisionLifecycleCall
	batches [][]ModelRevisionTransition
	pending int
	err     error
}

type revisionLifecycleCall struct {
	oldName, newName, revision string
	disabled                   bool
}

func (f *fakeRevisionLifecycle) ApplyConfigRevisions(_ context.Context, transitions []ModelRevisionTransition) (int, error) {
	f.batches = append(f.batches, append([]ModelRevisionTransition(nil), transitions...))
	for _, transition := range transitions {
		f.calls = append(f.calls, revisionLifecycleCall{
			oldName: transition.ModelName, newName: transition.ModelName,
			revision: transition.ConfigRevision, disabled: transition.Disabled,
		})
	}
	return f.pending, f.err
}

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

	It("rejects a symlink when snapshotting a mutation", func() {
		target := filepath.Join(dir, "target.yaml")
		Expect(os.WriteFile(target, []byte("name: target\n"), 0o600)).To(Succeed())
		link := filepath.Join(dir, "link.yaml")
		Expect(os.Symlink(target, link)).To(Succeed())

		called := false
		Expect(svc.withMutationRollback([]string{link}, func() error {
			called = true
			return nil
		})).ToNot(Succeed())
		Expect(called).To(BeFalse())
	})

	It("removes a symlink created at a previously absent rollback destination", func() {
		target := filepath.Join(dir, "target.yaml")
		Expect(os.WriteFile(target, []byte("unchanged"), 0o600)).To(Succeed())
		destination := filepath.Join(dir, "new.yaml")
		mutationErr := errors.New("mutation failed")

		err := svc.withMutationRollback([]string{destination}, func() error {
			Expect(os.Symlink(target, destination)).To(Succeed())
			return mutationErr
		})
		Expect(err).To(MatchError(mutationErr))
		_, statErr := os.Lstat(destination)
		Expect(statErr).To(MatchError(os.ErrNotExist))
		data, readErr := os.ReadFile(target)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(data).To(Equal([]byte("unchanged")))
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
		It("rejects a patched name change before mutating any state", func() {
			lifecycle := &fakeRevisionLifecycle{}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})
			path := filepath.Join(dir, "qwen.yaml")
			before, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())

			_, err = svc.PatchConfig(ctx, "qwen", map[string]any{"name": "renamed", "context_size": 8192})
			Expect(err).To(MatchError(ContainSubstring("cannot rename")))
			Expect(errors.Is(err, ErrInvalidConfig)).To(BeTrue())
			after, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(after).To(Equal(before))
			Expect(filepath.Join(dir, "renamed.yaml")).NotTo(BeAnExistingFile())
			loaded, ok := svc.Loader.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(loaded.ContextSize).To(HaveValue(Equal(4096)))
			_, renamed := svc.Loader.GetModelConfig("renamed")
			Expect(renamed).To(BeFalse())
			Expect(lifecycle.calls).To(BeEmpty())
		})

		It("accepts an omitted or unchanged patched name", func() {
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			withoutName, err := svc.PatchConfig(ctx, "qwen", map[string]any{"context_size": 8192})
			Expect(err).ToNot(HaveOccurred())
			Expect(withoutName.Name).To(Equal("qwen"))
			withSameName, err := svc.PatchConfig(ctx, "qwen", map[string]any{"name": "qwen", "context_size": 10000})
			Expect(err).ToNot(HaveOccurred())
			Expect(withSameName.Name).To(Equal("qwen"))
		})

		It("serializes lifecycle publication across local service instances", func() {
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})
			lifecycle := newBlockingRevisionLifecycle()
			first := NewConfigService(svc.Loader, svc.AppConfig, lifecycle)
			second := NewConfigService(svc.Loader, svc.AppConfig, lifecycle)
			firstDone := make(chan error, 1)
			secondDone := make(chan error, 1)

			go func() {
				_, err := first.PatchConfig(ctx, "qwen", map[string]any{"context_size": 8192})
				firstDone <- err
			}()
			Eventually(lifecycle.entered).Should(Receive())
			go func() {
				_, err := second.PatchConfig(ctx, "qwen", map[string]any{"context_size": 10000})
				secondDone <- err
			}()
			Consistently(secondDone).ShouldNot(Receive())
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 8192))

			close(lifecycle.release)
			Eventually(firstDone).Should(Receive(Succeed()))
			Eventually(secondDone).Should(Receive(Succeed()))
			loaded, ok := svc.Loader.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(loaded.ContextSize).To(HaveValue(Equal(10000)))
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 10000))
		})

		It("restores disk and loader when revision publication fails", func() {
			svc.Lifecycle = &fakeRevisionLifecycle{err: errors.New("registry unavailable")}
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			_, err := svc.PatchConfig(ctx, "qwen", map[string]any{"context_size": 8192})
			Expect(err).To(MatchError(ContainSubstring("registry unavailable")))
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 4096))
			loaded, ok := svc.Loader.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(loaded.ContextSize).To(HaveValue(Equal(4096)))
			restarted := config.NewModelConfigLoader(dir)
			Expect(restarted.LoadModelConfigsFromPath(dir)).To(Succeed())
			reloaded, ok := restarted.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(reloaded.ContextSize).To(HaveValue(Equal(4096)))
		})
		It("applies the persisted semantic revision and reports pending cleanup", func() {
			lifecycle := &fakeRevisionLifecycle{pending: 2}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			updated, err := svc.PatchConfig(ctx, "qwen", map[string]any{"context_size": 8192})
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.ConfigRevision).ToNot(BeEmpty())
			Expect(updated.PendingCleanup).To(Equal(2))
			Expect(lifecycle.calls).To(ConsistOf(revisionLifecycleCall{
				oldName: "qwen", newName: "qwen", revision: updated.ConfigRevision,
			}))
		})

		It("keeps a durable patch successful when cleanup remains pending", func() {
			lifecycle := &fakeRevisionLifecycle{pending: 1}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			updated, err := svc.PatchConfig(ctx, "qwen", map[string]any{"context_size": 8192})
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.PendingCleanup).To(Equal(1))
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 8192))
		})
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

		It("replaces a map field wholesale so deleted entries do not survive", func() {
			// A detector model with a populated entity_actions map. The editor
			// removes SSN and re-sends the remaining map; a naive deep-merge
			// would re-add SSN (it only adds/overrides keys, never deletes).
			writeModelYAML(svc, dir, "ner", map[string]any{
				"backend":        "llama-cpp",
				"known_usecases": []any{"token_classify"},
				"pii_detection": map[string]any{
					"default_action": "mask",
					"entity_actions": map[string]any{"SSN": "block", "EMAIL": "mask"},
				},
			})

			_, err := svc.PatchConfig(ctx, "ner", map[string]any{
				"pii_detection": map[string]any{
					"default_action": "mask",
					"entity_actions": map[string]any{"EMAIL": "mask"},
				},
			})
			Expect(err).ToNot(HaveOccurred())

			raw, err := os.ReadFile(filepath.Join(dir, "ner.yaml"))
			Expect(err).ToNot(HaveOccurred())
			var got map[string]any
			Expect(yaml.Unmarshal(raw, &got)).To(Succeed())
			pii := got["pii_detection"].(map[string]any)
			ea := pii["entity_actions"].(map[string]any)
			Expect(ea).To(HaveKeyWithValue("EMAIL", "mask"))
			Expect(ea).NotTo(HaveKey("SSN"), "deleted map entry must not survive the patch")
			// The scalar sibling in the same nested block is still preserved.
			Expect(pii).To(HaveKeyWithValue("default_action", "mask"))
		})

		It("drops a map field entirely when the patch empties it", func() {
			writeModelYAML(svc, dir, "ner", map[string]any{
				"backend":        "llama-cpp",
				"known_usecases": []any{"token_classify"},
				"pii_detection": map[string]any{
					"default_action": "mask",
					"entity_actions": map[string]any{"SSN": "block"},
				},
			})

			_, err := svc.PatchConfig(ctx, "ner", map[string]any{
				"pii_detection": map[string]any{
					"entity_actions": map[string]any{},
				},
			})
			Expect(err).ToNot(HaveOccurred())

			raw, err := os.ReadFile(filepath.Join(dir, "ner.yaml"))
			Expect(err).ToNot(HaveOccurred())
			var got map[string]any
			Expect(yaml.Unmarshal(raw, &got)).To(Succeed())
			pii := got["pii_detection"].(map[string]any)
			Expect(pii).NotTo(HaveKey("entity_actions"))
		})
	})

	Describe("EditYAML", func() {
		It("does not publish an in-place revision when preload preparation fails", func() {
			materializer := &failingConfigMaterializer{err: errors.New("artifact unavailable")}
			svc.Loader = config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
			lifecycle := &fakeRevisionLifecycle{}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			body := []byte("name: qwen\nbackend: llama-cpp\ncontext_size: 8192\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n")
			_, err := svc.EditYAML(ctx, "qwen", body)
			Expect(err).To(MatchError(ContainSubstring("artifact unavailable")))
			Expect(lifecycle.calls).To(BeEmpty())
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 4096))
			loaded, ok := svc.Loader.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(loaded.ContextSize).To(HaveValue(Equal(4096)))
			restarted := config.NewModelConfigLoader(dir)
			Expect(restarted.LoadModelConfigsFromPath(dir)).To(Succeed())
			reloaded, ok := restarted.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(reloaded.ContextSize).To(HaveValue(Equal(4096)))
		})

		It("does not publish a rename revision when preload preparation fails", func() {
			materializer := &failingConfigMaterializer{err: errors.New("artifact unavailable")}
			svc.Loader = config.NewModelConfigLoader(dir, config.WithArtifactMaterializer(materializer))
			lifecycle := &fakeRevisionLifecycle{}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "old", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			body := []byte("name: new\nbackend: llama-cpp\ncontext_size: 8192\nartifacts:\n  - name: model\n    target: model\n    source: {type: huggingface, repo: owner/repo}\n")
			_, err := svc.EditYAML(ctx, "old", body)
			Expect(err).To(MatchError(ContainSubstring("artifact unavailable")))
			Expect(lifecycle.calls).To(BeEmpty())
			Expect(filepath.Join(dir, "old.yaml")).To(BeAnExistingFile())
			Expect(filepath.Join(dir, "new.yaml")).NotTo(BeAnExistingFile())
			_, oldOK := svc.Loader.GetModelConfig("old")
			_, newOK := svc.Loader.GetModelConfig("new")
			Expect(oldOK).To(BeTrue())
			Expect(newOK).To(BeFalse())
			restarted := config.NewModelConfigLoader(dir)
			Expect(restarted.LoadModelConfigsFromPath(dir)).To(Succeed())
			_, oldOK = restarted.GetModelConfig("old")
			_, newOK = restarted.GetModelConfig("new")
			Expect(oldOK).To(BeTrue())
			Expect(newOK).To(BeFalse())
		})

		It("restores an in-place edit when revision publication fails", func() {
			svc.Lifecycle = &fakeRevisionLifecycle{err: errors.New("registry unavailable")}
			writeModelYAML(svc, dir, "qwen", map[string]any{"backend": "llama-cpp", "context_size": 4096})

			_, err := svc.EditYAML(ctx, "qwen", []byte("name: qwen\nbackend: llama-cpp\ncontext_size: 8192\n"))
			Expect(err).To(MatchError(ContainSubstring("registry unavailable")))
			Expect(readMap(filepath.Join(dir, "qwen.yaml"))).To(HaveKeyWithValue("context_size", 4096))
			loaded, ok := svc.Loader.GetModelConfig("qwen")
			Expect(ok).To(BeTrue())
			Expect(loaded.ContextSize).To(HaveValue(Equal(4096)))
		})

		It("restores both identities and gallery metadata when rename publication fails", func() {
			svc.Lifecycle = &fakeRevisionLifecycle{err: errors.New("registry unavailable")}
			writeModelYAML(svc, dir, "old", map[string]any{"backend": "llama-cpp", "context_size": 4096})
			oldGallery := filepath.Join(dir, gallery.GalleryFileName("old"))
			Expect(os.WriteFile(oldGallery, []byte("metadata"), 0644)).To(Succeed())

			_, err := svc.EditYAML(ctx, "old", []byte("name: new\nbackend: llama-cpp\ncontext_size: 8192\n"))
			Expect(err).To(MatchError(ContainSubstring("registry unavailable")))
			Expect(filepath.Join(dir, "old.yaml")).To(BeAnExistingFile())
			Expect(filepath.Join(dir, "new.yaml")).NotTo(BeAnExistingFile())
			Expect(oldGallery).To(BeAnExistingFile())
			Expect(filepath.Join(dir, gallery.GalleryFileName("new"))).NotTo(BeAnExistingFile())
			_, oldOK := svc.Loader.GetModelConfig("old")
			_, newOK := svc.Loader.GetModelConfig("new")
			Expect(oldOK).To(BeTrue())
			Expect(newOK).To(BeFalse())
		})
		It("applies both rename identities in one revision lifecycle batch", func() {
			lifecycle := &fakeRevisionLifecycle{pending: 1}
			svc.Lifecycle = lifecycle
			writeModelYAML(svc, dir, "old", map[string]any{"backend": "llama-cpp"})
			body := []byte("name: new\nbackend: llama-cpp\ncontext_size: 8192\n")

			result, err := svc.EditYAML(ctx, "old", body)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.ConfigRevision).ToNot(BeEmpty())
			Expect(result.PendingCleanup).To(Equal(1))
			Expect(lifecycle.batches).To(HaveLen(1))
			Expect(lifecycle.calls).To(Equal([]revisionLifecycleCall{
				{oldName: "old", newName: "old", revision: DeletedModelConfigRevision("old"), disabled: true},
				{oldName: "new", newName: "new", revision: result.ConfigRevision},
			}))
		})
		It("renames the on-disk file and reindexes the loader", func() {
			writeModelYAML(svc, dir, "old-name", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: new-name\nbackend: llama-cpp\n")
			result, err := svc.EditYAML(ctx, "old-name", body)
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
			_, err := svc.EditYAML(ctx, "alpha", body)
			Expect(err).To(MatchError(ErrConflict))
		})

		It("rejects path-separator characters in the new name", func() {
			writeModelYAML(svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: ../escape\nbackend: llama-cpp\n")
			_, err := svc.EditYAML(ctx, "alpha", body)
			Expect(err).To(MatchError(ErrPathSeparator))
		})

		It("returns ErrEmptyBody when the body is nil", func() {
			writeModelYAML(svc, dir, "alpha", map[string]any{"backend": "llama-cpp"})
			_, err := svc.EditYAML(ctx, "alpha", nil)
			Expect(err).To(MatchError(ErrEmptyBody))
		})

		It("rejects editing a config into an alias with a missing target", func() {
			writeModelYAML(svc, dir, "base", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: base\nalias: ghost\n")
			_, err := svc.EditYAML(ctx, "base", body)
			Expect(err).To(MatchError(ErrInvalidConfig))
			Expect(err.Error()).To(ContainSubstring("ghost"))
		})

		It("accepts editing a config into an alias with a real target", func() {
			writeModelYAML(svc, dir, "base", map[string]any{"backend": "llama-cpp"})
			writeModelYAML(svc, dir, "target", map[string]any{"backend": "llama-cpp"})

			body := []byte("name: base\nalias: target\n")
			_, err := svc.EditYAML(ctx, "base", body)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
