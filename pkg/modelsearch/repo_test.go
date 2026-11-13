package modelsearch_test

import (
	stdctx "context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	"github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	rev = "9217f5db79a29953eb74d5343926648285ec7e67"

	// Shape of GET /api/models/<repo>?blobs=true, trimmed to what matters.
	hfRepoFixture = `{"id":"acme/Tiny-GGUF","sha":"` + rev + `","gated":false,"lastModified":"2026-01-02T03:04:05.000Z",
 "pipeline_tag":"text-generation","tags":["gguf","license:apache-2.0"],"cardData":{"license":"apache-2.0"},
 "gguf":{"total":630167424,"architecture":"qwen2","context_length":32768},
 "siblings":[
  {"rfilename":".gitattributes","size":1561},
  {"rfilename":"README.md","size":4856},
  {"rfilename":"tiny-q8_0.gguf","size":670000000,"lfs":{"sha256":"aa","size":670000000}},
  {"rfilename":"tiny-q4_k_m.gguf","size":398000000,"lfs":{"sha256":"bb","size":398000000}},
  {"rfilename":"tiny-q4_k_s.gguf","size":380000000},
  {"rfilename":"big/Big-BF16-00001-of-00002.gguf","size":4000000000,"lfs":{"sha256":"c1","size":4000000000}},
  {"rfilename":"big/Big-BF16-00002-of-00002.gguf","size":1000000000,"lfs":{"sha256":"c2","size":1000000000}},
  {"rfilename":"mmproj-tiny-f16.gguf","size":5000}
 ]}`

	hfReadme = "---\nlicense: apache-2.0\n---\n\n# Tiny\n\nTiny is a very small chat model for tests, built to run anywhere.\n"
)

type fakeHF struct {
	*httptest.Server
	repoHits   atomic.Int32
	revHits    atomic.Int32
	readmeHits atomic.Int32
	status     atomic.Int32 // when non-zero, answered for the repo route
	repoBody   atomic.Value
	revBody    atomic.Value
	delay      time.Duration
}

func newFakeHF() *fakeHF {
	f := &fakeHF{}
	f.repoBody.Store(hfRepoFixture)
	f.revBody.Store(hfRepoFixture)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		switch {
		case strings.Contains(r.URL.Path, "/revision/"):
			f.revHits.Add(1)
			if s := int(f.status.Load()); s != 0 {
				w.WriteHeader(s)
				return
			}
			_, _ = w.Write([]byte(f.revBody.Load().(string)))
		case strings.HasPrefix(r.URL.Path, "/api/models/"):
			f.repoHits.Add(1)
			if s := int(f.status.Load()); s != 0 {
				w.WriteHeader(s)
				return
			}
			_, _ = w.Write([]byte(f.repoBody.Load().(string)))
		case strings.HasSuffix(r.URL.Path, "/README.md"):
			f.readmeHits.Add(1)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(hfReadme))
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func inspector(f *fakeHF, o modelsearch.InspectorOptions) *modelsearch.Inspector {
	o.HFBase = f.URL
	o.Client = f.Client()
	return modelsearch.NewInspector(o)
}

var _ = Describe("Inspector.Files", func() {
	var hf *fakeHF
	BeforeEach(func() { hf = newFakeHF() })
	AfterEach(func() { hf.Close() })

	It("lists weight files with sizes, groups shards and reads licence, gated and the model card", func() {
		in := inspector(hf, modelsearch.InspectorOptions{})
		rf, err := in.Files(stdctx.Background(), "huggingface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())

		Expect(rf.Provider).To(Equal("huggingface"))
		Expect(rf.URI).To(Equal("huggingface://acme/Tiny-GGUF"))
		Expect(rf.Revision).To(Equal(rev))
		Expect(rf.License).To(Equal("apache-2.0"))
		Expect(rf.Gated).To(BeFalse())
		Expect(rf.PipelineTag).To(Equal("text-generation"))
		Expect(rf.SizeKnown).To(BeTrue())
		Expect(rf.Description).To(Equal("Tiny is a very small chat model for tests, built to run anywhere."))
		Expect(rf.GGUF).To(Equal(&modelsearch.GGUFInfo{Architecture: "qwen2", ContextLength: 32768, Parameters: 630167424}))

		// README.md and .gitattributes are not weights; the mmproj file is
		// listed but is not a variant.
		var paths []string
		for _, f := range rf.Files {
			paths = append(paths, f.Path)
		}
		Expect(paths).To(ConsistOf("tiny-q8_0.gguf", "tiny-q4_k_m.gguf", "tiny-q4_k_s.gguf",
			"big/Big-BF16-00001-of-00002.gguf", "big/Big-BF16-00002-of-00002.gguf", "mmproj-tiny-f16.gguf"))

		Expect(rf.Variants).To(HaveLen(4))
		q := map[string]modelsearch.Variant{}
		var order []string
		for _, v := range rf.Variants {
			q[v.Quantization] = v
			order = append(order, v.Quantization)
		}
		Expect(order).To(Equal([]string{"Q4_K_S", "Q4_K_M", "Q8_0", "BF16"}), "smallest first")
		bf := q["BF16"]
		Expect(bf.Shards).To(Equal(2))
		Expect(bf.SizeBytes).To(Equal(int64(5_000_000_000)))
		Expect(bf.File).To(Equal("big/Big-BF16-00001-of-00002.gguf"))
		Expect(q["Q4_K_M"].Recommended).To(BeTrue())
		Expect(q["Q8_0"].Recommended).To(BeFalse())
		Expect(q["Q4_K_M"].SizeBytes).To(Equal(int64(398_000_000)))
	})

	It("computes a preference that selects only the variant", func() {
		hf.repoBody.Store(strings.Replace(hfRepoFixture,
			`{"rfilename":"tiny-q8_0.gguf"`, `{"rfilename":"tiny-ud-q4_k_m.gguf","size":410000000},{"rfilename":"tiny-q8_0.gguf"`, 1))
		in := inspector(hf, modelsearch.InspectorOptions{QuantMatches: func(base, pref string) bool {
			return strings.Contains(strings.ToLower(base), strings.ToLower(pref))
		}})
		rf, err := in.Files(stdctx.Background(), "huggingface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		pref := map[string]string{}
		for _, v := range rf.Variants {
			pref[v.File] = v.Preference
		}
		Expect(pref["tiny-q8_0.gguf"]).To(Equal("q8_0"))
		// "q4_k_m" matches two files here, so the whole file name is used.
		Expect(pref["tiny-q4_k_m.gguf"]).To(Equal("tiny-q4_k_m"))
	})

	It("reports gated repositories without failing", func() {
		hf.repoBody.Store(strings.Replace(hfRepoFixture, `"gated":false`, `"gated":"manual"`, 1))
		rf, err := inspector(hf, modelsearch.InspectorOptions{}).Files(stdctx.Background(), "acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.Gated).To(BeTrue())
	})

	It("keeps a non-GGUF repository as plain weight files with no variants", func() {
		hf.repoBody.Store(`{"id":"acme/St","sha":"` + rev + `","tags":[],"siblings":[
		 {"rfilename":"model-00001-of-00002.safetensors","size":3000},{"rfilename":"model-00002-of-00002.safetensors","size":2000},{"rfilename":"config.json","size":10}]}`)
		rf, err := inspector(hf, modelsearch.InspectorOptions{}).Files(stdctx.Background(), "acme/St")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.Variants).To(BeEmpty())
		Expect(rf.Files).To(HaveLen(2))
		Expect(rf.WeightBytes()).To(Equal(int64(5000)))
	})

	It("caches per repository and shares concurrent calls", func() {
		hf.delay = 50 * time.Millisecond
		in := inspector(hf, modelsearch.InspectorOptions{})
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := in.Files(stdctx.Background(), "huggingface://acme/Tiny-GGUF")
				Expect(err).ToNot(HaveOccurred())
			}()
		}
		wg.Wait()
		_, err := in.Files(stdctx.Background(), "hf://acme/Tiny-GGUF/tiny-q8_0.gguf")
		Expect(err).ToNot(HaveOccurred())
		Expect(hf.repoHits.Load()).To(Equal(int32(1)))
		Expect(hf.readmeHits.Load()).To(Equal(int32(1)))
	})

	It("fetches again after the TTL", func() {
		in := inspector(hf, modelsearch.InspectorOptions{TTL: 20 * time.Millisecond})
		_, err := in.Files(stdctx.Background(), "acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		time.Sleep(40 * time.Millisecond)
		_, err = in.Files(stdctx.Background(), "acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(hf.repoHits.Load()).To(Equal(int32(2)))
	})

	It("maps 429 to ErrRateLimited and does not ask again during the error TTL", func() {
		hf.status.Store(http.StatusTooManyRequests)
		in := inspector(hf, modelsearch.InspectorOptions{ErrorTTL: time.Minute})
		for range 3 {
			_, err := in.Files(stdctx.Background(), "acme/Tiny-GGUF")
			Expect(errors.Is(err, modelsearch.ErrRateLimited)).To(BeTrue())
		}
		Expect(hf.repoHits.Load()).To(Equal(int32(1)))
	})

	It("maps 401 and 404 to ErrRepoNotFound", func() {
		for _, st := range []int{http.StatusUnauthorized, http.StatusNotFound} {
			hf.status.Store(int32(st))
			_, err := inspector(hf, modelsearch.InspectorOptions{}).Files(stdctx.Background(), "acme/Gone")
			Expect(errors.Is(err, modelsearch.ErrRepoNotFound)).To(BeTrue())
		}
	})

	It("does not cache other failures", func() {
		hf.status.Store(http.StatusInternalServerError)
		in := inspector(hf, modelsearch.InspectorOptions{})
		_, err := in.Files(stdctx.Background(), "acme/Tiny-GGUF")
		Expect(err).To(HaveOccurred())
		hf.status.Store(0)
		_, err = in.Files(stdctx.Background(), "acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
	})

	It("rejects a uri that names no repository", func() {
		_, err := inspector(hf, modelsearch.InspectorOptions{}).Files(stdctx.Background(), "magnet:?xt=urn:btih:aa")
		Expect(errors.Is(err, modelsearch.ErrBadRepo)).To(BeTrue())
	})

	It("limits upstream calls running at once", func() {
		var cur, peak atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/api/models/") {
				n := cur.Add(1)
				for {
					p := peak.Load()
					if n <= p || peak.CompareAndSwap(p, n) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				cur.Add(-1)
				_, _ = w.Write([]byte(hfRepoFixture))
				return
			}
			http.NotFound(w, r)
		}))
		defer srv.Close()
		in := modelsearch.NewInspector(modelsearch.InspectorOptions{HFBase: srv.URL, Client: srv.Client(), MaxConcurrent: 2})
		var wg sync.WaitGroup
		for _, id := range []string{"a/1", "a/2", "a/3", "a/4", "a/5", "a/6"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = in.Files(stdctx.Background(), id)
			}()
		}
		wg.Wait()
		Expect(peak.Load()).To(BeNumerically("<=", 2))
	})
})

var _ = Describe("Inspector.Files for Pirate Face", func() {
	var hf *fakeHF
	BeforeEach(func() { hf = newFakeHF() })
	AfterEach(func() { hf.Close() })

	pf := func(entry *downloader.PirateFaceEntry, err error, hits *atomic.Int32) func(stdctx.Context, string) (*downloader.PirateFaceEntry, error) {
		return func(_ stdctx.Context, repo string) (*downloader.PirateFaceEntry, error) {
			if hits != nil {
				hits.Add(1)
			}
			Expect(repo).To(Equal("acme/Tiny-GGUF"))
			return entry, err
		}
	}
	yes := true
	entry := &downloader.PirateFaceEntry{
		RepoID: "acme/Tiny-GGUF", Revision: rev, HF: &yes,
		Magnet: "magnet:?xt=urn:btih:aa",
		Files:  []downloader.PirateFaceFile{{Path: "tiny-q4_k_m.gguf", SHA256: "bb"}},
		Quants: []downloader.PirateFaceQuant{
			{Quant: "x", Revision: rev, Files: []downloader.PirateFaceFile{{Path: "tiny-q4_k_m.gguf", SHA256: "bb"}}},
			{Quant: "y", Revision: rev, Files: []downloader.PirateFaceFile{{Path: "tiny-q8_0.gguf", SHA256: "aa"}}},
		},
	}

	It("takes the file list from the index and the sizes from the same Hugging Face commit", func() {
		var hits atomic.Int32
		in := inspector(hf, modelsearch.InspectorOptions{PirateFace: pf(entry, nil, &hits)})
		rf, err := in.Files(stdctx.Background(), "pirateface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.Provider).To(Equal("pirateface"))
		Expect(rf.URI).To(Equal("pirateface://acme/Tiny-GGUF"))
		Expect(rf.SizeKnown).To(BeTrue())
		Expect(rf.License).To(Equal("apache-2.0"))
		Expect(rf.Gated).To(BeFalse())
		Expect(rf.Variants).To(HaveLen(2))
		Expect(rf.Variants[0].Quantization).To(Equal("Q4_K_M"))
		Expect(rf.Variants[0].SizeBytes).To(Equal(int64(398_000_000)))
		Expect(rf.Files[0].SHA256).ToNot(BeEmpty())
		// Hugging Face was asked for the commit the torrent mirrors.
		Expect(hf.revHits.Load()).To(Equal(int32(1)))
		Expect(hf.repoHits.Load()).To(BeZero())

		_, err = in.Files(stdctx.Background(), "pirateface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(hits.Load()).To(Equal(int32(1)), "one index lookup per TTL")
	})

	It("says sizes are unknown when Hugging Face does not know the repository", func() {
		hf.status.Store(http.StatusUnauthorized)
		in := inspector(hf, modelsearch.InspectorOptions{PirateFace: pf(entry, nil, nil)})
		rf, err := in.Files(stdctx.Background(), "pirateface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.SizeKnown).To(BeFalse())
		Expect(rf.Variants).To(HaveLen(2))
		Expect(rf.Variants[0].SizeBytes).To(BeZero())
	})

	It("marks a repository with no magnet link as gated", func() {
		noMagnet := *entry
		noMagnet.Magnet = ""
		rf, err := inspector(hf, modelsearch.InspectorOptions{PirateFace: pf(&noMagnet, nil, nil)}).Files(stdctx.Background(), "pirateface://acme/Tiny-GGUF")
		Expect(err).ToNot(HaveOccurred())
		Expect(rf.Gated).To(BeTrue())
	})

	It("maps an unlisted repository to ErrRepoNotFound", func() {
		in := inspector(hf, modelsearch.InspectorOptions{PirateFace: pf(nil, downloader.ErrPirateFaceNotListed, nil)})
		_, err := in.Files(stdctx.Background(), "pirateface://acme/Tiny-GGUF")
		Expect(errors.Is(err, modelsearch.ErrRepoNotFound)).To(BeTrue())
	})
})

var _ = Describe("Inspector.Estimate", func() {
	var hf *fakeHF
	var readURIs []string
	BeforeEach(func() { hf = newFakeHF(); readURIs = nil })
	AfterEach(func() { hf.Close() })

	meta := &vram.GGUFMeta{BlockCount: 24, EmbeddingLength: 896, HeadCount: 14, HeadCountKV: 2, MaximumContextLength: 32768}
	reader := func(m *vram.GGUFMeta, err error) func(stdctx.Context, string) (*vram.GGUFMeta, error) {
		return func(_ stdctx.Context, uri string) (*vram.GGUFMeta, error) {
			readURIs = append(readURIs, uri)
			return m, err
		}
	}

	It("estimates the recommended variant by default, from the header, with a pinned file url", func() {
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)})
		r, err := in.Estimate(stdctx.Background(), "huggingface://acme/Tiny-GGUF", "", []uint32{4096, 8192})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.File).To(Equal("tiny-q4_k_m.gguf"))
		Expect(r.Quantization).To(Equal("Q4_K_M"))
		Expect(r.Source).To(Equal("gguf-header"))
		Expect(r.Approximate).To(BeFalse())
		Expect(r.BlockCount).To(Equal(uint32(24)))
		Expect(r.SizeBytes).To(Equal(uint64(398_000_000)))
		Expect(r.Estimates).To(HaveKey("4096"))
		Expect(r.Estimates).To(HaveKey("8192"))
		Expect(readURIs).To(Equal([]string{hf.URL + "/acme/Tiny-GGUF/resolve/" + rev + "/tiny-q4_k_m.gguf"}))
	})

	It("answers every variant from the one header read", func() {
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)})
		r, err := in.Estimate(stdctx.Background(), "acme/Tiny-GGUF", "", []uint32{8192})
		Expect(err).ToNot(HaveOccurred())
		Expect(readURIs).To(HaveLen(1))
		Expect(r.Variants).To(HaveLen(4))
		byFile := map[string]modelsearch.VariantEstimate{}
		for _, v := range r.Variants {
			byFile[v.File] = v
		}
		small, big := byFile["tiny-q4_k_s.gguf"], byFile["big/Big-BF16-00001-of-00002.gguf"]
		Expect(small.SizeBytes).To(Equal(uint64(380_000_000)))
		Expect(big.SizeBytes).To(Equal(uint64(5_000_000_000)))
		Expect(big.Estimates["8192"].VRAMBytes).To(BeNumerically(">", small.Estimates["8192"].VRAMBytes))
		// the chosen variant's own figure is the same as its entry in the list
		Expect(byFile["tiny-q4_k_m.gguf"].Estimates["8192"].VRAMBytes).To(Equal(r.Estimates["8192"].VRAMBytes))
	})

	It("estimates a sharded variant on the sum of its shards, reading the first shard", func() {
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)})
		r, err := in.Estimate(stdctx.Background(), "acme/Tiny-GGUF", "big/Big-BF16-00001-of-00002.gguf", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.SizeBytes).To(Equal(uint64(5_000_000_000)))
		Expect(readURIs[0]).To(HaveSuffix("/big/Big-BF16-00001-of-00002.gguf"))
	})

	It("marks a size-only estimate as approximate when the header cannot be read", func() {
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(nil, errors.New("range not supported"))})
		r, err := in.Estimate(stdctx.Background(), "acme/Tiny-GGUF", "tiny-q8_0.gguf", []uint32{8192})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Approximate).To(BeTrue())
		Expect(r.Source).To(Equal("file-size"))
		Expect(r.Estimates["8192"].VRAMBytes).To(Equal(uint64(670_000_000 + 1024*8192*2)))
		Expect(r.Variants).ToNot(BeEmpty())
	})

	It("refuses a file that is not a weight file of the repository", func() {
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)})
		_, err := in.Estimate(stdctx.Background(), "acme/Tiny-GGUF", "README.md", nil)
		Expect(errors.Is(err, modelsearch.ErrNoSuchFile)).To(BeTrue())
	})

	It("estimates a non-GGUF repository on its total weight size, approximately", func() {
		hf.repoBody.Store(`{"id":"acme/St","sha":"` + rev + `","tags":[],"siblings":[
		 {"rfilename":"a.safetensors","size":3000000000},{"rfilename":"b.safetensors","size":2000000000}]}`)
		in := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)})
		r, err := in.Estimate(stdctx.Background(), "acme/St", "", []uint32{8192})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.SizeBytes).To(Equal(uint64(5_000_000_000)))
		Expect(r.Approximate).To(BeTrue())
		Expect(readURIs).To(BeEmpty())
		Expect(r.Variants).To(BeEmpty())
	})

	It("answers unknown, reading nothing, when no size is known", func() {
		hf.status.Store(http.StatusUnauthorized)
		yes := true
		in := inspector(hf, modelsearch.InspectorOptions{
			ReadMeta: reader(meta, nil),
			PirateFace: func(stdctx.Context, string) (*downloader.PirateFaceEntry, error) {
				return &downloader.PirateFaceEntry{Revision: rev, HF: &yes, Magnet: "m", Files: []downloader.PirateFaceFile{{Path: "tiny-q4_k_m.gguf"}}}, nil
			},
		})
		r, err := in.Estimate(stdctx.Background(), "pirateface://acme/Tiny-GGUF", "", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Source).To(Equal("unknown"))
		Expect(r.SizeBytes).To(BeZero())
		Expect(readURIs).To(BeEmpty())
	})

	It("passes errors of the repository lookup through", func() {
		hf.status.Store(http.StatusTooManyRequests)
		_, err := inspector(hf, modelsearch.InspectorOptions{ReadMeta: reader(meta, nil)}).Estimate(stdctx.Background(), "acme/Tiny-GGUF", "", nil)
		Expect(errors.Is(err, modelsearch.ErrRateLimited)).To(BeTrue())
	})
})
