package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("PrimaryArtifactSpec backend gating", func() {
	parse := func(doc string) config.ModelConfig {
		var c config.ModelConfig
		Expect(yaml.Unmarshal([]byte(doc), &c)).To(Succeed())
		return c
	}

	It("does not infer a managed artifact for a single-file backend", func() {
		// llama.cpp consumes a single GGUF file, not a snapshot directory.
		// A bare HuggingFace file reference must stay on the legacy
		// download-to-file path so the backend receives the file itself.
		c := parse("backend: llama-cpp\nparameters: {model: huggingface://owner/repo/model.gguf}\n")
		_, inferred, managed, err := c.PrimaryArtifactSpec("/models")
		Expect(err).NotTo(HaveOccurred())
		Expect(managed).To(BeFalse())
		Expect(inferred).To(BeFalse())
	})

	It("does not infer a managed artifact for a bare repo on a single-file backend", func() {
		c := parse("backend: llama-cpp\nparameters: {model: owner/repo}\n")
		_, _, managed, err := c.PrimaryArtifactSpec("/models")
		Expect(err).NotTo(HaveOccurred())
		Expect(managed).To(BeFalse())
	})

	It("infers a managed artifact for a directory-consuming backend", func() {
		c := parse("backend: transformers\nparameters: {model: huggingface://owner/repo/model.safetensors}\n")
		spec, inferred, managed, err := c.PrimaryArtifactSpec("/models")
		Expect(err).NotTo(HaveOccurred())
		Expect(managed).To(BeTrue())
		Expect(inferred).To(BeTrue())
		Expect(spec.Source.Repo).To(Equal("owner/repo"))
	})

	It("keeps explicit artifacts managed even on a single-file backend", func() {
		// An explicitly declared artifacts: block is a deliberate choice;
		// single-file resolution (PrimaryFile) handles the load path.
		c := parse("backend: llama-cpp\nartifacts:\n  - source: {type: huggingface, repo: owner/repo}\nparameters: {model: owner/repo}\n")
		_, _, managed, err := c.PrimaryArtifactSpec("/models")
		Expect(err).NotTo(HaveOccurred())
		Expect(managed).To(BeTrue())
	})
})
