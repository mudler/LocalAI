package oci_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/ollamatest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	ggufBytes = append([]byte("GGUF"), []byte(strings.Repeat("weights", 100))...)
	paramsDoc = []byte(`{"repeat_penalty":1,"stop":["<|im_start|>","<|im_end|>"],"temperature":0.6,"top_k":20,"top_p":0.95}`)
)

// tensorModel is shaped like an MLX or nvfp4 tag: tensor and json layers, a
// license and params, and no model layer.
func tensorModel() ollamatest.Model {
	return ollamatest.Model{Layers: []ollamatest.Layer{
		{MediaType: "application/vnd.ollama.image.tensor", Data: []byte("tensor-1")},
		{MediaType: "application/vnd.ollama.image.tensor", Data: []byte("tensor-2")},
		{MediaType: "application/vnd.ollama.image.tensor", Data: []byte("tensor-3")},
		{MediaType: "application/vnd.ollama.image.json", Data: []byte(`{"a":1}`)},
		{MediaType: "application/vnd.ollama.image.json", Data: []byte(`{"b":2}`)},
		{MediaType: "application/vnd.ollama.image.license", Data: []byte("license text")},
		{MediaType: OllamaMediaTypeParams, Data: paramsDoc},
	}}
}

func textModel() ollamatest.Model {
	return ollamatest.Model{Layers: []ollamatest.Layer{
		{MediaType: OllamaMediaTypeModel, Data: ggufBytes},
		{MediaType: "application/vnd.ollama.image.template", Data: []byte("{{ .Prompt }}")},
		{MediaType: "application/vnd.ollama.image.license", Data: []byte("license text")},
		{MediaType: OllamaMediaTypeParams, Data: paramsDoc},
	}}
}

var _ = Describe("Ollama registry", func() {
	var (
		reg *ollamatest.Registry
		dir string
	)

	BeforeEach(func() {
		reg = ollamatest.New()
		DeferCleanup(reg.Close)
		DeferCleanup(reg.Use())
		dir = GinkgoT().TempDir()
	})

	Describe("manifest", func() {
		It("reports a missing model by name and tag", func() {
			_, err := OllamaModelManifest("qwen3:8b")
			Expect(err).To(MatchError(ContainSubstring(`"qwen3:8b"`)))
			Expect(err).To(MatchError(ContainSubstring("404")))
		})

		It("reports a registry error status", func() {
			reg.Add("library/qwen3:8b", ollamatest.Model{ManifestStatus: 503})
			_, err := OllamaModelManifest("qwen3:8b")
			Expect(err).To(MatchError(ContainSubstring(`"qwen3:8b"`)))
			Expect(err).To(MatchError(ContainSubstring("503")))
		})

		It("rejects a body that is not a manifest", func() {
			reg.Add("library/qwen3:8b", ollamatest.Model{ManifestBody: []byte("<html>not json</html>")})
			_, err := OllamaModelManifest("qwen3:8b")
			Expect(err).To(MatchError(ContainSubstring(`"qwen3:8b"`)))
			Expect(err).To(MatchError(ContainSubstring("valid manifest")))
		})

		It("rejects a manifest without layers", func() {
			reg.Add("library/qwen3:8b", ollamatest.Model{ManifestBody: []byte(`{"schemaVersion":2}`)})
			_, err := OllamaModelManifest("qwen3:8b")
			Expect(err).To(MatchError(ContainSubstring("valid manifest")))
		})

		It("fails, not an empty digest, when the model has no model layer", func() {
			reg.Add("library/qwen3:8b", ollamatest.Model{Layers: []ollamatest.Layer{
				{MediaType: OllamaMediaTypeParams, Data: paramsDoc},
			}})
			digest, err := OllamaModelBlob("qwen3:8b")
			Expect(err).To(MatchError(ContainSubstring(`"qwen3:8b"`)))
			Expect(err).To(MatchError(ContainSubstring("no model layer")))
			Expect(digest).To(BeEmpty())
		})

		It("explains that a tensor-only model is not a GGUF file", func() {
			reg.Add("library/qwen3.8:27b-mlx", tensorModel())
			digest, err := OllamaModelBlob("qwen3.8:27b-mlx")
			Expect(digest).To(BeEmpty())
			Expect(err).To(MatchError(ContainSubstring(`"qwen3.8:27b-mlx"`)))
			Expect(err).To(MatchError(ContainSubstring("safetensors/MLX tensors")))
			Expect(err).To(MatchError(ContainSubstring("not a GGUF file")))
			Expect(err).To(MatchError(ContainSubstring("llama.cpp")))
			Expect(err).To(MatchError(ContainSubstring("choose a GGUF tag")))
			Expect(err).NotTo(MatchError(ContainSubstring("no model layer")))
		})

		It("names a user namespace in the error", func() {
			_, err := OllamaModelManifest("someone/tool:v2")
			Expect(err).To(MatchError(ContainSubstring(`"someone/tool:v2"`)))
		})
	})

	Describe("downloading", func() {
		It("downloads the model layer and the projector layer", func() {
			m := textModel()
			projector := append([]byte("GGUF"), []byte("projector")...)
			m.Layers = append(m.Layers, ollamatest.Layer{MediaType: OllamaMediaTypeProjector, Data: projector})
			reg.Add("library/gemma3:4b", m)

			model := filepath.Join(dir, "model.gguf")
			Expect(OllamaFetchModel(context.Background(), "gemma3:4b", model, nil)).To(Succeed())
			Expect(os.ReadFile(model)).To(Equal(ggufBytes))

			mm := filepath.Join(dir, "mmproj.gguf")
			Expect(OllamaFetchModel(context.Background(), "gemma3:4b#projector", mm, nil)).To(Succeed())
			Expect(os.ReadFile(mm)).To(Equal(projector))
		})

		It("fails on a projector request when the model has none", func() {
			reg.Add("library/qwen3:8b", textModel())
			mm := filepath.Join(dir, "mmproj.gguf")
			err := OllamaFetchModel(context.Background(), "qwen3:8b#projector", mm, nil)
			Expect(err).To(MatchError(ContainSubstring("no projector layer")))
			Expect(mm).NotTo(BeAnExistingFile())
		})

		It("does not leave a file when the manifest is missing", func() {
			dst := filepath.Join(dir, "model.gguf")
			Expect(OllamaFetchModel(context.Background(), "nope:1b", dst, nil)).NotTo(Succeed())
			Expect(dst).NotTo(BeAnExistingFile())
		})
	})

	Describe("blob verification", func() {
		fetch := func(name string, layer ollamatest.Layer) (string, error) {
			m := textModel()
			m.Layers[0] = layer
			reg.Add("library/"+name+":1b", m)
			dst := filepath.Join(dir, name+".gguf")
			return dst, OllamaFetchModel(context.Background(), name+":1b", dst, nil)
		}

		It("accepts a blob whose digest matches", func() {
			dst, err := fetch("good", ollamatest.Layer{MediaType: OllamaMediaTypeModel, Data: ggufBytes})
			Expect(err).NotTo(HaveOccurred())
			Expect(dst).To(BeAnExistingFile())
		})

		It("fails and removes the file when the digest does not match", func() {
			corrupt := append([]byte(nil), ggufBytes...)
			corrupt[10] ^= 0xff
			dst, err := fetch("bad", ollamatest.Layer{MediaType: OllamaMediaTypeModel, Data: ggufBytes, Served: corrupt})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("verification"))
			Expect(dst).NotTo(BeAnExistingFile())
		})

		It("fails and removes the file when the registry sends extra bytes", func() {
			dst, err := fetch("long", ollamatest.Layer{MediaType: OllamaMediaTypeModel, Data: ggufBytes, Served: append(append([]byte(nil), ggufBytes...), 'x')})
			Expect(err).To(HaveOccurred())
			Expect(dst).NotTo(BeAnExistingFile())
		})

		It("fails and removes the file when the blob is truncated", func() {
			dst, err := fetch("short", ollamatest.Layer{MediaType: OllamaMediaTypeModel, Data: ggufBytes, Truncate: true})
			Expect(err).To(HaveOccurred())
			Expect(dst).NotTo(BeAnExistingFile())
		})

		It("verifies any registry blob, not only Ollama ones", func() {
			corrupt := append([]byte(nil), ggufBytes...)
			corrupt[5] ^= 0xff
			m := textModel()
			m.Layers[0] = ollamatest.Layer{MediaType: "application/octet-stream", Data: ggufBytes, Served: corrupt}
			reg.Add("library/plain:1b", m)
			dst := filepath.Join(dir, "plain")
			err := FetchImageBlob(context.Background(), reg.Host()+"/library/plain", ollamatest.Digest(ggufBytes), dst, nil)
			Expect(err).To(HaveOccurred())
			Expect(dst).NotTo(BeAnExistingFile())

			good := filepath.Join(dir, "good")
			m.Layers[0].Served = nil
			reg.Add("library/plain:1b", m)
			Expect(FetchImageBlob(context.Background(), reg.Host()+"/library/plain", ollamatest.Digest(ggufBytes), good, nil)).To(Succeed())
			Expect(os.ReadFile(good)).To(Equal(ggufBytes))
		})
	})

	Describe("CheckGGUF", func() {
		It("accepts a GGUF file", func() {
			p := filepath.Join(dir, "a.gguf")
			Expect(os.WriteFile(p, ggufBytes, 0o600)).To(Succeed())
			Expect(CheckGGUF(p)).To(Succeed())
			Expect(p).To(BeAnExistingFile())
		})

		It("refuses and removes a file that is not GGUF", func() {
			p := filepath.Join(dir, "b.gguf")
			Expect(os.WriteFile(p, []byte("PK\x03\x04 zip"), 0o600)).To(Succeed())
			err := CheckGGUF(p)
			Expect(err).To(MatchError(ContainSubstring("this model is not a GGUF file")))
			Expect(p).NotTo(BeAnExistingFile())
		})

		It("refuses and removes an empty file", func() {
			p := filepath.Join(dir, "c.gguf")
			Expect(os.WriteFile(p, nil, 0o600)).To(Succeed())
			Expect(CheckGGUF(p)).To(MatchError(ContainSubstring("not a GGUF")))
			Expect(p).NotTo(BeAnExistingFile())
		})
	})

	Describe("OllamaDescribeModel", func() {
		It("reads the params layer and sees no projector on a text model", func() {
			reg.Add("library/qwen3:8b", textModel())
			info, err := OllamaDescribeModel(context.Background(), "qwen3:8b")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.HasProjector).To(BeFalse())
			Expect(info.Params).To(HaveKeyWithValue("temperature", 0.6))
			Expect(info.Params).To(HaveKey("stop"))
		})

		It("sees the projector of a vision model", func() {
			m := textModel()
			m.Layers = append(m.Layers, ollamatest.Layer{MediaType: OllamaMediaTypeProjector, Data: append([]byte("GGUF"), 1)})
			reg.Add("library/gemma3:4b", m)
			info, err := OllamaDescribeModel(context.Background(), "gemma3:4b")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.HasProjector).To(BeTrue())
		})

		It("refuses weights that are not a GGUF file", func() {
			m := textModel()
			m.Layers[0].Data = []byte("{\"not\":\"gguf\"}")
			reg.Add("library/odd:1b", m)
			_, err := OllamaDescribeModel(context.Background(), "odd:1b")
			Expect(err).To(MatchError(ContainSubstring("this model is not a GGUF file")))
		})

		It("refuses a tensor-only model before any download", func() {
			reg.Add("library/qwen3.8:27b-nvfp4", tensorModel())
			_, err := OllamaDescribeModel(context.Background(), "qwen3.8:27b-nvfp4")
			Expect(err).To(MatchError(ContainSubstring(`"qwen3.8:27b-nvfp4"`)))
			Expect(err).To(MatchError(ContainSubstring("safetensors/MLX tensors")))
			Expect(err).To(MatchError(ContainSubstring("choose a GGUF tag")))
			for _, p := range reg.Paths() {
				Expect(p).NotTo(ContainSubstring("/blobs/"))
			}
		})

		It("works without a params layer", func() {
			m := textModel()
			m.Layers = m.Layers[:3]
			reg.Add("library/plain:1b", m)
			info, err := OllamaDescribeModel(context.Background(), "plain:1b")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Params).To(BeNil())
		})

		It("ignores a params layer that is not a JSON object", func() {
			m := textModel()
			m.Layers[3].Data = []byte(`["not","an","object"]`)
			reg.Add("library/odd:2b", m)
			info, err := OllamaDescribeModel(context.Background(), "odd:2b")
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Params).To(BeNil())
		})

		It("refuses an oversized params layer", func() {
			m := textModel()
			m.Layers[3].Data = []byte(`{"stop":["` + strings.Repeat("a", 70<<10) + `"]}`)
			reg.Add("library/big:1b", m)
			_, err := OllamaDescribeModel(context.Background(), "big:1b")
			Expect(err).To(MatchError(ContainSubstring("more than")))
		})

		It("fails for a missing model", func() {
			_, err := OllamaDescribeModel(context.Background(), "ghost:1b")
			Expect(err).To(MatchError(ContainSubstring(`"ghost:1b"`)))
		})
	})
})
