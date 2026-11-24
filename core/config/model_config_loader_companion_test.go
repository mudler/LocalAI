package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

// companionMaterializer resolves each requested repo to its own snapshot, so a
// test can tell which artifacts were actually acquired rather than assuming the
// single fixed result the other preload fakes return.
type companionMaterializer struct {
	mu    sync.Mutex
	specs []modelartifacts.Spec
	fail  string
}

func (f *companionMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	if f.fail != "" && spec.Source.Repo == f.fail {
		return modelartifacts.Result{}, fmt.Errorf("materialization refused for %s", spec.Source.Repo)
	}
	// A distinct, well-formed cache key per repo, so the resulting snapshot
	// paths differ and a test can prove the companion is not aliasing the
	// primary.
	key := strings.Repeat(fmt.Sprintf("%x", len(spec.Source.Repo)%16), 64)
	resolved := spec
	resolved.Source.Revision = "main"
	resolved.Resolved = &modelartifacts.Resolved{
		Endpoint: "https://huggingface.co",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		CacheKey: key,
	}
	return modelartifacts.Result{
		Spec:         resolved,
		RelativePath: filepath.Join(".artifacts", "huggingface", key, "snapshot"),
	}, nil
}

func (f *companionMaterializer) repos() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := make([]string, 0, len(f.specs))
	for _, spec := range f.specs {
		seen = append(seen, spec.Source.Repo)
	}
	return seen
}

var _ = Describe("companion artifact materialization", func() {
	const companionConfig = `
name: avatar
backend: longcat-video
artifacts:
  - name: model
    target: model
    source: {type: huggingface, repo: meituan-longcat/LongCat-Video-Avatar-1.5}
  - name: base_model
    target: companion
    source: {type: huggingface, repo: meituan-longcat/LongCat-Video}
parameters: {model: meituan-longcat/LongCat-Video-Avatar-1.5}
`

	It("acquires every declared artifact, not only the primary", func() {
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "avatar.yaml")
		Expect(os.WriteFile(configPath, []byte(companionConfig), 0644)).To(Succeed())

		fake := &companionMaterializer{}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		Expect(fake.repos()).To(ConsistOf(
			"meituan-longcat/LongCat-Video-Avatar-1.5",
			"meituan-longcat/LongCat-Video",
		))
	})

	It("persists the resolved state of every artifact", func() {
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "avatar.yaml")
		Expect(os.WriteFile(configPath, []byte(companionConfig), 0644)).To(Succeed())

		fake := &companionMaterializer{}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		loaded, found := loader.GetModelConfig("avatar")
		Expect(found).To(BeTrue())
		Expect(loaded.Artifacts).To(HaveLen(2))
		Expect(loaded.Artifacts[0].Target).To(Equal(modelartifacts.TargetModel))
		Expect(loaded.Artifacts[0].Resolved).ToNot(BeNil())
		Expect(loaded.Artifacts[1].Name).To(Equal("base_model"))
		Expect(loaded.Artifacts[1].Target).To(Equal(modelartifacts.TargetCompanion))
		Expect(loaded.Artifacts[1].Resolved).ToNot(BeNil())
		// The companion must land in its own snapshot, never alias the primary.
		Expect(loaded.Artifacts[1].Resolved.CacheKey).ToNot(Equal(loaded.Artifacts[0].Resolved.CacheKey))
		// The load target still resolves from the primary alone.
		Expect(loaded.ModelFileName()).To(ContainSubstring(loaded.Artifacts[0].Resolved.CacheKey))
	})

	It("keeps every resolved artifact in the persisted file across a reload", func() {
		// Regression for the distributed longcat-video companion loss: a
		// controller resolves the primary and companion in memory, but if the
		// binding it writes back to disk carries only the primary, the companion
		// is gone the moment the process restarts and reloads the file. With no
		// companion in the config, withCompanionArtifactOptions synthesizes no
		// base_model option, so the remote backend falls back to downloading the
		// base repo itself and fails ("base_model must point to a LongCat-Video
		// checkpoint"). The persisted document, reloaded fresh, must still name
		// the companion.
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "avatar.yaml")
		Expect(os.WriteFile(configPath, []byte(companionConfig), 0644)).To(Succeed())

		fake := &companionMaterializer{}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		Expect(loader.PreloadWithContext(context.Background(), modelsPath)).To(Succeed())

		// A fresh loader models the restart: it only ever sees what was written
		// back to disk, never the in-memory state the first loader held.
		reloaded := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(&companionMaterializer{}))
		Expect(reloaded.LoadModelConfigsFromPath(modelsPath)).To(Succeed())

		persisted, found := reloaded.GetModelConfig("avatar")
		Expect(found).To(BeTrue())
		Expect(persisted.Artifacts).To(HaveLen(2))
		Expect(persisted.Artifacts[1].Name).To(Equal("base_model"))
		Expect(persisted.Artifacts[1].Target).To(Equal(modelartifacts.TargetCompanion))
		Expect(persisted.Artifacts[1].Resolved).ToNot(BeNil())
		Expect(persisted.Artifacts[1].Resolved.CacheKey).ToNot(BeEmpty())
	})

	It("fails the load when an explicitly declared companion cannot be acquired", func() {
		// Explicit artifacts are all-or-nothing: a config that names a companion
		// is asserting the backend needs it, so silently loading without it
		// would just move the failure somewhere less legible.
		modelsPath := GinkgoT().TempDir()
		configPath := filepath.Join(modelsPath, "avatar.yaml")
		Expect(os.WriteFile(configPath, []byte(companionConfig), 0644)).To(Succeed())

		fake := &companionMaterializer{fail: "meituan-longcat/LongCat-Video"}
		loader := NewModelConfigLoader(modelsPath, WithArtifactMaterializer(fake))
		Expect(loader.LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		err := loader.PreloadWithContext(context.Background(), modelsPath)
		Expect(err).To(MatchError(ContainSubstring("LongCat-Video")))
	})
})
