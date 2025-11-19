package config

import (
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test cases for config related functions", func() {
	Context("Test Read configuration functions", func() {
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`backend: "../foo-bar"
name: "foo"
parameters:
  model: "foo-bar"
known_usecases:
- chat
- COMPLETION
`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readModelConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			valid, err := config.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(config.KnownUsecases).ToNot(BeNil())
		})
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`name: bar-baz
backend: "foo-bar"
parameters:
  model: "foo-bar"`)
			Expect(err).ToNot(HaveOccurred())
			config, err := readModelConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("bar-baz"))
			valid, err := config.Validate()
			Expect(err).To(BeNil())
			Expect(valid).To(BeTrue())

			// download https://raw.githubusercontent.com/mudler/LocalAI/v2.25.0/embedded/models/hermes-2-pro-mistral.yaml
			httpClient := http.Client{}
			resp, err := httpClient.Get("https://raw.githubusercontent.com/mudler/LocalAI/v2.25.0/embedded/models/hermes-2-pro-mistral.yaml")
			Expect(err).To(BeNil())
			defer resp.Body.Close()
			tmp, err = os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = io.Copy(tmp, resp.Body)
			Expect(err).To(BeNil())
			config, err = readModelConfigFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("hermes-2-pro-mistral"))
			valid, err = config.Validate()
			Expect(err).To(BeNil())
			Expect(valid).To(BeTrue())
		})
	})
	It("Properly handles backend usecase matching", func() {

		a := ModelConfig{
			Name: "a",
		}
		Expect(a.HasUsecases(FLAG_ANY)).To(BeTrue()) // FLAG_ANY just means the config _exists_ essentially.

		b := ModelConfig{
			Name:    "b",
			Backend: "stablediffusion",
		}
		Expect(b.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_IMAGE)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_CHAT)).To(BeFalse())

		c := ModelConfig{
			Name:    "c",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat: "chat",
			},
		}
		Expect(c.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(c.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_COMPLETION)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_CHAT)).To(BeTrue())

		d := ModelConfig{
			Name:    "d",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat:       "chat",
				Completion: "completion",
			},
		}
		Expect(d.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(d.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_CHAT)).To(BeTrue())

		trueValue := true
		e := ModelConfig{
			Name:    "e",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Completion: "completion",
			},
			Embeddings: &trueValue,
		}

		Expect(e.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_CHAT)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_EMBEDDINGS)).To(BeTrue())

		f := ModelConfig{
			Name:    "f",
			Backend: "piper",
		}
		Expect(f.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_CHAT)).To(BeFalse())

		g := ModelConfig{
			Name:    "g",
			Backend: "whisper",
		}
		Expect(g.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TTS)).To(BeFalse())

		h := ModelConfig{
			Name:    "h",
			Backend: "transformers-musicgen",
		}
		Expect(h.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_TRANSCRIPT)).To(BeFalse())
		Expect(h.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_SOUND_GENERATION)).To(BeTrue())

		knownUsecases := FLAG_CHAT | FLAG_COMPLETION
		i := ModelConfig{
			Name:    "i",
			Backend: "whisper",
			// Earlier test checks parsing, this just needs to set final values
			KnownUsecases: &knownUsecases,
		}
		Expect(i.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TTS)).To(BeFalse())
		Expect(i.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_CHAT)).To(BeTrue())
	})

	It("Handles multiple configs with same model file but different names", func() {
		// Create a temporary directory for test configs
		tmpDir, err := os.MkdirTemp("", "config_test_*")
		Expect(err).To(BeNil())
		defer os.RemoveAll(tmpDir)

		// Write first config without MCP
		config1Path := tmpDir + "/model-without-mcp.yaml"
		err = os.WriteFile(config1Path, []byte(`name: model-without-mcp
backend: llama-cpp
parameters:
  model: shared-model.gguf
`), 0644)
		Expect(err).To(BeNil())

		// Write second config with MCP
		config2Path := tmpDir + "/model-with-mcp.yaml"
		err = os.WriteFile(config2Path, []byte(`name: model-with-mcp
backend: llama-cpp
parameters:
  model: shared-model.gguf
mcp:
  stdio: |
    mcpServers:
      test:
        command: echo
        args: ["hello"]
`), 0644)
		Expect(err).To(BeNil())

		// Load all configs
		loader := NewModelConfigLoader(tmpDir)
		err = loader.LoadModelConfigsFromPath(tmpDir)
		Expect(err).To(BeNil())

		// Verify both configs are loaded
		cfg1, exists1 := loader.GetModelConfig("model-without-mcp")
		Expect(exists1).To(BeTrue())
		Expect(cfg1.Name).To(Equal("model-without-mcp"))
		Expect(cfg1.Model).To(Equal("shared-model.gguf"))
		Expect(cfg1.MCP.Stdio).To(Equal(""))
		Expect(cfg1.MCP.Servers).To(Equal(""))

		cfg2, exists2 := loader.GetModelConfig("model-with-mcp")
		Expect(exists2).To(BeTrue())
		Expect(cfg2.Name).To(Equal("model-with-mcp"))
		Expect(cfg2.Model).To(Equal("shared-model.gguf"))
		Expect(cfg2.MCP.Stdio).ToNot(Equal(""))

		// Verify both configs are in the list
		allConfigs := loader.GetAllModelsConfigs()
		Expect(len(allConfigs)).To(Equal(2))

		// Find each config in the list
		foundWithoutMCP := false
		foundWithMCP := false
		for _, cfg := range allConfigs {
			if cfg.Name == "model-without-mcp" {
				foundWithoutMCP = true
				Expect(cfg.Model).To(Equal("shared-model.gguf"))
				Expect(cfg.MCP.Stdio).To(Equal(""))
			}
			if cfg.Name == "model-with-mcp" {
				foundWithMCP = true
				Expect(cfg.Model).To(Equal("shared-model.gguf"))
				Expect(cfg.MCP.Stdio).ToNot(Equal(""))
			}
		}
		Expect(foundWithoutMCP).To(BeTrue())
		Expect(foundWithMCP).To(BeTrue())
	})
})
