package modelsearch_test

import (
	stdctx "context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	"github.com/mudler/LocalAI/pkg/modelsearch"
	"github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// Shape of an Ollama registry manifest of a text model.
	ollamaTextManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
 "config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:c0","size":488},
 "layers":[
  {"mediaType":"application/vnd.ollama.image.model","digest":"sha256:aa11","size":5200000000},
  {"mediaType":"application/vnd.ollama.image.template","digest":"sha256:bb22","size":1500},
  {"mediaType":"application/vnd.ollama.image.license","digest":"sha256:cc33","size":11400},
  {"mediaType":"application/vnd.ollama.image.params","digest":"sha256:dd44","size":210}
 ]}`

	// The same with a vision projector and a system prompt.
	ollamaVisionManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
 "config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:c1","size":500},
 "layers":[
  {"mediaType":"application/vnd.ollama.image.model","digest":"sha256:ee55","size":3300000000},
  {"mediaType":"application/vnd.ollama.image.projector","digest":"sha256:ff66","size":851000000},
  {"mediaType":"application/vnd.ollama.image.system","digest":"sha256:0077","size":90},
  {"mediaType":"application/vnd.ollama.image.template","digest":"sha256:bb22","size":1500}
 ]}`
)

type fakeOllama struct {
	*httptest.Server
	hits   atomic.Int32
	accept atomic.Value
	status atomic.Int32
}

func newFakeOllama() *fakeOllama {
	f := &fakeOllama{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.accept.Store(r.Header.Get("Accept"))
		if s := int(f.status.Load()); s != 0 {
			w.WriteHeader(s)
			return
		}
		switch r.URL.Path {
		case "/v2/library/qwen3/manifests/8b", "/v2/library/qwen3/manifests/latest":
			w.Header().Set("Docker-Content-Digest", "sha256:abcd")
			_, _ = w.Write([]byte(ollamaTextManifest))
		case "/v2/acme/vision/manifests/4b":
			_, _ = w.Write([]byte(ollamaVisionManifest))
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func ollamaInspector(f *fakeOllama, o modelsearch.InspectorOptions) *modelsearch.Inspector {
	o.OllamaBase = f.URL
	o.Client = f.Client()
	return modelsearch.NewInspector(o)
}

var _ = Describe("Inspector with an Ollama repository", func() {
	var reg *fakeOllama
	BeforeEach(func() { reg = newFakeOllama() })
	AfterEach(func() { reg.Close() })
	ctx := stdctx.Background()

	It("lists the model layer as the one gguf file and skips the text layers", func() {
		rf, err := ollamaInspector(reg, modelsearch.InspectorOptions{}).Files(ctx, "ollama://qwen3:8b")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.accept.Load()).To(Equal("application/vnd.docker.distribution.manifest.v2+json"))
		Expect(rf.Provider).To(Equal(modelsearch.OllamaName))
		Expect(rf.ID).To(Equal("library/qwen3:8b"))
		Expect(rf.URI).To(Equal("ollama://qwen3:8b"))
		Expect(rf.Revision).To(Equal("sha256:abcd"))
		Expect(rf.Files).To(Equal([]modelsearch.RepoFile{
			{Path: "qwen3-8b.gguf", Size: 5_200_000_000, SHA256: "aa11", Kind: "gguf"},
		}))
		Expect(rf.Variants).To(HaveLen(1))
		Expect(rf.Variants[0].File).To(Equal("qwen3-8b.gguf"))
		Expect(rf.Variants[0].SizeBytes).To(Equal(int64(5_200_000_000)))
		Expect(rf.Variants[0].Recommended).To(BeTrue())
		Expect(rf.SizeKnown).To(BeTrue())
	})

	It("lists the projector layer as an mmproj file, outside the variant", func() {
		rf, err := ollamaInspector(reg, modelsearch.InspectorOptions{}).Files(ctx, "ollama://acme/vision:4b")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.URI).To(Equal("ollama://acme/vision:4b"))
		Expect(rf.Files).To(Equal([]modelsearch.RepoFile{
			{Path: "vision-4b.gguf", Size: 3_300_000_000, SHA256: "ee55", Kind: "gguf"},
			{Path: "vision-4b-mmproj.gguf", Size: 851_000_000, SHA256: "ff66", Kind: "mmproj"},
		}))
		Expect(rf.Variants).To(HaveLen(1))
		Expect(rf.Variants[0].Files).To(Equal([]string{"vision-4b.gguf"}))
		Expect(rf.WeightBytes()).To(Equal(int64(3_300_000_000)))
	})

	It("reads the latest tag for a bare name and caches the listing", func() {
		in := ollamaInspector(reg, modelsearch.InspectorOptions{})
		rf, err := in.Files(ctx, "ollama://qwen3")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.ID).To(Equal("library/qwen3:latest"))
		_, err = in.Files(ctx, "ollama://library/qwen3")
		Expect(err).ToNot(HaveOccurred())
		Expect(reg.hits.Load()).To(Equal(int32(1)))
	})

	It("maps the registry answers to the same errors as the other providers", func() {
		in := ollamaInspector(reg, modelsearch.InspectorOptions{})
		_, err := in.Files(ctx, "ollama://nothing")
		Expect(errors.Is(err, modelsearch.ErrRepoNotFound)).To(BeTrue())
		reg.status.Store(http.StatusTooManyRequests)
		_, err = in.Files(ctx, "ollama://qwen3:8b")
		Expect(errors.Is(err, modelsearch.ErrRateLimited)).To(BeTrue())
		reg.status.Store(http.StatusInternalServerError)
		_, err = ollamaInspector(reg, modelsearch.InspectorOptions{}).Files(ctx, "ollama://qwen3:8b")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, modelsearch.ErrRepoNotFound)).To(BeFalse())
	})

	It("rejects a bad uri before any request", func() {
		_, err := ollamaInspector(reg, modelsearch.InspectorOptions{}).Files(ctx, "ollama://a/../b")
		Expect(errors.Is(err, modelsearch.ErrBadRepo)).To(BeTrue())
		Expect(reg.hits.Load()).To(BeZero())
	})

	Describe("Estimate", func() {
		meta := &vram.GGUFMeta{BlockCount: 36, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8, MaximumContextLength: 40960}

		It("estimates the model layer, reading the header from the blob address", func() {
			var read []string
			in := ollamaInspector(reg, modelsearch.InspectorOptions{
				ReadMeta: func(_ stdctx.Context, uri string) (*vram.GGUFMeta, error) {
					read = append(read, uri)
					return meta, nil
				},
			})
			r, err := in.Estimate(ctx, "ollama://acme/vision:4b", "", []uint32{8192})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.File).To(Equal("vision-4b.gguf"))
			Expect(r.Quantization).To(Equal("4b"))
			Expect(r.Source).To(Equal("gguf-header"))
			Expect(r.SizeBytes).To(Equal(uint64(3_300_000_000)), "the projector is not counted")
			Expect(r.Estimates).To(HaveKey("8192"))
			Expect(read).To(Equal([]string{reg.URL + "/v2/acme/vision/blobs/sha256:ee55"}))
		})

		It("falls back to the file size when the header cannot be read", func() {
			in := ollamaInspector(reg, modelsearch.InspectorOptions{
				ReadMeta: func(stdctx.Context, string) (*vram.GGUFMeta, error) { return nil, errors.New("no range") },
			})
			r, err := in.Estimate(ctx, "ollama://qwen3:8b", "qwen3-8b.gguf", []uint32{8192})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.Approximate).To(BeTrue())
			Expect(r.Source).To(Equal("file-size"))
			Expect(r.SizeBytes).To(Equal(uint64(5_200_000_000)))
		})

		It("refuses a file that is not the model layer", func() {
			_, err := ollamaInspector(reg, modelsearch.InspectorOptions{}).Estimate(ctx, "ollama://acme/vision:4b", "vision-4b-mmproj.gguf", nil)
			Expect(errors.Is(err, modelsearch.ErrNoSuchFile)).To(BeTrue())
		})
	})
})
