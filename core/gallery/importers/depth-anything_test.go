package importers_test

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// depthAnythingDetails builds Details carrying a synthetic HF file list so
// detection can be exercised without hitting the network.
func depthAnythingDetails(uri string, prefs string, files ...hfapi.ModelFile) importers.Details {
	return importers.Details{
		URI:         uri,
		Preferences: json.RawMessage(prefs),
		HuggingFace: &hfapi.ModelDetails{Files: files},
	}
}

var _ = Describe("DepthAnythingImporter", func() {
	imp := &importers.DepthAnythingImporter{}

	Context("Importer interface metadata", func() {
		It("exposes name/modality/autodetect", func() {
			Expect(imp.Name()).To(Equal("depth-anything"))
			Expect(imp.Modality()).To(Equal("image"))
			Expect(imp.AutoDetects()).To(BeTrue())
		})
	})

	Context("detection (Match)", func() {
		It("matches an HF repo shipping a depth-anything GGUF", func() {
			d := depthAnythingDetails("huggingface://mudler/depth-anything.cpp-gguf", `{}`,
				hfapi.ModelFile{Path: "depth-anything-small-f32.gguf"},
				hfapi.ModelFile{Path: "README.md"},
			)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("matches a direct URL to a depth-anything GGUF", func() {
			d := depthAnythingDetails("https://huggingface.co/mudler/depth-anything.cpp-gguf/resolve/main/depth-anything-large-q4_k.gguf", `{}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("honours preferences.backend=depth-anything for arbitrary URIs", func() {
			d := depthAnythingDetails("https://example.com/whatever", `{"backend": "depth-anything"}`)
			Expect(imp.Match(d)).To(BeTrue())
		})

		It("does NOT claim a generic llama-style GGUF", func() {
			d := depthAnythingDetails("huggingface://someorg/some-llm-gguf", `{}`,
				hfapi.ModelFile{Path: "llama-3-8b-instruct-q4_k_m.gguf"},
			)
			Expect(imp.Match(d)).To(BeFalse())
		})

		It("does NOT claim the upstream PyTorch repo (safetensors, no GGUF)", func() {
			d := depthAnythingDetails("huggingface://depth-anything/Depth-Anything-V3", `{}`,
				hfapi.ModelFile{Path: "model.safetensors"},
			)
			Expect(imp.Match(d)).To(BeFalse())
		})
	})

	Context("import (Import)", func() {
		It("picks the default quant (q4_k) from a multi-quant HF repo", func() {
			d := depthAnythingDetails("huggingface://mudler/depth-anything.cpp-gguf", `{"name":"depth-anything-small"}`,
				hfapi.ModelFile{Path: "depth-anything-small-f32.gguf", URL: "https://hf/f32", SHA256: "aaa"},
				hfapi.ModelFile{Path: "depth-anything-small-q4_k.gguf", URL: "https://hf/q4k", SHA256: "bbb"},
				hfapi.ModelFile{Path: "depth-anything-small-q8_0.gguf", URL: "https://hf/q8", SHA256: "ccc"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.ConfigFile).To(ContainSubstring("backend: depth-anything"), fmt.Sprintf("%+v", cfg))
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q4k"), "default quant should be q4_k")
			Expect(cfg.Files[0].Filename).To(ContainSubstring("depth-anything/models/depth-anything-small/depth-anything-small-q4_k.gguf"))
		})

		It("honours a preferred quantization override", func() {
			d := depthAnythingDetails("huggingface://mudler/depth-anything.cpp-gguf", `{"name":"d","quantizations":"q8_0"}`,
				hfapi.ModelFile{Path: "depth-anything-small-f32.gguf", URL: "https://hf/f32"},
				hfapi.ModelFile{Path: "depth-anything-small-q8_0.gguf", URL: "https://hf/q8"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/q8"))
		})

		It("falls back to f32 when no quantized file is present", func() {
			d := depthAnythingDetails("huggingface://mudler/depth-anything.cpp-gguf", `{"name":"d"}`,
				hfapi.ModelFile{Path: "depth-anything-base-f32.gguf", URL: "https://hf/f32"},
			)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("https://hf/f32"))
		})

		It("uses the exact file for a direct GGUF URL", func() {
			d := depthAnythingDetails("https://huggingface.co/mudler/depth-anything.cpp-gguf/resolve/main/depth-anything-base-q5_k.gguf", `{"name":"da"}`)
			cfg, err := imp.Import(d)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].Filename).To(ContainSubstring("depth-anything/models/da/depth-anything-base-q5_k.gguf"))
		})
	})
})
