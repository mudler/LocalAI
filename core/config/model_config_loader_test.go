package config

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type preloadArtifactMaterializer struct {
	result  modelartifacts.Result
	err     error
	seen    chan modelartifacts.Spec
	release <-chan struct{}
}

func (f *preloadArtifactMaterializer) Ensure(ctx context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	if f.seen != nil {
		f.seen <- spec
	}
	if f.release != nil {
		select {
		case <-ctx.Done():
			return modelartifacts.Result{}, ctx.Err()
		case <-f.release:
		}
	}
	return f.result, f.err
}

var _ = Describe("ModelConfigLoader artifact preload", func() {
	It("materializes and persists a source-only artifact binding", func() {
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "managed.yaml")
		Expect(os.WriteFile(configPath, []byte(`
name: managed
backend: transformers
unknown_extension: keep-me
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: owner/repo}
parameters: {model: owner/repo}
`), 0644)).To(Succeed())
		resolved := modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		fake := &preloadArtifactMaterializer{result: modelartifacts.Result{
			Spec:         resolved,
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
		}, seen: make(chan modelartifacts.Spec, 1)}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		loaded, found := loader.GetModelConfig("managed")
		Expect(found).To(BeTrue())
		Expect(loaded.Model).To(Equal("owner/repo"))
		Expect(loaded.ModelFileName()).To(Equal(fake.result.RelativePath))
		data, err := os.ReadFile(configPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("unknown_extension: keep-me"))
		Expect(string(data)).To(ContainSubstring("revision: 0123456789abcdef0123456789abcdef01234567"))
		Expect(string(data)).To(ContainSubstring("cache_key: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
		Expect(string(data)).To(ContainSubstring("model: owner/repo"))
	})

	It("materializes a direct Hugging Face file reference", func() {
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "hf-file.yaml")
		Expect(os.WriteFile(configPath, []byte(`
name: hf-file
backend: transformers
parameters:
  model: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.f16.gguf
`), 0644)).To(Succeed())
		resolved := modelartifacts.Spec{
			Name:   "model",
			Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "nomic-ai/nomic-embed-text-v1.5-GGUF", AllowPatterns: []string{"nomic-embed-text-v1.5.f16.gguf"}, Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		fake := &preloadArtifactMaterializer{result: modelartifacts.Result{
			Spec:         resolved,
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
		}, seen: make(chan modelartifacts.Spec, 1)}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		loaded, found := loader.GetModelConfig("hf-file")
		Expect(found).To(BeTrue())
		Expect(loaded.Model).To(Equal("https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.f16.gguf"))
		Expect(loaded.ModelFileName()).To(Equal(fake.result.RelativePath))
		Expect(fake.seen).To(Receive(SatisfyAll(
			WithTransform(func(spec modelartifacts.Spec) string { return spec.Source.Repo }, Equal("nomic-ai/nomic-embed-text-v1.5-GGUF")),
			WithTransform(func(spec modelartifacts.Spec) []string { return spec.Source.AllowPatterns }, Equal([]string{"nomic-embed-text-v1.5.f16.gguf"})),
		)))
	})

	It("falls back to the legacy path when inferred materialization fails", func() {
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "hf-legacy.yaml")
		Expect(os.WriteFile(configPath, []byte(`
name: hf-legacy
backend: transformers
parameters:
  model: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.f16.gguf
`), 0644)).To(Succeed())
		fake := &preloadArtifactMaterializer{err: context.Canceled, seen: make(chan modelartifacts.Spec, 1)}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		loaded, found := loader.GetModelConfig("hf-legacy")
		Expect(found).To(BeTrue())
		Expect(loaded.Artifacts).To(BeEmpty())
		Expect(loaded.Model).To(Equal("https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.f16.gguf"))
		Expect(fake.seen).To(Receive())
	})

	It("does not hold the loader lock while materialization blocks", func() {
		seen := make(chan modelartifacts.Spec, 1)
		release := make(chan struct{})
		fake := &preloadArtifactMaterializer{seen: seen, release: release}
		loader := NewModelConfigLoader(GinkgoT().TempDir(), WithArtifactMaterializer(fake))
		loader.Lock()
		loader.configs["managed"] = ModelConfig{
			Name: "managed",
			Artifacts: []modelartifacts.Spec{{
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			}},
		}
		loader.Unlock()
		done := make(chan error, 1)
		go func() { done <- loader.PreloadWithContext(context.Background(), loader.modelPath) }()
		<-seen
		lookupDone := make(chan struct{})
		go func() {
			_, _ = loader.GetModelConfig("managed")
			close(lookupDone)
		}()
		Eventually(lookupDone).Should(BeClosed())
		close(release)
		Expect(<-done).NotTo(HaveOccurred())
	})

	It("propagates preload cancellation", func() {
		seen := make(chan modelartifacts.Spec, 1)
		release := make(chan struct{})
		fake := &preloadArtifactMaterializer{seen: seen, release: release}
		loader := NewModelConfigLoader(GinkgoT().TempDir(), WithArtifactMaterializer(fake))
		loader.Lock()
		loader.configs["managed"] = ModelConfig{
			Name: "managed",
			Artifacts: []modelartifacts.Spec{{
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			}},
		}
		loader.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- loader.PreloadWithContext(ctx, loader.modelPath) }()
		<-seen
		cancel()
		Expect(<-done).To(MatchError(context.Canceled))
	})

	It("does not overwrite a config changed during materialization", func() {
		seen := make(chan modelartifacts.Spec, 1)
		release := make(chan struct{})
		fake := &preloadArtifactMaterializer{
			seen: seen, release: release,
			result: modelartifacts.Result{RelativePath: ".artifacts/huggingface/cached/snapshot"},
		}
		loader := NewModelConfigLoader(GinkgoT().TempDir(), WithArtifactMaterializer(fake))
		loader.Lock()
		loader.configs["managed"] = ModelConfig{
			Name: "managed", Description: "before",
			Artifacts: []modelartifacts.Spec{{
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			}},
		}
		loader.Unlock()
		done := make(chan error, 1)
		go func() { done <- loader.PreloadWithContext(context.Background(), loader.modelPath) }()
		<-seen
		loader.UpdateModelConfig("managed", func(cfg *ModelConfig) {
			cfg.Description = "changed concurrently"
		})
		close(release)
		Expect(<-done).NotTo(HaveOccurred())
		loaded, found := loader.GetModelConfig("managed")
		Expect(found).To(BeTrue())
		Expect(loaded.Description).To(Equal("changed concurrently"))
	})
})

var _ = Describe("ModelConfigLoader.GetModelsConflictingWith", func() {
	var bcl *ModelConfigLoader

	BeforeEach(func() {
		bcl = NewModelConfigLoader("/tmp/conflict-test-models")
	})

	insert := func(cfg ModelConfig) {
		bcl.Lock()
		bcl.configs[cfg.Name] = cfg
		bcl.Unlock()
	}

	It("returns nil when the named model has no groups", func() {
		insert(ModelConfig{Name: "loner"})
		Expect(bcl.GetModelsConflictingWith("loner")).To(BeNil())
	})

	It("returns nil when the named model is unknown", func() {
		Expect(bcl.GetModelsConflictingWith("ghost")).To(BeNil())
	})

	It("returns nil when no other model shares a group", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"vision"}})
		Expect(bcl.GetModelsConflictingWith("a")).To(BeNil())
	})

	It("returns models that share at least one group", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "c", ConcurrencyGroups: []string{"vision"}})
		insert(ModelConfig{Name: "d", ConcurrencyGroups: []string{"heavy", "vision"}})

		conflicts := bcl.GetModelsConflictingWith("a")
		Expect(conflicts).To(ConsistOf("b", "d"))
	})

	It("never lists the queried model itself", func() {
		insert(ModelConfig{Name: "self", ConcurrencyGroups: []string{"heavy"}})
		Expect(bcl.GetModelsConflictingWith("self")).To(BeNil())
	})

	It("ignores disabled conflicting models", func() {
		disabled := true
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{"heavy"}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy"}, Disabled: &disabled})
		Expect(bcl.GetModelsConflictingWith("a")).To(BeNil())
	})

	It("normalizes groups so whitespace and duplicates do not break overlap", func() {
		insert(ModelConfig{Name: "a", ConcurrencyGroups: []string{" heavy "}})
		insert(ModelConfig{Name: "b", ConcurrencyGroups: []string{"heavy", "heavy"}})
		Expect(bcl.GetModelsConflictingWith("a")).To(ConsistOf("b"))
	})
})

var _ = Describe("ModelConfigLoader alias resolution", func() {
	var loader *ModelConfigLoader

	BeforeEach(func() {
		loader = NewModelConfigLoader("")
		loader.configs["real"] = ModelConfig{Name: "real", Backend: "llama-cpp"}
		loader.configs["gpt-4"] = ModelConfig{Name: "gpt-4", Alias: "real"}
		loader.configs["chain"] = ModelConfig{Name: "chain", Alias: "gpt-4"}
		loader.configs["dangling"] = ModelConfig{Name: "dangling", Alias: "nope"}
	})

	It("returns non-alias configs unchanged", func() {
		cfg := loader.configs["real"]
		got, was, err := loader.ResolveAlias(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeFalse())
		Expect(got.Name).To(Equal("real"))
	})

	It("resolves an alias to its target", func() {
		cfg := loader.configs["gpt-4"]
		got, was, err := loader.ResolveAlias(&cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(was).To(BeTrue())
		Expect(got.Name).To(Equal("real"))
	})

	It("rejects an alias chain", func() {
		cfg := loader.configs["chain"]
		_, was, err := loader.ResolveAlias(&cfg)
		Expect(was).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("chains are not allowed")))
	})

	It("rejects a dangling alias", func() {
		cfg := loader.configs["dangling"]
		_, _, err := loader.ResolveAlias(&cfg)
		Expect(err).To(MatchError(ContainSubstring("unknown model")))
	})

	It("ValidateAliasTarget passes for a real target and fails for a chain", func() {
		good := loader.configs["gpt-4"]
		Expect(loader.ValidateAliasTarget(&good)).ToNot(HaveOccurred())
		bad := loader.configs["chain"]
		Expect(loader.ValidateAliasTarget(&bad)).To(MatchError(ContainSubstring("itself an alias")))
	})
})
