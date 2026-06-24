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

	// The raw hash is unexported on purpose, so these specs exercise it the way
	// every caller now must: by stamping the parsed config.
	revision := func(cfg *config.ModelConfig) string {
		Expect(cfg.StampPersistedConfigRevision()).To(Succeed())
		return cfg.PersistedConfigRevision()
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

var _ = Describe("Strict model configuration snapshots", func() {
	write := func(dir, name, body string) {
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)).To(Succeed())
	}

	It("ignores valid catalogue and legacy gallery metadata", func() {
		dir := GinkgoT().TempDir()
		write(dir, "catalogue.yaml", "- name: downloadable\n  url: github:example/model.yaml\n- name: inline\n  config_file:\n    backend: llama-cpp\n")
		write(dir, "gallery_simple.yaml", "name: legacy\nconfig_file: |\n  backend: llama-cpp\nfiles:\n- filename: model.gguf\n  uri: https://example.invalid/model.gguf\n")
		write(dir, "installed.yaml", "name: installed\nbackend: llama-cpp\n")

		loader := config.NewModelConfigLoader(dir)
		galleryFiles := config.LoadOptionGalleryFiles(
			config.Gallery{URL: "file://" + filepath.Join(dir, "catalogue.yaml")},
			config.Gallery{URL: "file://" + filepath.Join(dir, "gallery_simple.yaml")},
		)
		Expect(loader.LoadModelConfigsFromPathStrict(dir, galleryFiles)).To(Succeed())
		_, found := loader.GetModelConfig("installed")
		Expect(found).To(BeTrue())
		_, found = loader.GetModelConfig("downloadable")
		Expect(found).To(BeFalse())
		_, found = loader.GetModelConfig("legacy")
		Expect(found).To(BeFalse())
	})

	DescribeTable("rejects malformed gallery-looking documents",
		func(body, message string) {
			dir := GinkgoT().TempDir()
			write(dir, "broken.yaml", body)
			loader := config.NewModelConfigLoader(dir)
			galleryFile := config.LoadOptionGalleryFiles(config.Gallery{URL: "file://" + filepath.Join(dir, "broken.yaml")})
			Expect(loader.LoadModelConfigsFromPathStrict(dir, galleryFile)).To(MatchError(ContainSubstring(message)))
		},
		Entry("invalid variants", "- name: broken\n  variants: []\n", "variants must be a non-empty sequence"),
		Entry("malformed payload type", "- name: broken\n  files: nope\n", "files must be a sequence"),
		Entry("mixed runtime and gallery fields", "- name: broken\n  backend: llama-cpp\n  url: github:example/model.yaml\n", `field "backend" is not gallery metadata`),
		Entry("malformed legacy config", "name: broken\nconfig_file: [not, yaml]\n", "config_file must be a non-empty YAML string"),
	)

	It("still rejects invalid runtime configuration sequences", func() {
		dir := GinkgoT().TempDir()
		write(dir, "broken.yaml", "- name: broken\n  backend: [\n")
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPathStrict(dir)).To(MatchError(ContainSubstring("cannot unmarshal config file")))
	})

	It("does not skip a valid gallery-shaped document without configured provenance", func() {
		dir := GinkgoT().TempDir()
		write(dir, "runtime.yaml", "- name: ambiguous\n  overrides:\n    backend: llama-cpp\n")
		loader := config.NewModelConfigLoader(dir)
		Expect(loader.LoadModelConfigsFromPathStrict(dir)).ToNot(Succeed())
	})
})
