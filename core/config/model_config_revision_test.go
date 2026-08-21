package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Model configuration revisions", func() {
	parse := func(document string) *config.ModelConfig {
		cfg := &config.ModelConfig{}
		Expect(yaml.Unmarshal([]byte(document), cfg)).To(Succeed())
		return cfg
	}

	revision := func(cfg *config.ModelConfig) string {
		value, err := config.ModelConfigRevision(cfg)
		Expect(err).NotTo(HaveOccurred())
		return value
	}

	It("is stable across equivalent YAML formatting and map order", func() {
		first := parse("name: example\nbackend: llama-cpp\nroles: {user: USER, assistant: ASSISTANT}\n")
		second := parse("# Comments and presentation details do not affect the typed configuration.\n" +
			"backend: llama-cpp\nroles:\n  assistant: ASSISTANT\n  user: USER\nname: example\n")

		Expect(revision(first)).To(Equal(revision(second)))
	})

	It("changes for context and parallel options", func() {
		base := parse("name: example\ncontext_size: 2048\noptions: [parallel:1]\n")
		contextChanged := parse("name: example\ncontext_size: 4096\noptions: [parallel:1]\n")
		parallelChanged := parse("name: example\ncontext_size: 2048\noptions: [parallel:2]\n")

		Expect(revision(contextChanged)).NotTo(Equal(revision(base)))
		Expect(revision(parallelChanged)).NotTo(Equal(revision(base)))
	})

	It("preserves meaningful absence versus explicit zero", func() {
		absent := parse("name: example\n")
		explicitZero := parse("name: example\ncontext_size: 0\n")

		Expect(revision(explicitZero)).NotTo(Equal(revision(absent)))
	})

	It("excludes the configuration source path", func() {
		dir := GinkgoT().TempDir()
		paths := []string{filepath.Join(dir, "first.yaml"), filepath.Join(dir, "second.yaml")}
		for _, path := range paths {
			Expect(os.WriteFile(path, []byte("name: example\nbackend: llama-cpp\n"), 0o600)).To(Succeed())
		}

		loaded := make([]config.ModelConfig, 0, len(paths))
		for _, path := range paths {
			loader := config.NewModelConfigLoader(dir)
			Expect(loader.ReadModelConfig(path)).To(Succeed())
			cfg, found := loader.GetModelConfig("example")
			Expect(found).To(BeTrue())
			loaded = append(loaded, cfg)
		}

		Expect(loaded[0].GetModelConfigFile()).NotTo(Equal(loaded[1].GetModelConfigFile()))
		Expect(revision(&loaded[0])).To(Equal(revision(&loaded[1])))
	})

	It("hashes effective protobuf options deterministically without mutation", func() {
		opts := &pb.ModelOptions{Model: "example", ContextSize: 2048, TensorParallelSize: 1}
		original := opts.String()

		first, err := config.EffectiveModelOptionsHash(opts)
		Expect(err).NotTo(HaveOccurred())
		second, err := config.EffectiveModelOptionsHash(opts)
		Expect(err).NotTo(HaveOccurred())

		Expect(second).To(Equal(first))
		Expect(opts.String()).To(Equal(original))

		changed := &pb.ModelOptions{Model: "example", ContextSize: 4096, TensorParallelSize: 1}
		different, err := config.EffectiveModelOptionsHash(changed)
		Expect(err).NotTo(HaveOccurred())
		Expect(different).NotTo(Equal(first))
	})
})
