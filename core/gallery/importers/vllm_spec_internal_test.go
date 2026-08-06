package importers

import (
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vllm-cpp speculative auto-config (importer)", func() {
	Context("applySpecFromConfigJSON", func() {
		It("enables mtp when the checkpoint declares an MTP head", func() {
			cfg := &config.ModelConfig{Name: "qwen3.5"}
			applySpecFromConfigJSON(cfg, []byte(`{
				"model_type": "qwen3_5_moe",
				"mtp_num_hidden_layers": 1
			}`), "huggingface://Qwen/Qwen3.5-A3B")
			Expect(cfg.EngineArgs).To(HaveKeyWithValue("speculative_config",
				map[string]any{"method": "mtp"}))
		})

		It("leaves a plain checkpoint untouched", func() {
			cfg := &config.ModelConfig{Name: "llama"}
			applySpecFromConfigJSON(cfg, []byte(`{"model_type": "llama"}`), "huggingface://meta/llama")
			Expect(cfg.EngineArgs).To(BeEmpty())
		})

		It("refuses to configure a DFlash draft as a servable model", func() {
			// The draft only proposes tokens; configuring it standalone would
			// produce a model that cannot load.
			cfg := &config.ModelConfig{Name: "dflash-draft"}
			applySpecFromConfigJSON(cfg, []byte(`{
				"model_type": "qwen3_dflash",
				"dflash_config": {"mask_token_id": 151666, "target_layer_ids": [0, 1]}
			}`), "huggingface://z-lab/Qwen3.6-27B-DFlash")
			Expect(cfg.EngineArgs).To(BeEmpty())
		})

		It("survives a config.json it cannot parse", func() {
			cfg := &config.ModelConfig{Name: "weird"}
			Expect(func() {
				applySpecFromConfigJSON(cfg, []byte(`<html>404</html>`), "huggingface://a/b")
			}).ToNot(Panic())
			Expect(cfg.EngineArgs).To(BeEmpty())
		})
	})

	Context("Import over a repository with an MTP head", func() {
		var restore func()

		BeforeEach(func() {
			original := specConfigFetcher
			restore = func() { specConfigFetcher = original }
		})
		AfterEach(func() { restore() })

		importWith := func(backend, configJSON string) string {
			specConfigFetcher = func(string) ([]byte, error) {
				return []byte(configJSON), nil
			}
			importer := &VLLMImporter{}
			out, err := importer.Import(Details{
				URI:         "huggingface://Qwen/Qwen3.5-A3B",
				Preferences: json.RawMessage(`{"backend": "` + backend + `"}`),
				HuggingFace: &hfapi.ModelDetails{ModelID: "Qwen/Qwen3.5-A3B"},
			})
			Expect(err).ToNot(HaveOccurred())
			return out.ConfigFile
		}

		It("emits engine_args.speculative_config for vllm-cpp", func() {
			yaml := importWith("vllm-cpp", `{"model_type":"qwen3_5_moe","mtp_num_hidden_layers":1}`)
			Expect(yaml).To(ContainSubstring("engine_args:"))
			Expect(yaml).To(ContainSubstring("speculative_config:"))
			Expect(yaml).To(ContainSubstring("method: mtp"))
		})

		It("emits nothing speculative for the python vllm backend", func() {
			// The python backend has its own speculative surface and its own
			// version-dependent MTP support; this hook is vllm-cpp only.
			yaml := importWith("vllm", `{"model_type":"qwen3_5_moe","mtp_num_hidden_layers":1}`)
			Expect(yaml).NotTo(ContainSubstring("speculative_config"))
		})

		It("emits nothing speculative when the probe fails", func() {
			specConfigFetcher = func(string) ([]byte, error) {
				return nil, errors.New("network down")
			}
			importer := &VLLMImporter{}
			out, err := importer.Import(Details{
				URI:         "huggingface://Qwen/Qwen3.5-A3B",
				Preferences: json.RawMessage(`{"backend": "vllm-cpp"}`),
				HuggingFace: &hfapi.ModelDetails{ModelID: "Qwen/Qwen3.5-A3B"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.ConfigFile).NotTo(ContainSubstring("speculative_config"))
		})
	})

	Context("vllmSpecProbeURL", func() {
		It("resolves the repository's config.json to an HTTPS URL", func() {
			url := vllmSpecProbeURL(Details{
				URI:         "huggingface://Qwen/Qwen3.5-A3B",
				HuggingFace: &hfapi.ModelDetails{ModelID: "Qwen/Qwen3.5-A3B"},
			})
			Expect(url).To(ContainSubstring("Qwen/Qwen3.5-A3B"))
			Expect(url).To(HaveSuffix("config.json"))
			Expect(url).To(HavePrefix("https://"))
		})

		It("skips the probe when there is no HuggingFace repo behind the import", func() {
			Expect(vllmSpecProbeURL(Details{URI: "/models/local-dir"})).To(BeEmpty())
		})
	})
})
