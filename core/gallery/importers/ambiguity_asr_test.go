package importers_test

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ASR ambiguity", func() {
	// pyannote/voice-activity-detection carries
	// pipeline_tag=automatic-speech-recognition but ships only a YAML
	// recipe — no ggml-*.bin, no .nemo, no Systran-style model.bin, no
	// tokenizer.json, no .onnx. None of the ASR importers should match and
	// none of the generic importers (vllm, transformers, llama-cpp, mlx,
	// diffusers) should match either. Because the modality is in the
	// ambiguous whitelist, DiscoverModelConfig must surface
	// ErrAmbiguousImport rather than a bare "no importer matched" error.
	It("returns ErrAmbiguousImport when ASR pipeline_tag is present but no importer matches", func() {
		uri := "https://huggingface.co/pyannote/voice-activity-detection"
		preferences := json.RawMessage(`{}`)

		_, err := importers.DiscoverModelConfig(uri, preferences)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, importers.ErrAmbiguousImport)).To(BeTrue(), "expected ErrAmbiguousImport, got: %v", err)
	})
})
