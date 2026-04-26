package importers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
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
})
