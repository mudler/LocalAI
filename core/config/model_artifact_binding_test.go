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
		primary := modelartifacts.Spec{
			Name: "model", Target: "model",
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", Revision: "main"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		Expect(persistArtifactBinding(fileName, "managed", []modelartifacts.Spec{primary})).To(Succeed())
		updated, err := os.ReadFile(fileName)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updated)).To(ContainSubstring("name: sibling"))
		Expect(string(updated)).To(ContainSubstring("sibling_only: true"))
		Expect(string(updated)).To(ContainSubstring("unknown_extension: keep-me"))
		Expect(string(updated)).To(ContainSubstring("model: owner/repo"))
		Expect(string(updated)).To(ContainSubstring("cache_key: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
		Expect(string(updated)).To(ContainSubstring("revision: 0123456789abcdef0123456789abcdef01234567"))
	})

	It("writes back every artifact it is given, primary and companion", func() {
		// The binding replaces the whole artifacts list, so a companion is only
		// retained if it is passed in. Dropping it here is what lost companions
		// on a controller restart (the distributed longcat-video base_model bug).
		fileName := filepath.Join(GinkgoT().TempDir(), "models.yaml")
		Expect(os.WriteFile(fileName, []byte(`
- name: avatar
  backend: longcat-video
  artifacts:
    - name: model
      target: model
      source: {type: huggingface, repo: owner/avatar}
    - name: base_model
      target: companion
      source: {type: huggingface, repo: owner/base}
  parameters: {model: owner/avatar}
`), 0644)).To(Succeed())
		primaryKey := "1111111111111111111111111111111111111111111111111111111111111111"
		companionKey := "2222222222222222222222222222222222222222222222222222222222222222"
		resolved := func(repo, key string) modelartifacts.Spec {
			return modelartifacts.Spec{
				Name: "x", Target: "companion",
				Source: modelartifacts.Source{Type: "huggingface", Repo: repo, Revision: "main"},
				Resolved: &modelartifacts.Resolved{
					Endpoint: "https://huggingface.co",
					Revision: "0123456789abcdef0123456789abcdef01234567",
					CacheKey: key,
				},
			}
		}
		primary := resolved("owner/avatar", primaryKey)
		primary.Name, primary.Target = "model", "model"
		companion := resolved("owner/base", companionKey)
		companion.Name = "base_model"

		Expect(persistArtifactBinding(fileName, "avatar", []modelartifacts.Spec{primary, companion})).To(Succeed())

		updated, err := os.ReadFile(fileName)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updated)).To(ContainSubstring("name: base_model"))
		Expect(string(updated)).To(ContainSubstring(primaryKey))
		Expect(string(updated)).To(ContainSubstring(companionKey))
	})
})
