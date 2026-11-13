package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/importers"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	"github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const remoteRev = "9217f5db79a29953eb74d5343926648285ec7e67"

const remoteRepoFixture = `{"id":"acme/Tiny-GGUF","sha":"` + remoteRev + `","gated":false,
 "pipeline_tag":"text-generation","tags":["gguf","license:apache-2.0"],"cardData":{"license":"apache-2.0"},
 "gguf":{"total":630167424,"architecture":"qwen2","context_length":32768},
 "siblings":[
  {"rfilename":"README.md","size":4856},
  {"rfilename":"tiny-q8_0.gguf","size":670000000,"lfs":{"sha256":"aa","size":670000000}},
  {"rfilename":"tiny-q4_k_m.gguf","size":398000000,"lfs":{"sha256":"bb","size":398000000}}]}`

var _ = Describe("remote model routes", func() {
	var (
		hf        *httptest.Server
		repoHits  atomic.Int32
		repoState atomic.Int32
		e         *echo.Echo
		cl        *config.ModelConfigLoader
		readURIs  []string
	)

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}

	BeforeEach(func() {
		repoHits.Store(0)
		readURIs = nil
		repoState.Store(0)
		hf = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/models/"):
				repoHits.Add(1)
				if st := int(repoState.Load()); st != 0 {
					w.WriteHeader(st)
					return
				}
				_, _ = w.Write([]byte(remoteRepoFixture))
			case strings.HasSuffix(r.URL.Path, "/README.md"):
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("# Tiny\n\nTiny is a very small chat model used by the tests of this route.\n"))
			default:
				http.NotFound(w, r)
			}
		}))
		yes := true
		in := modelsearch.NewInspector(modelsearch.InspectorOptions{
			HFBase: hf.URL, Client: hf.Client(), QuantMatches: importers.QuantMatches,
			PirateFace: func(_ stdContext, repo string) (*downloader.PirateFaceEntry, error) {
				if repo != "acme/Tiny-GGUF" {
					return nil, downloader.ErrPirateFaceNotListed
				}
				return &downloader.PirateFaceEntry{Revision: remoteRev, HF: &yes, Magnet: "magnet:?xt=urn:btih:aa",
					Files: []downloader.PirateFaceFile{{Path: "tiny-q4_k_m.gguf", SHA256: "bb"}}}, nil
			},
			ReadMeta: func(_ stdContext, uri string) (*vram.GGUFMeta, error) {
				readURIs = append(readURIs, uri)
				return &vram.GGUFMeta{BlockCount: 24, EmbeddingLength: 896, HeadCount: 14, HeadCountKV: 2, MaximumContextLength: 32768}, nil
			},
		})
		cl = config.NewModelConfigLoader(GinkgoT().TempDir())
		e = echo.New()
		e.GET("/api/models/remote/files", RemoteModelFilesEndpoint(in, cl))
		e.GET("/api/models/remote/estimate", RemoteModelEstimateEndpoint(in))
		s := modelsearch.NewSearcher(modelsearch.Options{CacheTTL: -1})
		s.Register(&stubSearchProvider{name: "huggingface", res: []modelsearch.Result{
			{ID: "acme/Tiny-GGUF", URI: "huggingface://acme/Tiny-GGUF", Tags: []string{"gguf"}, PipelineTag: "text-generation"},
			{ID: "acme/Other", URI: "huggingface://acme/Other", Tags: []string{"safetensors"}},
		}})
		s.Register(&stubSearchProvider{name: "pirateface", res: []modelsearch.Result{{ID: "acme/Peer", URI: "pirateface://acme/Peer"}}})
		e.GET("/api/models/search", ModelSearchEndpoint(s, cl))
	})
	AfterEach(func() { hf.Close() })

	Describe("files", func() {
		It("returns variants, licence, description and the backend of a dry run", func() {
			rec := get("/api/models/remote/files?uri=huggingface://acme/Tiny-GGUF")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["license"]).To(Equal("apache-2.0"))
			Expect(out["description"]).To(ContainSubstring("very small chat model"))
			Expect(out["backend"]).To(Equal("llama-cpp"))
			Expect(out["installed"]).To(BeFalse())
			Expect(out["size_known"]).To(BeTrue())
			variants := out["variants"].([]any)
			Expect(variants).To(HaveLen(2))
			first := variants[0].(map[string]any)
			Expect(first["quantization"]).To(Equal("Q4_K_M"))
			Expect(first["preference"]).To(Equal("q4_k_m"))
			Expect(first["recommended"]).To(BeTrue())
			Expect(out["import_files"].([]any)[0]).To(HaveSuffix("tiny-q4_k_m.gguf"))
		})

		It("dry-runs the import with the chosen quantization", func() {
			rec := get("/api/models/remote/files?uri=huggingface://acme/Tiny-GGUF&quantization=q8_0")
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["import_files"].([]any)[0]).To(HaveSuffix("tiny-q8_0.gguf"))
		})

		It("says which installed model comes from the repository", func() {
			cl.ReplaceModelConfigs([]config.ModelConfig{
				{Name: "other", DownloadFiles: []config.File{{URI: "huggingface://acme/Else/x.gguf"}}},
				{Name: "tiny", DownloadFiles: []config.File{{URI: "https://huggingface.co/acme/Tiny-GGUF/resolve/main/tiny-q4_k_m.gguf"}}},
			})
			rec := get("/api/models/remote/files?uri=huggingface://acme/Tiny-GGUF")
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["installed"]).To(BeTrue())
			Expect(out["installed_name"]).To(Equal("tiny"))
		})

		It("serves a Pirate Face repository", func() {
			rec := get("/api/models/remote/files?uri=pirateface://acme/Tiny-GGUF")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["provider"]).To(Equal("pirateface"))
			Expect(out["variants"].([]any)).To(HaveLen(1))
			Expect(out["variants"].([]any)[0].(map[string]any)["size_bytes"]).To(BeNumerically("==", 398_000_000))
		})

		It("serves the second request from the cache", func() {
			get("/api/models/remote/files?uri=huggingface://acme/Tiny-GGUF")
			get("/api/models/remote/files?uri=hf://acme/Tiny-GGUF")
			Expect(repoHits.Load()).To(Equal(int32(1)))
		})

		DescribeTable("maps failures to statuses",
			func(target string, upstream int, want int, wantRetry bool) {
				repoState.Store(int32(upstream))
				rec := get(target)
				Expect(rec.Code).To(Equal(want))
				Expect(rec.Header().Get("Retry-After") != "").To(Equal(wantRetry))
				if want != http.StatusOK {
					Expect(rec.Body.String()).To(ContainSubstring(`"message"`))
				}
			},
			Entry("no uri", "/api/models/remote/files", 0, http.StatusBadRequest, false),
			Entry("not a repository", "/api/models/remote/files?uri=magnet:?xt=urn:btih:aa", 0, http.StatusBadRequest, false),
			Entry("unknown to Hugging Face", "/api/models/remote/files?uri=acme/Gone", http.StatusUnauthorized, http.StatusNotFound, false),
			Entry("rate limited", "/api/models/remote/files?uri=acme/Slow", http.StatusTooManyRequests, http.StatusTooManyRequests, true),
			Entry("upstream broken", "/api/models/remote/files?uri=acme/Broken", http.StatusInternalServerError, http.StatusBadGateway, false),
			Entry("not listed on Pirate Face", "/api/models/remote/files?uri=pirateface://acme/Nope", 0, http.StatusNotFound, false),
		)
	})

	Describe("estimate", func() {
		It("returns the gallery estimate shape plus what was estimated and every variant", func() {
			rec := get("/api/models/remote/estimate?uri=huggingface://acme/Tiny-GGUF&file=tiny-q8_0.gguf&contexts=4096,8192")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out).To(HaveKey("sizeBytes"))
			Expect(out).To(HaveKey("sizeDisplay"))
			Expect(out["sizeBytes"]).To(BeNumerically("==", 670_000_000))
			ests := out["estimates"].(map[string]any)
			Expect(ests).To(HaveKey("4096"))
			Expect(ests).To(HaveKey("8192"))
			Expect(ests["8192"].(map[string]any)["vramBytes"]).To(BeNumerically(">", 670_000_000))
			Expect(out["blockCount"]).To(BeNumerically("==", 24))
			Expect(out["modelMaxContext"]).To(BeNumerically("==", 32768))
			Expect(out["file"]).To(Equal("tiny-q8_0.gguf"))
			Expect(out["quantization"]).To(Equal("Q8_0"))
			Expect(out["source"]).To(Equal("gguf-header"))
			Expect(out).ToNot(HaveKey("approximate"))
			Expect(out["variants"].([]any)).To(HaveLen(2))
			Expect(readURIs).To(HaveLen(1))
			Expect(readURIs[0]).To(HaveSuffix("/tiny-q8_0.gguf"))
		})

		It("defaults to the recommended variant and 8192 tokens", func() {
			rec := get("/api/models/remote/estimate?uri=huggingface://acme/Tiny-GGUF")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["estimates"].(map[string]any)).To(HaveKey(strconvU(config.DefaultEstimateContextSize)))
			Expect(out["file"]).To(Equal("tiny-q4_k_m.gguf"))
		})

		It("estimates a Pirate Face file through Hugging Face", func() {
			rec := get("/api/models/remote/estimate?uri=pirateface://acme/Tiny-GGUF&file=tiny-q4_k_m.gguf")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out["sizeBytes"]).To(BeNumerically("==", 398_000_000))
		})

		DescribeTable("rejects a bad request",
			func(target string, want int) {
				Expect(get(target).Code).To(Equal(want))
			},
			Entry("no uri", "/api/models/remote/estimate", http.StatusBadRequest),
			Entry("bad contexts", "/api/models/remote/estimate?uri=acme/Tiny-GGUF&contexts=abc", http.StatusBadRequest),
			Entry("tiny context", "/api/models/remote/estimate?uri=acme/Tiny-GGUF&contexts=1", http.StatusBadRequest),
			Entry("too many contexts", "/api/models/remote/estimate?uri=acme/Tiny-GGUF&contexts=1024,2048,4096,8192,16384,32768,65536,131072,262144", http.StatusBadRequest),
			Entry("a file that is not a weight file", "/api/models/remote/estimate?uri=acme/Tiny-GGUF&file=README.md", http.StatusBadRequest),
		)

		It("passes a rate limit through", func() {
			repoState.Store(http.StatusTooManyRequests)
			rec := get("/api/models/remote/estimate?uri=acme/Tiny-GGUF")
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		})
	})

	Describe("search enrichment", func() {
		It("adds installed, a derived backend and the variants hint", func() {
			cl.ReplaceModelConfigs([]config.ModelConfig{
				{Name: "tiny", DownloadFiles: []config.File{{URI: "huggingface://acme/Tiny-GGUF/tiny-q4_k_m.gguf"}}},
				{Name: "peer", Description: "Imported from pirateface://acme/Peer"},
			})
			rec := get("/api/models/search?q=x")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var out modelsearch.Response
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out.Results).To(HaveLen(3))
			tiny, other, peer := out.Results[0], out.Results[1], out.Results[2]
			Expect(tiny.Installed).To(BeTrue())
			Expect(tiny.InstalledName).To(Equal("tiny"))
			Expect(tiny.Backend).To(Equal("llama-cpp"))
			Expect(tiny.HasVariants).To(BeTrue())
			Expect(other.Installed).To(BeFalse())
			Expect(other.HasVariants).To(BeFalse())
			Expect(peer.Installed).To(BeTrue())
			Expect(peer.HasVariants).To(BeTrue())
		})
	})
})

type stdContext = context.Context

func strconvU(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
