package importers_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery/importers"
)

var _ = Describe("ImportLocalPath", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "importers-local-test")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Context("GGUF detection", func() {
		It("detects a GGUF file in the directory", func() {
			modelDir := filepath.Join(tmpDir, "my-model")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "model-q4_k_m.gguf"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("llama-cpp"))
			Expect(cfg.Model).To(ContainSubstring(".gguf"))
			Expect(cfg.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
			Expect(cfg.KnownUsecaseStrings).To(ContainElement("chat"))
			Expect(cfg.Options).To(ContainElement("use_jinja:true"))
		})

		It("detects GGUF in _gguf subdirectory", func() {
			modelDir := filepath.Join(tmpDir, "my-model")
			ggufDir := modelDir + "_gguf"
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.MkdirAll(ggufDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(ggufDir, "model.gguf"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "my-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("llama-cpp"))
		})
	})

	Context("LoRA adapter detection", func() {
		It("detects LoRA adapter via adapter_config.json", func() {
			modelDir := filepath.Join(tmpDir, "lora-model")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())

			adapterConfig := map[string]any{
				"base_model_name_or_path": "meta-llama/Llama-2-7b-hf",
				"peft_type":               "LORA",
			}
			data, _ := json.Marshal(adapterConfig)
			Expect(os.WriteFile(filepath.Join(modelDir, "adapter_config.json"), data, 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "lora-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("transformers"))
			Expect(cfg.Model).To(Equal("meta-llama/Llama-2-7b-hf"))
			Expect(cfg.LLMConfig.LoraAdapter).To(Equal("lora-model"))
			Expect(cfg.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
		})

		It("reads base model from export_metadata.json as fallback", func() {
			modelDir := filepath.Join(tmpDir, "lora-unsloth")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())

			adapterConfig := map[string]any{"peft_type": "LORA"}
			data, _ := json.Marshal(adapterConfig)
			Expect(os.WriteFile(filepath.Join(modelDir, "adapter_config.json"), data, 0644)).To(Succeed())

			metadata := map[string]any{"base_model": "unsloth/tinyllama-bnb-4bit"}
			data, _ = json.Marshal(metadata)
			Expect(os.WriteFile(filepath.Join(modelDir, "export_metadata.json"), data, 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "lora-unsloth")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Model).To(Equal("unsloth/tinyllama-bnb-4bit"))
		})
	})

	Context("Merged model detection", func() {
		It("detects merged model with safetensors + config.json", func() {
			modelDir := filepath.Join(tmpDir, "merged")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "model.safetensors"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "merged")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("transformers"))
			Expect(cfg.Model).To(Equal("merged"))
			Expect(cfg.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
		})

		It("detects merged model with pytorch_model files", func() {
			modelDir := filepath.Join(tmpDir, "merged-pt")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "config.json"), []byte("{}"), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "pytorch_model-00001-of-00002.bin"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "merged-pt")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("transformers"))
			Expect(cfg.Model).To(Equal("merged-pt"))
		})
	})

	Context("Whisper ggml-*.bin detection", func() {
		It("maps ggml-base.en.bin to the whisper backend", func() {
			modelDir := filepath.Join(tmpDir, "whisper-base")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "ggml-base.en.bin"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "whisper-base")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("whisper"))
			Expect(cfg.Model).To(ContainSubstring("ggml-base.en.bin"))
			Expect(cfg.KnownUsecaseStrings).To(ContainElement("transcript"))
		})
	})

	Context("Piper ONNX + ONNX config detection", func() {
		It("maps the .onnx + .onnx.json pair to the piper backend", func() {
			modelDir := filepath.Join(tmpDir, "piper-amy")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "en_US-amy-medium.onnx"), []byte("fake"), 0644)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "en_US-amy-medium.onnx.json"), []byte("{}"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "piper-amy")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("piper"))
			Expect(cfg.Model).To(ContainSubstring("en_US-amy-medium.onnx"))
		})
	})

	Context("Silero VAD detection", func() {
		It("maps silero_vad.onnx to the silero-vad backend", func() {
			modelDir := filepath.Join(tmpDir, "silero")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(modelDir, "silero_vad.onnx"), []byte("fake"), 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "silero")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Backend).To(Equal("silero-vad"))
			Expect(cfg.Model).To(ContainSubstring("silero_vad.onnx"))
		})
	})

	Context("fallback", func() {
		It("returns error for empty directory", func() {
			modelDir := filepath.Join(tmpDir, "empty")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())

			_, err := importers.ImportLocalPath(modelDir, "empty")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not detect model format"))
		})
	})

	Context("description", func() {
		It("includes base model name in description", func() {
			modelDir := filepath.Join(tmpDir, "desc-test")
			Expect(os.MkdirAll(modelDir, 0755)).To(Succeed())

			adapterConfig := map[string]any{
				"base_model_name_or_path": "TinyLlama/TinyLlama-1.1B",
			}
			data, _ := json.Marshal(adapterConfig)
			Expect(os.WriteFile(filepath.Join(modelDir, "adapter_config.json"), data, 0644)).To(Succeed())

			cfg, err := importers.ImportLocalPath(modelDir, "desc-test")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Description).To(ContainSubstring("TinyLlama/TinyLlama-1.1B"))
			Expect(cfg.Description).To(ContainSubstring("Fine-tuned from"))
		})
	})
})
