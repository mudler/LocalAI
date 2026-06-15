package importers_test

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image ambiguity", func() {
	// h94/IP-Adapter-FaceID carries pipeline_tag="text-to-image" but ships
	// only .bin + .safetensors + README — no model_index.json /
	// scheduler_config.json (rules out diffusers), no .gguf (rules out
	// llama-cpp and stablediffusion-ggml), no tokenizer.json (rules out
	// vllm/transformers), owner is not mlx-community (rules out mlx), and
	// the repo owner/name contain no ace-step/flux/sd1.5/sdxl/sd3/
	// stable-diffusion arch token at the URI level — so none of the
	// Batch-3 Image/Video importers match either. Because text-to-image
	// is whitelisted as an ambiguous modality, DiscoverModelConfig must
	// surface ErrAmbiguousImport rather than a bare "no importer matched".
	It("returns ErrAmbiguousImport when text-to-image pipeline_tag is present but no importer matches", func() {
		uri := "https://huggingface.co/h94/IP-Adapter-FaceID"
		preferences := json.RawMessage(`{}`)

		_, err := importers.DiscoverModelConfig(uri, preferences)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, importers.ErrAmbiguousImport)).To(BeTrue(), "expected ErrAmbiguousImport, got: %v", err)
	})
})
