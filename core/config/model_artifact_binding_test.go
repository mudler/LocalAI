package config

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("artifact binding persistence", func() {
	It("updates only the named model in a multi-config document", func() {
		fileName := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(fileName, []byte(`
- name: managed
  backend: transformers
  unknown_extension: keep-me
  artifacts:
    - source: {type: huggingface, repo: owner/repo}
  parameters: {model: owner/repo}
- name: sibling
  backend: llama-cpp
  sibling_only: true
  parameters: {model: sibling.gguf}
`), 0644)).To(Succeed())
		result := modelartifacts.Result{
			RelativePath: ".artifacts/huggingface/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/snapshot",
			Spec: modelartifacts.Spec{
				Name: "model", Target: "model",
				Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
				Resolved: &modelartifacts.Resolved{
					Endpoint: "https://huggingface.co",
					Revision: "0123456789abcdef0123456789abcdef01234567",
					CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				},
			},
		}
		Expect(persistArtifactBinding(fileName, "managed", result)).To(Succeed())
		updated, err := os.ReadFile(fileName)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updated)).To(ContainSubstring("name: sibling"))
		Expect(string(updated)).To(ContainSubstring("sibling_only: true"))
		Expect(string(updated)).To(ContainSubstring("unknown_extension: keep-me"))
		Expect(string(updated)).To(ContainSubstring("model: owner/repo"))
		Expect(string(updated)).To(ContainSubstring("cache_key: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
		Expect(string(updated)).To(ContainSubstring("revision: 0123456789abcdef0123456789abcdef01234567"))
	})
})
