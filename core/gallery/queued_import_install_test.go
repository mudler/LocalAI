package gallery_test

import (
	"context"
	"path/filepath"

	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The queued manual-import op carries only the config YAML — no files: —
// so InstallModel must resolve and bind the managed artifact itself,
// exactly as it does for a gallery entry. These specs pin that for the
// references the import endpoint forwards without flattening; a file
// entry here would have routed the repository through the single-file
// downloader and skipped the materializer entirely.
var _ = Describe("queued manual import installs", func() {
	run := func(configFile string) (*fakeArtifactMaterializer, string) {
		modelsPath := GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).ToNot(HaveOccurred())

		// Standing in for the materializer keeps the install off the network.
		materializer := &fakeArtifactMaterializer{result: modelartifacts.Result{
			Spec: modelartifacts.Spec{
				Name: "model", Target: "model",
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
				Resolved: &modelartifacts.Resolved{
					Endpoint: "https://huggingface.co",
					Revision: "0123456789abcdef0123456789abcdef01234567",
					CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
			},
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
		}}

		_, err = InstallModel(context.TODO(), state, "queued-model",
			&ModelConfig{ConfigFile: configFile}, nil, nil, false,
			WithArtifactMaterializer(materializer))
		Expect(err).ToNot(HaveOccurred())
		return materializer, modelsPath
	}

	It("binds an hf:// repository through the materializer", func() {
		m, dir := run(`name: queued-model
backend: transformers
parameters:
  model: hf://owner/repo
`)
		Expect(m.seen).ToNot(BeEmpty())
		Expect(m.seen[0].Source.Repo).To(Equal("owner/repo"))
		Expect(filepath.Join(dir, "queued-model.yaml")).To(BeAnExistingFile())
	})

	It("binds a bare owner/repo reference through the materializer", func() {
		m, dir := run(`name: queued-model
backend: transformers
parameters:
  model: owner/repo
`)
		Expect(m.seen).ToNot(BeEmpty())
		Expect(m.seen[0].Source.Repo).To(Equal("owner/repo"))
		Expect(filepath.Join(dir, "queued-model.yaml")).To(BeAnExistingFile())
	})
})
