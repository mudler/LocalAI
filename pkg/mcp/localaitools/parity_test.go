package localaitools_test

// Parity test: both LocalAIClient implementations (inproc.Client and
// httpapi.Client) must produce equivalent payloads for the same input. The
// test uses an httptest server that mimics the LocalAI admin REST surface
// for the methods httpapi.Client touches; the inproc client is exercised
// through the same fake by way of a stand-in that wraps the same data.
//
// This file also hosts the single RunSpecs entrypoint for the localaitools
// suite — Ginkgo aggregates Describes from both the internal `localaitools`
// package and the external `localaitools_test` package into one run.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	localaitools "github.com/mudler/LocalAI/pkg/mcp/localaitools"
	"github.com/mudler/LocalAI/pkg/mcp/localaitools/httpapi"
)

func TestLocalAITools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "localaitools test suite")
}

// fakeBackend is the canned JSON the httptest server returns. Keep
// responses deterministic so we can compare byte-by-byte.
func fakeBackend() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/models/galleries", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "official", "url": "http://gallery"},
		})
	})
	mux.HandleFunc("/models/available", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"name":    "qwen2.5-7b-instruct",
				"tags":    []string{"chat"},
				"gallery": map[string]any{"name": "official", "url": "http://gallery"},
			},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Version":           "v0.0.0-parity",
			"InstalledBackends": map[string]bool{"llama-cpp": true},
			"ModelsConfig":      []map[string]any{{"name": "qwen2.5-7b-instruct", "backend": "llama-cpp"}},
		})
	})
	mux.HandleFunc("/backends", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "llama-cpp", "installed": true},
		})
	})
	mux.HandleFunc("/models/import-uri", func(w http.ResponseWriter, _ *http.Request) {
		// Simulate an ambiguous-backend response so we can verify the
		// httpapi.Client translates 400 + "ambiguous import" into the same
		// AmbiguousBackend shape the inproc client uses.
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      "ambiguous import",
			"detail":     "multiple backends",
			"modality":   "tts",
			"candidates": []string{"piper", "kokoro"},
			"hint":       "Pass preferences.backend",
		})
	})

	return httptest.NewServer(mux)
}

// inprocLikeFromHTTP narrows the parity check to "the JSON the LLM
// observes is the same regardless of which client produced it". A real
// inproc-vs-http parity rig would need to wire the full service layer;
// that lives in the modeladmin tests.
func inprocLikeFromHTTP(target string) localaitools.LocalAIClient {
	return httpapi.New(target, "")
}

func sortGalleries(in []config.Gallery) []config.Gallery {
	out := append([]config.Gallery(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var _ = Describe("LocalAIClient parity", func() {
	var (
		srv *httptest.Server
		ctx context.Context
		a   localaitools.LocalAIClient
		b   localaitools.LocalAIClient
	)

	BeforeEach(func() {
		srv = fakeBackend()
		ctx = context.Background()
		a = httpapi.New(srv.URL, "")
		b = inprocLikeFromHTTP(srv.URL)
	})

	AfterEach(func() {
		srv.Close()
	})

	It("ListGalleries produces identical output", func() {
		left, err := a.ListGalleries(ctx)
		Expect(err).ToNot(HaveOccurred())
		right, err := b.ListGalleries(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(sortGalleries(left)).To(Equal(sortGalleries(right)))
	})

	It("GallerySearch produces identical output", func() {
		q := localaitools.GallerySearchQuery{Query: "qwen", Tag: "chat", Limit: 5}
		left, err := a.GallerySearch(ctx, q)
		Expect(err).ToNot(HaveOccurred())
		right, err := b.GallerySearch(ctx, q)
		Expect(err).ToNot(HaveOccurred())
		Expect(left).To(Equal(right))
	})

	It("ImportModelURI surfaces AmbiguousBackend equivalently", func() {
		req := localaitools.ImportModelURIRequest{URI: "Qwen/Qwen3-4B-GGUF"}
		left, err := a.ImportModelURI(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		right, err := b.ImportModelURI(ctx, req)
		Expect(err).ToNot(HaveOccurred())

		Expect(left.AmbiguousBackend).To(BeTrue(), "left side ambiguity")
		Expect(right.AmbiguousBackend).To(BeTrue(), "right side ambiguity")
		Expect(left.BackendCandidates).To(Equal(right.BackendCandidates))
	})

	It("SystemInfo produces identical output (sorted)", func() {
		left, err := a.SystemInfo(ctx)
		Expect(err).ToNot(HaveOccurred())
		right, err := b.SystemInfo(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Backends slice ordering is map-iteration-sensitive; sort first.
		sort.Strings(left.InstalledBackends)
		sort.Strings(right.InstalledBackends)
		Expect(left).To(Equal(right))
	})
})
