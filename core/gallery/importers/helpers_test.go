package importers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"gopkg.in/yaml.v3"
)

var _ = Describe("importer helpers", func() {
	mkFiles := func(paths ...string) []hfapi.ModelFile {
		out := make([]hfapi.ModelFile, 0, len(paths))
		for _, p := range paths {
			out = append(out, hfapi.ModelFile{Path: p})
		}
		return out
	}

	Describe("HasFile", func() {
		It("returns false for an empty slice", func() {
			Expect(importers.HasFile(nil, "config.json")).To(BeFalse())
			Expect(importers.HasFile([]hfapi.ModelFile{}, "config.json")).To(BeFalse())
		})
		It("matches on exact basename, ignoring directory components", func() {
			files := mkFiles("sub/dir/config.json", "other.txt")
			Expect(importers.HasFile(files, "config.json")).To(BeTrue())
		})
		It("does not match partial basenames", func() {
			files := mkFiles("sub/dir/myconfig.json")
			Expect(importers.HasFile(files, "config.json")).To(BeFalse())
		})
	})

	Describe("HasExtension", func() {
		It("matches case-insensitively", func() {
			files := mkFiles("model.ONNX", "other.txt")
			Expect(importers.HasExtension(files, ".onnx")).To(BeTrue())
		})
		It("returns false when no file has the extension", func() {
			Expect(importers.HasExtension(mkFiles("README.md"), ".onnx")).To(BeFalse())
		})
		It("handles empty slices gracefully", func() {
			Expect(importers.HasExtension(nil, ".onnx")).To(BeFalse())
		})
	})

	Describe("HasONNX", func() {
		It("is true when any file ends in .onnx", func() {
			Expect(importers.HasONNX(mkFiles("voice/en_US-amy-medium.onnx"))).To(BeTrue())
		})
		It("is false otherwise", func() {
			Expect(importers.HasONNX(mkFiles("model.bin"))).To(BeFalse())
		})
	})

	Describe("HasONNXConfigPair", func() {
		It("matches the piper .onnx + .onnx.json pair", func() {
			files := mkFiles(
				"en/en_US/amy/medium/en_US-amy-medium.onnx",
				"en/en_US/amy/medium/en_US-amy-medium.onnx.json",
			)
			Expect(importers.HasONNXConfigPair(files)).To(BeTrue())
		})
		It("requires the accompanying json to share the .onnx basename", func() {
			files := mkFiles("model.onnx", "config.json")
			Expect(importers.HasONNXConfigPair(files)).To(BeFalse())
		})
		It("returns false for a lone .onnx file", func() {
			files := mkFiles("model.onnx")
			Expect(importers.HasONNXConfigPair(files)).To(BeFalse())
		})
	})

	Describe("HasGGMLFile", func() {
		It("finds ggml-prefixed .bin files", func() {
			files := mkFiles("ggml-base.en.bin", "README.md")
			Expect(importers.HasGGMLFile(files, "ggml-")).To(BeTrue())
		})
		It("requires both prefix and .bin suffix", func() {
			files := mkFiles("ggml-base.en.gguf")
			Expect(importers.HasGGMLFile(files, "ggml-")).To(BeFalse())
		})
		It("returns false when prefix does not match", func() {
			files := mkFiles("whisper.bin")
			Expect(importers.HasGGMLFile(files, "ggml-")).To(BeFalse())
		})
	})

	Describe("LocalModelPath", func() {
		It("strips the file:// scheme from an absolute local path", func() {
			Expect(importers.LocalModelPath("file:///Users/u/.lmstudio/models/mlx-community/Qwen3-4bit")).
				To(Equal("/Users/u/.lmstudio/models/mlx-community/Qwen3-4bit"))
		})
		It("strips the file:// scheme from a relative local path", func() {
			Expect(importers.LocalModelPath("file://my-models/nvidia/Qwen3-30B-A3B-FP4")).
				To(Equal("my-models/nvidia/Qwen3-30B-A3B-FP4"))
		})
		It("leaves HuggingFace and HTTP URIs unchanged", func() {
			Expect(importers.LocalModelPath("https://huggingface.co/mlx-community/test-model")).
				To(Equal("https://huggingface.co/mlx-community/test-model"))
			Expect(importers.LocalModelPath("mlx-community/test-model")).
				To(Equal("mlx-community/test-model"))
		})
	})
})

var _ = Describe("managed artifact attachment", func() {
	details := importers.Details{
		URI:         "https://huggingface.co/owner/repo",
		HuggingFace: &hfapi.ModelDetails{ModelID: "owner/repo"},
	}

	It("attaches a source when the imported model is the discovered repository", func() {
		input := gallery.ModelConfig{ConfigFile: "backend: transformers\nparameters:\n  model: owner/repo\n"}
		output, err := importers.AttachPrimaryArtifact(input, details)
		Expect(err).NotTo(HaveOccurred())
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal([]byte(output.ConfigFile), &cfg)).To(Succeed())
		Expect(cfg.Artifacts).To(HaveLen(1))
		Expect(cfg.Artifacts[0].Source.Repo).To(Equal("owner/repo"))
	})

	It("preserves explicit gallery files", func() {
		input := gallery.ModelConfig{
			ConfigFile: "parameters:\n  model: owner/repo\n",
			Files:      []gallery.File{{Filename: "model.gguf", URI: "https://huggingface.co/owner/repo/resolve/main/model.gguf"}},
		}
		output, err := importers.AttachPrimaryArtifact(input, details)
		Expect(err).NotTo(HaveOccurred())
		Expect(output.ConfigFile).To(Equal(input.ConfigFile))
	})

	It("does not attach a managed source to an unmigrated backend", func() {
		input := gallery.ModelConfig{ConfigFile: "backend: custom-python\nparameters:\n  model: owner/repo\n"}
		output, err := importers.AttachPrimaryArtifact(input, details)
		Expect(err).NotTo(HaveOccurred())
		Expect(output.ConfigFile).To(Equal(input.ConfigFile))
	})

	DescribeTable("does not guess an unrelated or local source",
		func(model string, candidate importers.Details) {
			input := gallery.ModelConfig{ConfigFile: "parameters:\n  model: " + model + "\n"}
			output, err := importers.AttachPrimaryArtifact(input, candidate)
			Expect(err).NotTo(HaveOccurred())
			var cfg config.ModelConfig
			Expect(yaml.Unmarshal([]byte(output.ConfigFile), &cfg)).To(Succeed())
			Expect(cfg.Artifacts).To(BeEmpty())
		},
		Entry("different repo", "other/repo", details),
		Entry("local file", "/models/repo", importers.Details{URI: "file:///models/repo"}),
	)
})
