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

	It("normalizes a named companion artifact", func() {
		// Composed pipelines (LongCat-Video-Avatar pulling tokenizer, text
		// encoder and VAE from the LongCat-Video base repo) need more than one
		// snapshot. A companion is an ordinary HuggingFace snapshot addressed by
		// name; the name is what the backend later receives as an option key.
		spec, err := (modelartifacts.Spec{
			Name:   "base_model",
			Target: modelartifacts.TargetCompanion,
			Source: modelartifacts.Source{Type: "huggingface", Repo: "meituan-longcat/LongCat-Video"},
		}).Normalize()
		Expect(err).NotTo(HaveOccurred())
		Expect(spec.Name).To(Equal("base_model"))
		Expect(spec.Target).To(Equal(modelartifacts.TargetCompanion))
		Expect(spec.Source.Revision).To(Equal("main"))
	})

	DescribeTable("rejects malformed companion declarations",
		func(spec modelartifacts.Spec, message string) {
			Expect(spec.Validate()).To(MatchError(ContainSubstring(message)))
		},
		Entry("unknown target", modelartifacts.Spec{Name: "controlnet", Target: "controlnet", Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}, "target"),
		Entry("uppercase name", modelartifacts.Spec{Name: "Base_Model", Target: modelartifacts.TargetCompanion, Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}, "name"),
		Entry("name with a slash", modelartifacts.Spec{Name: "base/model", Target: modelartifacts.TargetCompanion, Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}}, "name"),
		Entry("companion declaring a primary file", modelartifacts.Spec{Name: "base_model", Target: modelartifacts.TargetCompanion, Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}, Resolved: &modelartifacts.Resolved{Endpoint: "https://huggingface.co", Revision: "0123456789abcdef0123456789abcdef01234567", CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", PrimaryFile: "model.gguf"}}, "primary_file"),
	)

	// The cache key is the on-disk identity of a materialized snapshot
	// (.artifacts/huggingface/<key>/snapshot). If widening the artifact model
	// perturbed it, every already-installed managed model would miss its cache
	// and silently re-download on next load. These two specs pin that.
	It("keeps the cache key independent of artifact name and target", func() {
		source := modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}
		resolved := func() *modelartifacts.Resolved {
			return &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
			}
		}
		primary, err := modelartifacts.CacheKey(modelartifacts.Spec{
			Name: modelartifacts.TargetModel, Target: modelartifacts.TargetModel,
			Source: source, Resolved: resolved(),
		})
		Expect(err).NotTo(HaveOccurred())
		companion, err := modelartifacts.CacheKey(modelartifacts.Spec{
			Name: "base_model", Target: modelartifacts.TargetCompanion,
			Source: source, Resolved: resolved(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(companion).To(Equal(primary))
	})

	It("pins the cache key digest for a known primary artifact", func() {
		key, err := modelartifacts.CacheKey(modelartifacts.Spec{
			Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"},
			Resolved: &modelartifacts.Resolved{
				Endpoint: "https://huggingface.co",
				Revision: "0123456789abcdef0123456789abcdef01234567",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("b79e23e0b9c50af094d627582df30109eff8637438864172d64be07dfc5a98f9"))
	})
})
