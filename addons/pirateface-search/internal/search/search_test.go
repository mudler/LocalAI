// SPDX-License-Identifier: MIT
package search_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/pirateface-search/internal/search"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

// The listing is a recorded page of the text search of the site. The tests
// never call the live site.
func fixture() string {
	b, err := os.ReadFile("testdata/listing.txt")
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

func env(args map[string]any) contract.Envelope {
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "pirateface-search", OperationID: "op-1", Generation: 1, Action: "read", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "ann", CredentialID: "c1", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Hour)},
	}
}

var _ = Describe("the text listing", func() {
	It("parses the lines it understands and skips the others", func() {
		rows := search.ParseListing(fixture(), 0, 0)
		Expect(rows).To(HaveLen(4))
		Expect(rows[0].ID).To(Equal("Qwen/Qwen3-0.6B"))
		Expect(rows[0].SizeBytes).To(Equal(int64(1_500_000_000)))
		Expect(rows[0].Seeders).To(Equal(int64(6)))
		Expect(rows[0].URI).To(Equal("pirateface://Qwen/Qwen3-0.6B"))
		Expect(rows[0].Magnet).To(HavePrefix("magnet:?xt=urn:btih:7158ba0f"))
		Expect(rows[0].Magnet).To(ContainSubstring("&ws="))
		Expect(rows[1].ID).To(Equal("Qwen/Qwen3-14B"))
		Expect(rows[2].SizeBytes).To(Equal(int64(512_000_000)))
		Expect(rows[3].SizeBytes).To(Equal(int64(2.5 * (1 << 30))))
	})

	It("keeps the limit and drops the rows with too few seeders first", func() {
		Expect(search.ParseListing(fixture(), 2, 0)).To(HaveLen(2))
		rows := search.ParseListing(fixture(), 0, 2)
		Expect(rows).To(HaveLen(2))
		Expect(rows[1].ID).To(Equal("Qwen/Qwen3-14B"))
		Expect(search.ParseListing(fixture(), 1, 2)[0].ID).To(Equal("Qwen/Qwen3-0.6B"))
	})
})

var _ = Describe("the search tool", func() {
	var (
		srv    *httptest.Server
		got    *http.Request
		status = http.StatusOK
		body   string
		rt     contract.Runtime
	)
	read := func(args map[string]any) (map[string]any, error) {
		res, err := rt.Read(context.Background(), env(args))
		if err != nil {
			return nil, err
		}
		var out map[string]any
		Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
		return out, nil
	}
	BeforeEach(func() {
		status, body, got = http.StatusOK, fixture(), nil
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		DeferCleanup(srv.Close)
		rt = search.New(search.Options{BaseURL: srv.URL + "/"})
	})

	It("reads the text page of the site and never an /api path", func() {
		out, err := read(map[string]any{"tool": "search", "q": "qwen 3/x?", "limit": 20, "sort": "downloads", "format": "gguf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.URL.EscapedPath()).To(Equal("/s/qwen%203%2Fx%3F"))
		Expect(strings.HasPrefix(got.URL.Path, "/api")).To(BeFalse())
		Expect(got.Header.Get("User-Agent")).To(ContainSubstring("LocalAI"))
		Expect(out["results"]).To(HaveLen(4))
	})

	It("applies the minimum seeders filter", func() {
		out, err := read(map[string]any{"tool": "search", "q": "qwen", "limit": 20, "filters": map[string]any{"min-seeders": 2}})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["results"]).To(HaveLen(2))
	})

	It("refuses a filter it does not declare, and a value out of range", func() {
		_, err := read(map[string]any{"tool": "search", "q": "x", "filters": map[string]any{"nope": 1}})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = read(map[string]any{"tool": "search", "q": "x", "filters": map[string]any{"min-seeders": 500}})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = read(map[string]any{"tool": "other", "q": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
	})

	It("answers an empty text with no results and no request", func() {
		out, err := read(map[string]any{"tool": "search", "q": "  "})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["results"]).To(BeEmpty())
		Expect(got).To(BeNil())
	})

	It("says in a plain sentence that the site failed, with no address in it", func() {
		status = http.StatusBadGateway
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(Equal("Pirate Face answered status 502"))
		Expect(out["results"]).To(BeEmpty())
	})

	It("refuses an oversized page", func() {
		body = strings.Repeat("a", 5<<20)
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(ContainSubstring("larger"))
	})

	It("does not say where it was pointed when the site cannot be reached", func() {
		srv.Close()
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(Equal("Pirate Face did not answer"))
	})
})

var _ = Describe("the manifest", func() {
	load := func() (contract.Manifest, []byte) {
		raw, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		return m, raw
	}

	It("is valid for this host and declares the one tool of the code", func() {
		m, _ := load()
		h := contract.HostCompatibility{Host: 1, API: 2, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: "linux", Arch: "amd64"}
		Expect(contract.ValidateManifest(m, h)).To(Succeed())
		Expect(m.Capabilities.Tools).To(Equal(search.Declared()))
		Expect(m.Capabilities.UI).To(BeNil(), "the host draws the rows and the filter")
		Expect(m.Capabilities.ModelSearch.Source.ID).To(Equal("pirateface"), "the stored choice and the saved queries name this ID")
		Expect(m.Capabilities.ModelSearch.Filters[0].ID).To(Equal(search.FilterMinSeeders))
		Expect(m.Capabilities.ModelSearch.Source.HasVariants).To(BeTrue())
		Expect(m.Capabilities.ModelSearch.Source.P2P).To(BeTrue())
		Expect(m.Capabilities.Network).To(BeNil(), "like the Web add-on, it calls its own site and declares no network capability")
	})

	It("takes the arguments that the host sends", func() {
		m, _ := load()
		def := m.Capabilities.ToolDefinitions[0]
		src := modelsearch.Source{ID: "pirateface", Filters: m.Capabilities.ModelSearch.Filters}
		own, err := modelsearch.ValidateFilters(src, map[string]string{"min-seeders": "3"})
		Expect(err).NotTo(HaveOccurred())
		// The same request shape the host builds for a source.
		req := contract.ModelSearchRequest{Query: "qwen", Limit: 20, Sort: "downloads", Format: "gguf", Filters: map[string]any{"min-seeders": float64(3)}}
		Expect(own).To(HaveKey("min-seeders"))
		raw, _ := json.Marshal(req)
		_, bound, err := toolargs.BindDefinition(def, raw)
		Expect(err).NotTo(HaveOccurred())
		var a map[string]any
		Expect(json.Unmarshal(bound, &a)).To(Succeed())
		Expect(a["tool"]).To(Equal("search"))
	})
})
