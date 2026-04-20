package importers_test

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Embeddings ambiguity", func() {
	// Qdrant/bm25 carries pipeline_tag="sentence-similarity" but ships
	// only config.json, README.md, .gitattributes, and per-language
	// stopword .txt files — no tokenizer.json (rules out vllm and
	// transformers), no modules.json / sentence_bert_config.json (rules
	// out sentencetransformers), no "reranker" / cross-encoder owner
	// (rules out rerankers), no rf-detr name (rules out rfdetr), no
	// snakers4 / silero_vad.onnx (rules out silero-vad), no .gguf
	// (rules out llama-cpp and stablediffusion-ggml), no mlx-community
	// owner (rules out mlx), no model_index.json / scheduler_config.json
	// (rules out diffusers). None of the ASR/TTS/image importers should
	// trip either. Because sentence-similarity is in the ambiguous
	// modality whitelist, DiscoverModelConfig must surface
	// ErrAmbiguousImport.
	It("returns ErrAmbiguousImport when sentence-similarity pipeline_tag is present but no importer matches", func() {
		uri := "https://huggingface.co/Qdrant/bm25"
		preferences := json.RawMessage(`{}`)

		_, err := importers.DiscoverModelConfig(uri, preferences)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, importers.ErrAmbiguousImport)).To(BeTrue(), "expected ErrAmbiguousImport, got: %v", err)
	})
})
