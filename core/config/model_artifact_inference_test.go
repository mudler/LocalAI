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

	It("infers a managed artifact for longcat-video from a bare repo id", func() {
		// longcat-video loads a checkpoint directory (its backend.py takes
		// request.ModelFile when os.path.isdir), so it belongs to the same
		// class as transformers/vllm/diffusers. Off the allow-list, the
		// controller never acquires the weights and the backend re-downloads
		// them from HuggingFace inside the remote LoadModel deadline.
		c := parse("backend: longcat-video\nparameters: {model: meituan-longcat/LongCat-Video-Avatar-1.5}\n")
		spec, inferred, managed, err := c.PrimaryArtifactSpec("/models")
		Expect(err).NotTo(HaveOccurred())
		Expect(managed).To(BeTrue())
		Expect(inferred).To(BeTrue())
		Expect(spec.Source.Repo).To(Equal("meituan-longcat/LongCat-Video-Avatar-1.5"))
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
