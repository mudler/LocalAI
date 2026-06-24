package importers_test

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TTS ambiguity", func() {
	// nari-labs/Dia-1.6B carries pipeline_tag="text-to-speech" but ships
	// only config.json + *.pth + model.safetensors + preprocessor_config.json.
	// None of the Batch-2 TTS importers match (owner neither "suno" nor
	// "fishaudio" nor "OuteAI" nor "KittenML" nor "ResembleAI" nor "neuphonic"
	// nor "coqui"; repo name contains none of "bark", "outetts", "voxcpm",
	// "kokoro", "kitten-tts", "neutts", "chatterbox", "vibevoice"; no piper
	// onnx/onnx.json pair). None of the generic importers match either —
	// no tokenizer.json (rules out vllm/transformers), no .gguf (llama-cpp),
	// no mlx-community owner (mlx), no model_index.json/scheduler_config
	// (diffusers). Because the HF pipeline_tag is in the ambiguous
	// whitelist, DiscoverModelConfig must surface ErrAmbiguousImport.
	It("returns ErrAmbiguousImport when TTS pipeline_tag is present but no importer matches", func() {
		uri := "https://huggingface.co/nari-labs/Dia-1.6B"
		preferences := json.RawMessage(`{}`)

		_, err := importers.DiscoverModelConfig(uri, preferences)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, importers.ErrAmbiguousImport)).To(BeTrue(), "expected ErrAmbiguousImport, got: %v", err)
	})
})
