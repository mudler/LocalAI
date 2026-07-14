package modelartifacts_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("artifact configuration", func() {
	It("normalizes the supported primary Hugging Face source", func() {
		spec, err := (modelartifacts.Spec{
			Source: modelartifacts.Source{
				Type:     "huggingface",
				Repo:     "hf://Qwen/Qwen3-ASR-1.7B",
				TokenEnv: "HF_TOKEN",
			},
		}).Normalize()
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Name).To(Equal("model"))
		Expect(spec.Target).To(Equal("model"))
		Expect(spec.Source.Repo).To(Equal("Qwen/Qwen3-ASR-1.7B"))
		Expect(spec.Source.Revision).To(Equal("main"))
	})

	It("parses Hugging Face repo and file references into managed sources", func() {
		repo, ok, err := modelartifacts.ParsePrimarySource("huggingface://Qwen/Qwen3-ASR-1.7B")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(repo.Type).To(Equal(modelartifacts.SourceTypeHuggingFace))
		Expect(repo.Repo).To(Equal("Qwen/Qwen3-ASR-1.7B"))
		Expect(repo.AllowPatterns).To(BeEmpty())

		file, ok, err := modelartifacts.ParsePrimarySource("https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.f16.gguf")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(file.Repo).To(Equal("nomic-ai/nomic-embed-text-v1.5-GGUF"))
		Expect(file.AllowPatterns).To(Equal([]string{"nomic-embed-text-v1.5.f16.gguf"}))
	})

	It("ignores non-Hugging Face references", func() {
		_, ok, err := modelartifacts.ParsePrimarySource("models/local-model.gguf")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	DescribeTable("rejects unsafe or unsupported declarations",
		func(spec modelartifacts.Spec, message string) {
			Expect(spec.Validate()).To(MatchError(ContainSubstring(message)))
		},
		Entry("unknown source", modelartifacts.Spec{Source: modelartifacts.Source{Type: "s3", Repo: "owner/repo"}}, "source type"),
		Entry("secondary target", modelartifacts.Spec{Name: "controlnet", Target: "controlnet", Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}, "target"),
		Entry("malformed repo", modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo/file"}}, "owner/repo"),
		Entry("unrelated secret", modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", TokenEnv: "AWS_SECRET_ACCESS_KEY"}}, "HF_TOKEN"),
		Entry("parent filter", modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", AllowPatterns: []string{"../*.json"}}}, "pattern"),
		Entry("prefixed cache key", modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}, Resolved: &modelartifacts.Resolved{Endpoint: "https://huggingface.co", Revision: "0123456789abcdef0123456789abcdef01234567", CacheKey: "sha256:bad"}}, "cache key"),
	)

	It("validates installed state", func() {
		spec := modelartifacts.Spec{
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
				CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		}
		Expect(spec.Validate()).To(Succeed())
	})
})
