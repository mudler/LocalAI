package gallery_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
)

// perRepoMaterializer resolves each repo to its own snapshot so a test can
// distinguish the primary from its companions in the persisted config.
type perRepoMaterializer struct {
	seen []modelartifacts.Spec
	fail string
}

func (f *perRepoMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	f.seen = append(f.seen, spec)
	if f.fail != "" && spec.Source.Repo == f.fail {
		return modelartifacts.Result{}, fmt.Errorf("acquisition failed for %s", spec.Source.Repo)
	}
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

var _ = Describe("gallery companion artifact installation", func() {
	const companionConfig = `
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

	It("acquires and persists every declared artifact at install time", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		fake := &perRepoMaterializer{}
		definition := &gallery.ModelConfig{Name: "avatar", ConfigFile: companionConfig}

		installed, err := gallery.InstallModel(context.Background(), state, "avatar", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.seen).To(HaveLen(2))
		Expect(installed.Artifacts).To(HaveLen(2))
		Expect(installed.Artifacts[1].Name).To(Equal("base_model"))
		Expect(installed.Artifacts[1].Resolved).ToNot(BeNil())

		data, err := os.ReadFile(filepath.Join(modelsPath, "avatar.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var persisted map[string]any
		Expect(yaml.Unmarshal(data, &persisted)).To(Succeed())
		artifacts := persisted["artifacts"].([]any)
		Expect(artifacts).To(HaveLen(2))
		companion := artifacts[1].(map[string]any)
		Expect(companion["name"]).To(Equal("base_model"))
		Expect(companion).To(HaveKey("resolved"))
	})

	It("does not install when a companion cannot be acquired", func() {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).NotTo(HaveOccurred())
		configPath := filepath.Join(modelsPath, "avatar.yaml")
		Expect(os.WriteFile(configPath, []byte("name: old\n"), 0644)).To(Succeed())
		fake := &perRepoMaterializer{fail: "meituan-longcat/LongCat-Video"}
		definition := &gallery.ModelConfig{Name: "avatar", ConfigFile: companionConfig}

		_, err = gallery.InstallModel(context.Background(), state, "avatar", definition, nil, nil, false,
			gallery.WithArtifactMaterializer(fake))
		Expect(err).To(MatchError(ContainSubstring("LongCat-Video")))
		// The pre-existing config must survive a failed install.
		Expect(os.ReadFile(configPath)).To(Equal([]byte("name: old\n")))
	})
})
