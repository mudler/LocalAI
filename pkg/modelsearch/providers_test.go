package modelsearch_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const hfFixture = `[
 {"_id":"1","id":"acme/Tiny-GGUF","author":"acme","gated":false,"lastModified":"2026-01-02T03:04:05.000Z",
  "likes":12,"downloads":3400,"tags":["gguf","license:apache-2.0","region:us"],"pipeline_tag":"text-generation"},
 {"_id":"2","id":"acme/Locked-GGUF","author":"acme","gated":"manual","likes":1,"downloads":5,"tags":["gguf"]},
 {"_id":"3","id":"acme/Hidden","private":true,"downloads":1},
 {"_id":"4","downloads":1}
]`

var _ = Describe("Hugging Face provider", func() {
	It("queries the models API and maps the answer", func() {
		var got *http.Request
		var auth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, auth = r, r.Header.Get("Authorization")
			_, _ = w.Write([]byte(hfFixture))
		}))
		defer srv.Close()

		p := modelsearch.NewHuggingFace(modelsearch.HuggingFaceOptions{BaseURL: srv.URL + "/api/models", Token: "tok"})
		res, err := p.Search(context(), modelsearch.Query{Text: "tiny model", Limit: 5}.Normalize())
		Expect(err).ToNot(HaveOccurred())

		qs := got.URL.Query()
		Expect(qs.Get("search")).To(Equal("tiny model"))
		Expect(qs.Get("filter")).To(Equal("gguf"))
		Expect(qs.Get("sort")).To(Equal("downloads"))
		Expect(qs.Get("direction")).To(Equal("-1"))
		Expect(qs.Get("limit")).To(Equal("5"))
		Expect(got.Header.Get("User-Agent")).To(ContainSubstring("LocalAI"))
		Expect(auth).To(Equal("Bearer tok"))

		Expect(res).To(HaveLen(2))
		Expect(res[0].ID).To(Equal("acme/Tiny-GGUF"))
		Expect(res[0].URI).To(Equal("huggingface://acme/Tiny-GGUF"))
		Expect(res[0].PipelineTag).To(Equal("text-generation"))
		Expect(res[0].License).To(Equal("apache-2.0"))
		Expect(res[0].Downloads).To(Equal(int64(3400)))
		Expect(res[0].Likes).To(Equal(int64(12)))
		Expect(res[0].Gated).To(BeFalse())
		Expect(res[1].Gated).To(BeTrue())
	})

	It("drops the format filter for any and sends no token by default", func() {
		var got *http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			_, _ = w.Write([]byte("[]"))
		}))
		defer srv.Close()
		p := modelsearch.NewHuggingFace(modelsearch.HuggingFaceOptions{BaseURL: srv.URL})
		_, err := p.Search(context(), modelsearch.Query{Text: "x", Format: "any", Sort: "likes"}.Normalize())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.URL.Query().Has("filter")).To(BeFalse())
		Expect(got.URL.Query().Get("sort")).To(Equal("likes"))
		Expect(got.Header.Get("Authorization")).To(BeEmpty())
	})

	It("lists the newest repositories by creation date when there is no text", func() {
		var got *http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			_, _ = w.Write([]byte(hfFixture))
		}))
		defer srv.Close()
		p := modelsearch.NewHuggingFace(modelsearch.HuggingFaceOptions{BaseURL: srv.URL})
		l, ok := p.(modelsearch.LatestLister)
		Expect(ok).To(BeTrue())
		Expect(l.ListsLatest()).To(BeTrue())
		res, err := p.Search(context(), modelsearch.Query{Sort: modelsearch.SortLatest, Limit: 10}.Normalize())
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(HaveLen(2))
		qs := got.URL.Query()
		Expect(qs.Has("search")).To(BeFalse())
		Expect(qs.Get("sort")).To(Equal("createdAt"))
		Expect(qs.Get("direction")).To(Equal("-1"))
		Expect(qs.Get("filter")).To(Equal("gguf"))
		Expect(qs.Get("limit")).To(Equal("10"))
	})

	It("reports rate limiting and bad bodies", func() {
		status, body := http.StatusTooManyRequests, ""
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()
		p := modelsearch.NewHuggingFace(modelsearch.HuggingFaceOptions{BaseURL: srv.URL})
		_, err := p.Search(context(), modelsearch.Query{Text: "x"}.Normalize())
		Expect(err).To(MatchError(ContainSubstring("rate limited")))
		status, body = http.StatusOK, "not json"
		_, err = p.Search(context(), modelsearch.Query{Text: "x"}.Normalize())
		Expect(err).To(MatchError(ContainSubstring("parse")))
	})
})

const pfFixture = `Qwen/Qwen3-0.6B                                        1.5 GB    6 seeds  magnet:?xt=urn:btih:7158ba0f294cdd7da7f4d4f7a496c6162a63a622&tr=udp%3A%2F%2Ftracker.example%3A6969%2Fannounce&ws=https%3A%2F%2Fexample.org%2Fws%2F
this line is garbage
Qwen/Qwen3-14B   30 GB   2 seeds   magnet:?xt=urn:btih:ffbfa2798581f0bfeed14d913ddf75815a7fce31
Qwen/NoMagnet   30 GB   2 seeds   http://example.org/x
NoSlash 1 GB 1 seeds magnet:?xt=urn:btih:aaaa
Qwen/Bad   x GB   y seeds   magnet:?xt=urn:btih:aaaa

org/one-seed   512 MB   1 seed   magnet:?xt=urn:btih:bbbb
org/third   2.5 GiB   0 seeds   magnet:?xt=urn:btih:cccc
`

var _ = Describe("Pirate Face provider", func() {
	It("parses the text listing and skips malformed lines", func() {
		var got *http.Request
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(pfFixture))
		}))
		defer srv.Close()

		p := modelsearch.NewPirateFace(modelsearch.PirateFaceOptions{BaseURL: srv.URL + "/"})
		res, err := p.Search(context(), modelsearch.Query{Text: "qwen 3/x?"}.Normalize())
		Expect(err).ToNot(HaveOccurred())

		Expect(got.URL.EscapedPath()).To(Equal("/s/qwen%203%2Fx%3F"))
		Expect(strings.HasPrefix(got.URL.Path, "/api")).To(BeFalse())
		Expect(got.Header.Get("User-Agent")).To(ContainSubstring("LocalAI"))

		Expect(res).To(HaveLen(4))
		Expect(res[0].ID).To(Equal("Qwen/Qwen3-0.6B"))
		Expect(res[0].SizeBytes).To(Equal(int64(1_500_000_000)))
		Expect(res[0].Seeders).To(Equal(int64(6)))
		Expect(res[0].URI).To(Equal("pirateface://Qwen/Qwen3-0.6B"))
		Expect(res[0].Magnet).To(HavePrefix("magnet:?xt=urn:btih:7158ba0f"))
		Expect(res[0].Magnet).To(ContainSubstring("&ws="))
		Expect(res[1].ID).To(Equal("Qwen/Qwen3-14B"))
		Expect(res[2].SizeBytes).To(Equal(int64(512_000_000)))
		Expect(res[3].SizeBytes).To(Equal(int64(2.5 * (1 << 30))))
	})

	It("fails on a non-200 answer", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		p := modelsearch.NewPirateFace(modelsearch.PirateFaceOptions{BaseURL: srv.URL})
		_, err := p.Search(context(), modelsearch.Query{Text: "x"}.Normalize())
		Expect(err).To(MatchError(ContainSubstring("502")))
	})

	It("refuses an oversized body", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("a", 5<<20)))
		}))
		defer srv.Close()
		p := modelsearch.NewPirateFace(modelsearch.PirateFaceOptions{BaseURL: srv.URL})
		_, err := p.Search(context(), modelsearch.Query{Text: "x"}.Normalize())
		Expect(err).To(MatchError(ContainSubstring("larger")))
	})
})
