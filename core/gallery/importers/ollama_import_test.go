package importers_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/ollamatest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

func ggufLayer(extra string) []byte { return append([]byte("GGUF"), []byte(extra)...) }

// qwenStyle is a text model with a params layer, template and license.
func qwenStyle(params string) ollamatest.Model {
	return ollamatest.Model{Layers: []ollamatest.Layer{
		{MediaType: oci.OllamaMediaTypeModel, Data: ggufLayer("qwen weights")},
		{MediaType: "application/vnd.ollama.image.template", Data: []byte("{{ .Prompt }} SECRET-TEMPLATE")},
		{MediaType: "application/vnd.ollama.image.license", Data: []byte("SECRET-LICENSE-TEXT")},
		{MediaType: oci.OllamaMediaTypeParams, Data: []byte(params)},
	}}
}

const qwenParams = `{"repeat_penalty":1,"stop":["<|im_start|>","<|im_end|>"],"temperature":0.6,"top_k":20,"top_p":0.95}`

// gemmaStyle is a vision model: weights plus a separate projector.
func gemmaStyle() ollamatest.Model {
	m := qwenStyle(`{"temperature":1,"top_k":64,"top_p":0.95,"stop":["<end_of_turn>"],"num_ctx":8192}`)
	m.Layers = append(m.Layers, ollamatest.Layer{MediaType: oci.OllamaMediaTypeProjector, Data: ggufLayer("projector")})
	return m
}

func parse(cfg string) config.ModelConfig {
	var mc config.ModelConfig
	ExpectWithOffset(1, yaml.Unmarshal([]byte(cfg), &mc)).To(Succeed())
	return mc
}

var _ = Describe("Ollama import", func() {
	var reg *ollamatest.Registry

	BeforeEach(func() {
		reg = ollamatest.New()
		DeferCleanup(reg.Close)
		DeferCleanup(reg.Use())
	})

	Describe("DiscoverModelConfig", func() {
		It("imports a text model with its params and a derived name", func() {
			reg.Add("library/qwen3:8b", qwenStyle(qwenParams))

			cfg, err := importers.DiscoverModelConfig("ollama://qwen3:8b", nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(cfg.Name).To(Equal("qwen3-8b"))
			Expect(cfg.Description).To(Equal("Imported from Ollama: qwen3:8b"))
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("ollama://qwen3:8b"))
			Expect(cfg.Files[0].Filename).To(Equal("qwen3-8b.gguf"))

			mc := parse(cfg.ConfigFile)
			Expect(mc.Name).To(Equal("qwen3-8b"))
			Expect(mc.Backend).To(Equal("llama-cpp"))
			Expect(mc.Model).To(Equal("qwen3-8b.gguf"))
			Expect(mc.MMProj).To(BeEmpty())
			Expect(*mc.Temperature).To(BeNumerically("==", 0.6))
			Expect(*mc.TopK).To(Equal(20))
			Expect(*mc.TopP).To(BeNumerically("==", 0.95))
			Expect(mc.RepeatPenalty).To(BeNumerically("==", 1))
			Expect(mc.StopWords).To(Equal([]string{"<|im_start|>", "<|im_end|>"}))
			// The GGUF's own chat template stays.
			Expect(mc.TemplateConfig.UseTokenizerTemplate).To(BeTrue())
			Expect(mc.Options).To(ContainElement("use_jinja:true"))
		})

		It("does not import the template, the system prompt, the license or num_ctx", func() {
			reg.Add("library/gemma3:4b", gemmaStyle())
			cfg, err := importers.DiscoverModelConfig("ollama://gemma3:4b", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ConfigFile).NotTo(ContainSubstring("SECRET"))
			Expect(cfg.ConfigFile).NotTo(ContainSubstring("num_ctx"))
			Expect(cfg.ConfigFile).NotTo(ContainSubstring("8192"))
			Expect(cfg.PromptTemplates).To(BeEmpty())
			// The template and license blobs are never requested.
			for _, p := range reg.Paths() {
				Expect(p).NotTo(HaveSuffix(ollamatest.Digest([]byte("{{ .Prompt }} SECRET-TEMPLATE"))))
				Expect(p).NotTo(HaveSuffix(ollamatest.Digest([]byte("SECRET-LICENSE-TEXT"))))
			}
			Expect(reg.Paths()).To(ContainElement(HaveSuffix(ollamatest.Digest(ggufLayer("qwen weights")))))
		})

		It("fetches the projector of a vision model as a second file", func() {
			reg.Add("library/gemma3:4b", gemmaStyle())
			cfg, err := importers.DiscoverModelConfig("ollama://gemma3:4b", nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(cfg.Files).To(HaveLen(2))
			Expect(cfg.Files[0].Filename).To(Equal("gemma3-4b.gguf"))
			Expect(cfg.Files[1].Filename).To(Equal("gemma3-4b-mmproj.gguf"))
			Expect(cfg.Files[1].URI).To(Equal("ollama://gemma3:4b#projector"))
			mc := parse(cfg.ConfigFile)
			Expect(mc.MMProj).To(Equal("gemma3-4b-mmproj.gguf"))
			Expect(mc.Model).To(Equal("gemma3-4b.gguf"))
			Expect(mc.StopWords).To(Equal([]string{"<end_of_turn>"}))
		})

		It("ignores unknown keys and values of the wrong type", func() {
			reg.Add("library/odd:1b", qwenStyle(`{
				"temperature":"hot","top_k":2.5,"top_p":7,"min_p":0.05,
				"repeat_penalty":-1,"seed":42,"num_predict":-1,
				"stop":["ok",3],"mirostat":2,"num_gpu":99,"num_ctx":4096}`))
			cfg, err := importers.DiscoverModelConfig("ollama://odd:1b", nil)
			Expect(err).NotTo(HaveOccurred())
			mc := parse(cfg.ConfigFile)
			Expect(mc.Temperature).To(BeNil())
			Expect(mc.TopK).To(BeNil())
			Expect(mc.TopP).To(BeNil())
			Expect(*mc.MinP).To(BeNumerically("==", 0.05))
			Expect(mc.RepeatPenalty).To(BeZero())
			Expect(*mc.Seed).To(Equal(42))
			Expect(mc.Maxtokens).To(BeNil())
			Expect(mc.StopWords).To(BeEmpty())
			Expect(cfg.ConfigFile).NotTo(ContainSubstring("4096"))
			Expect(cfg.ConfigFile).NotTo(ContainSubstring("mirostat"))
		})

		It("maps num_predict to max_tokens", func() {
			reg.Add("library/cap:1b", qwenStyle(`{"num_predict":256}`))
			cfg, err := importers.DiscoverModelConfig("ollama://cap:1b", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(*parse(cfg.ConfigFile).Maxtokens).To(Equal(256))
		})

		It("lets the user's preferences win over the params layer", func() {
			reg.Add("library/qwen3:8b", qwenStyle(qwenParams))
			prefs := json.RawMessage(`{"name":"my-qwen","temperature":0.1,"top_k":5,"stopwords":["STOP"]}`)
			cfg, err := importers.DiscoverModelConfig("ollama://qwen3:8b", prefs)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Name).To(Equal("my-qwen"))
			mc := parse(cfg.ConfigFile)
			Expect(*mc.Temperature).To(BeNumerically("==", 0.1))
			Expect(*mc.TopK).To(Equal(5))
			Expect(mc.StopWords).To(Equal([]string{"STOP"}))
			// What the user did not set keeps the model's value.
			Expect(*mc.TopP).To(BeNumerically("==", 0.95))
		})

		It("fails with the model name when the registry has no such model", func() {
			_, err := importers.DiscoverModelConfig("ollama://ghost:7b", nil)
			Expect(err).To(MatchError(ContainSubstring(`"ghost:7b"`)))
		})

		It("fails when the manifest has no model layer", func() {
			reg.Add("library/empty:1b", ollamatest.Model{Layers: []ollamatest.Layer{
				{MediaType: oci.OllamaMediaTypeParams, Data: []byte(`{}`)},
			}})
			_, err := importers.DiscoverModelConfig("ollama://empty:1b", nil)
			Expect(err).To(MatchError(ContainSubstring("no model layer")))
		})

		It("explains that an MLX tag is stored as tensors", func() {
			reg.Add("library/qwen3.8:27b-mlx", ollamatest.Model{Layers: []ollamatest.Layer{
				{MediaType: "application/vnd.ollama.image.tensor", Data: []byte("t1")},
				{MediaType: "application/vnd.ollama.image.tensor", Data: []byte("t2")},
				{MediaType: "application/vnd.ollama.image.json", Data: []byte("{}")},
				{MediaType: oci.OllamaMediaTypeParams, Data: []byte(`{}`)},
			}})
			_, err := importers.DiscoverModelConfig("ollama://qwen3.8:27b-mlx", nil)
			Expect(err).To(MatchError(ContainSubstring(`"qwen3.8:27b-mlx"`)))
			Expect(err).To(MatchError(ContainSubstring("safetensors/MLX tensors")))
			Expect(err).To(MatchError(ContainSubstring("choose a GGUF tag")))
		})

		It("refuses a model that is not a GGUF file", func() {
			m := qwenStyle(qwenParams)
			m.Layers[0].Data = []byte("<html>hello</html>")
			reg.Add("library/web:1b", m)
			_, err := importers.DiscoverModelConfig("ollama://web:1b", nil)
			Expect(err).To(MatchError(ContainSubstring("this model is not a GGUF file")))
		})

		It("does not look the URI up on Hugging Face", func() {
			// Send every outgoing proxied request to a recorder. A Hugging
			// Face lookup would reach it as a CONNECT to huggingface.co.
			var hits atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Host, "huggingface") || strings.Contains(r.URL.Host, "huggingface") {
					hits.Add(1)
				}
				w.WriteHeader(http.StatusBadGateway)
			}))
			DeferCleanup(proxy.Close)
			GinkgoT().Setenv("HTTPS_PROXY", proxy.URL)
			GinkgoT().Setenv("HTTP_PROXY", proxy.URL)
			GinkgoT().Setenv("NO_PROXY", "127.0.0.1,localhost")

			reg.Add("library/qwen3:8b", qwenStyle(qwenParams))
			_, err := importers.DiscoverModelConfig("ollama://qwen3:8b", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(hits.Load()).To(BeZero())
		})
	})

	Describe("names", func() {
		DescribeTable("derives the model and file names",
			func(uri, name, file, description string) {
				reg.Add("library/qwen3:latest", qwenStyle(qwenParams))
				reg.Add("library/qwen3:8b", qwenStyle(qwenParams))
				reg.Add("someone/tool:v2.1", qwenStyle(qwenParams))
				cfg, err := importers.DiscoverModelConfig(uri, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Name).To(Equal(name))
				Expect(cfg.Files[0].Filename).To(Equal(file))
				Expect(cfg.Description).To(Equal(description))
				Expect(file).To(HaveSuffix(".gguf"))
				Expect(parse(cfg.ConfigFile).Model).To(Equal(file))
			},
			Entry("no tag", "ollama://qwen3", "qwen3", "qwen3.gguf", "Imported from Ollama: qwen3:latest"),
			Entry("tag", "ollama://qwen3:8b", "qwen3-8b", "qwen3-8b.gguf", "Imported from Ollama: qwen3:8b"),
			Entry("library prefix", "ollama://library/qwen3:8b", "qwen3-8b", "qwen3-8b.gguf", "Imported from Ollama: qwen3:8b"),
			Entry("user namespace", "ollama://someone/tool:v2.1", "someone-tool-v2.1", "someone-tool-v2.1.gguf", "Imported from Ollama: someone/tool:v2.1"),
		)
	})

	Describe("importer choice", func() {
		DescribeTable("the llama-cpp importer claims every ollama:// URI",
			func(uri string) {
				p, err := importers.PreviewImport(uri, nil, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.Importer).To(Equal("llama-cpp"))
			},
			Entry("plain", "ollama://qwen3:8b"),
			Entry("tts-looking name", "ollama://kokoro-tts:latest"),
			Entry("whisper-looking name", "ollama://whisper-large:v3"),
			Entry("embedding name", "ollama://sentence-transformers/all-minilm:latest"),
			Entry("reranker name", "ollama://reranker:1b"),
			Entry("user namespace", "ollama://someone/vllm-model:1b"),
		)

		It("does not read an ollama:// URI as a Hugging Face owner and repo", func() {
			_, _, ok := importers.HFOwnerRepoFromURI("ollama://qwen3:8b")
			Expect(ok).To(BeFalse())
			_, _, ok = importers.HFOwnerRepoFromURI("oci://quay.io/org/image:tag")
			Expect(ok).To(BeFalse())
			owner, repo, ok := importers.HFOwnerRepoFromURI("hf://owner/repo")
			Expect(ok).To(BeTrue())
			Expect(owner + "/" + repo).To(Equal("owner/repo"))
		})
	})

	Describe("other OCI imports", func() {
		It("keeps the existing naming for an oci:// image", func() {
			cfg, err := importers.DiscoverModelConfig("oci://quay.io/org/model:v1", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Files).To(HaveLen(1))
			Expect(cfg.Files[0].URI).To(Equal("oci://quay.io/org/model:v1"))
			Expect(cfg.Files[0].Filename).To(Equal("quay.io__org__model__v1"))
			Expect(parse(cfg.ConfigFile).Model).To(Equal("quay.io__org__model__v1"))
			Expect(cfg.Description).To(Equal("Imported from oci://quay.io/org/model:v1"))
		})
	})

	// A registry that does not answer must not hang the import silently.
	It("reports an unreachable registry", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := l.Addr().String()
		Expect(l.Close()).To(Succeed())
		DeferCleanup(oci.UseOllamaRegistry(addr, true))
		_, err = importers.DiscoverModelConfig("ollama://qwen3:8b", nil)
		Expect(err).To(MatchError(ContainSubstring(`"qwen3:8b"`)))
	})
})
