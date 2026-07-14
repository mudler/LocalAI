package config

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixtures inlines what used to live under tests/models_fixtures/. Owning the
// YAMLs here keeps the test self-contained: no env-var plumbing, no shared
// directory the rest of the repo can stomp on.
var fixtures = map[string]string{
	"config.yaml": `- name: list1
  parameters:
    model: testmodel.ggml
    top_p: 80
    top_k: 0.9
    temperature: 0.1
  context_size: 200
  stopwords:
  - "HUMAN:"
  - "### Response:"
  roles:
    user: "HUMAN:"
    system: "GPT:"
  template:
    completion: completion
    chat: ggml-gpt4all-j
- name: list2
  parameters:
    top_p: 80
    top_k: 0.9
    temperature: 0.1
    model: testmodel.ggml
  context_size: 200
  stopwords:
  - "HUMAN:"
  - "### Response:"
  roles:
    user: "HUMAN:"
    system: "GPT:"
  template:
    completion: completion
    chat: ggml-gpt4all-j
`,
	"embeddings.yaml": `name: text-embedding-ada-002
embeddings: true
parameters:
  model: huggingface://hugging-quants/Llama-3.2-1B-Instruct-Q4_K_M-GGUF/llama-3.2-1b-instruct-q4_k_m.gguf
`,
	"grpc.yaml": `name: code-search-ada-code-001
backend: sentencetransformers
embeddings: true
parameters:
  model: all-MiniLM-L6-v2
`,
	"rwkv.yaml": `name: rwkv_test
parameters:
  model: huggingface://bartowski/rwkv-6-world-7b-GGUF/rwkv-6-world-7b-Q4_K_M.gguf
  top_k: 80
  temperature: 0.9
  max_tokens: 4098
  top_p: 0.8
context_size: 4098

roles:
  user: "User: "
  system: "System: "
  assistant: "Assistant: "

stopwords:
- 'Assistant:'
- '<s>'

template:
  chat: |
    {{.Input}}
    Assistant:
  completion: |
    {{.Input}}
`,
	"whisper.yaml": `name: whisper-1
backend: whisper
parameters:
  model: whisper-en
`,
}

var _ = Describe("Test cases for config related functions", func() {

	var (
		modelsPath string
		configFile string
	)

	Context("Test Read configuration functions", func() {
		BeforeEach(func() {
			tmp, err := os.MkdirTemp("", "model-config-fixtures-")
			Expect(err).ToNot(HaveOccurred())
			modelsPath = tmp
			for name, body := range fixtures {
				Expect(os.WriteFile(filepath.Join(modelsPath, name), []byte(body), 0644)).To(Succeed())
			}
			configFile = filepath.Join(modelsPath, "config.yaml")
		})

		AfterEach(func() {
			Expect(os.RemoveAll(modelsPath)).To(Succeed())
		})

		It("Test readConfigFile", func() {
			config, err := readModelConfigsFromFile(configFile)
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config[0].Name).To(Equal("list1"))
			Expect(config[1].Name).To(Equal("list2"))
		})

		It("Test LoadConfigs", func() {

			bcl := NewModelConfigLoader(modelsPath)
			err := bcl.LoadModelConfigsFromPath(modelsPath)

			Expect(err).To(BeNil())
			configs := bcl.GetAllModelsConfigs()
			loadedModelNames := []string{}
			for _, v := range configs {
				loadedModelNames = append(loadedModelNames, v.Name)
			}
			Expect(configs).ToNot(BeNil())

			Expect(loadedModelNames).To(ContainElements("code-search-ada-code-001"))

			// config should includes text-embedding-ada-002 models's api.config
			Expect(loadedModelNames).To(ContainElements("text-embedding-ada-002"))

			// config should includes rwkv_test models's api.config
			Expect(loadedModelNames).To(ContainElements("rwkv_test"))

			// config should includes whisper-1 models's api.config
			Expect(loadedModelNames).To(ContainElements("whisper-1"))
		})

		It("Test new loadconfig", func() {

			bcl := NewModelConfigLoader(modelsPath)
			err := bcl.LoadModelConfigsFromPath(modelsPath)
			Expect(err).To(BeNil())
			configs := bcl.GetAllModelsConfigs()
			loadedModelNames := []string{}
			for _, v := range configs {
				loadedModelNames = append(loadedModelNames, v.Name)
			}
			Expect(configs).ToNot(BeNil())
			totalModels := len(loadedModelNames)

			Expect(loadedModelNames).To(ContainElements("code-search-ada-code-001"))

			// config should includes text-embedding-ada-002 models's api.config
			Expect(loadedModelNames).To(ContainElements("text-embedding-ada-002"))

			// config should includes rwkv_test models's api.config
			Expect(loadedModelNames).To(ContainElements("rwkv_test"))

			// config should includes whisper-1 models's api.config
			Expect(loadedModelNames).To(ContainElements("whisper-1"))

			// create a temp directory and store a temporary model
			tmpdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpdir)

			// create a temporary model
			model := `name: "test-model"
description: "test model"
options:
- foo
- bar
- baz
`
			modelFile := tmpdir + "/test-model.yaml"
			err = os.WriteFile(modelFile, []byte(model), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = bcl.LoadModelConfigsFromPath(tmpdir)
			Expect(err).ToNot(HaveOccurred())

			configs = bcl.GetAllModelsConfigs()
			Expect(len(configs)).ToNot(Equal(totalModels))

			loadedModelNames = []string{}
			var testModel ModelConfig
			for _, v := range configs {
				loadedModelNames = append(loadedModelNames, v.Name)
				if v.Name == "test-model" {
					testModel = v
				}
			}
			Expect(loadedModelNames).To(ContainElements("test-model"))
			Expect(testModel.Description).To(Equal("test model"))
			Expect(testModel.Options).To(ContainElements("foo", "bar", "baz"))

		})

		It("Only loads files ending with yaml or yml", func() {
			tmpdir, err := os.MkdirTemp("", "model-config-loader")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpdir)

			err = os.WriteFile(filepath.Join(tmpdir, "foo.yaml"), []byte(
				`name: "foo-model"
description: "formal config"
backend: "llama-cpp"
parameters:
  model: "foo.gguf"
`), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(filepath.Join(tmpdir, "foo.yaml.bak"), []byte(
				`name: "foo-model"
description: "backup config"
backend: "llama-cpp"
parameters:
  model: "foo-backup.gguf"
`), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(filepath.Join(tmpdir, "foo.yaml.bak.123"), []byte(
				`name: "foo-backup-only"
description: "timestamped backup config"
backend: "llama-cpp"
parameters:
  model: "foo-timestamped.gguf"
`), 0644)
			Expect(err).ToNot(HaveOccurred())

			bcl := NewModelConfigLoader(tmpdir)
			err = bcl.LoadModelConfigsFromPath(tmpdir)
			Expect(err).ToNot(HaveOccurred())

			configs := bcl.GetAllModelsConfigs()
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Name).To(Equal("foo-model"))
			Expect(configs[0].Description).To(Equal("formal config"))

			_, exists := bcl.GetModelConfig("foo-backup-only")
			Expect(exists).To(BeFalse())
		})
	})
})
