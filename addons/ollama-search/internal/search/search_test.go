// SPDX-License-Identifier: MIT
package search_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/ollama-search/internal/search"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"github.com/mudler/LocalAI/pkg/modelsearch"
)

func env(args map[string]any) contract.Envelope {
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "ollama-search", OperationID: "op-1", Generation: 1, Action: "read", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "ann", CredentialID: "c1", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Hour)},
	}
}

var _ = Describe("the search tool", func() {
	var (
		srv    *httptest.Server
		got    []*http.Request
		status = http.StatusOK
		pages  map[string]string
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
	query := func() url.Values {
		Expect(got).To(HaveLen(1))
		return got[0].URL.Query()
	}
	BeforeEach(func() {
		status, got = http.StatusOK, nil
		pages = map[string]string{"/search": page("search-qwen.html"), "/library/qwen3/tags": page("tags-qwen3.html")}
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = append(got, r)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(pages[r.URL.Path]))
		}))
		DeferCleanup(srv.Close)
		rt = search.New(search.Options{BaseURL: srv.URL + "/"})
	})

	It("reads the HTML search page, sends the user agent, and never asks for an /api or /v1 path", func() {
		out, err := read(map[string]any{"tool": "search", "q": "qwen 3/x&c=cloud", "limit": 20, "sort": "downloads", "format": "gguf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].URL.Path).To(Equal("/search"))
		Expect(got[0].Header.Get("User-Agent")).To(Equal("LocalAI model search (+https://github.com/mudler/LocalAI)"))
		for _, r := range got {
			Expect(r.URL.Path).NotTo(HavePrefix("/api"))
			Expect(r.URL.Path).NotTo(HavePrefix("/v1"))
		}
		Expect(query().Get("q")).To(Equal("qwen 3/x&c=cloud"), "the text is one value, however it looks")
		Expect(query()["c"]).To(Equal([]string{"local"}), "the text cannot add a filter")
		Expect(out["results"]).To(HaveLen(14))
	})

	It("always asks for local models, so a model that runs only in the cloud never comes back", func() {
		_, err := read(map[string]any{"tool": "search", "q": "qwen"})
		Expect(err).NotTo(HaveOccurred())
		Expect(query()["c"]).To(ContainElement("local"))
	})

	It("maps the sorts: downloads is popular (the default), updated and latest are newest, the rest are ignored", func() {
		for sort, want := range map[string]string{"downloads": "", "updated": "newest", "latest": "newest", "likes": "", "trending": "", "": ""} {
			got = nil
			_, err := read(map[string]any{"tool": "search", "q": "qwen", "sort": sort})
			Expect(err).NotTo(HaveOccurred())
			Expect(query().Get("o")).To(Equal(want), "sort %q", sort)
		}
	})

	It("lists the newest models when the text is empty and the sort is latest, and the popular ones otherwise", func() {
		_, err := read(map[string]any{"tool": "search", "q": "", "sort": "latest"})
		Expect(err).NotTo(HaveOccurred())
		Expect(query().Get("o")).To(Equal("newest"))
		Expect(query().Has("q")).To(BeFalse())
		got = nil
		_, err = read(map[string]any{"tool": "search", "q": " "})
		Expect(err).NotTo(HaveOccurred())
		Expect(query().Get("o")).To(BeEmpty())
	})

	It("maps each capability filter to a c value and sends nothing for a filter that is off", func() {
		_, err := read(map[string]any{"tool": "search", "q": "qwen", "filters": map[string]any{"tools": true, "vision": true, "thinking": false}})
		Expect(err).NotTo(HaveOccurred())
		Expect(query()["c"]).To(Equal([]string{"local", "tools", "vision"}))
		got = nil
		_, err = read(map[string]any{"tool": "search", "q": "qwen", "filters": map[string]any{"thinking": true}})
		Expect(err).NotTo(HaveOccurred())
		Expect(query()["c"]).To(Equal([]string{"local", "thinking"}))
	})

	It("refuses a filter it does not declare, one that is not a bool, and another tool", func() {
		for _, f := range []map[string]any{{"embedding": true}, {"cloud": true}, {"tools": "yes"}, {"tools": 1}} {
			_, err := read(map[string]any{"tool": "search", "q": "x", "filters": f})
			Expect(err).To(MatchError(protocol.ErrBadRequest), "%v", f)
		}
		_, err := read(map[string]any{"tool": "other", "q": "x"})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		Expect(got).To(BeEmpty())
	})

	It("keeps the limit, and caps it at 50", func() {
		out, err := read(map[string]any{"tool": "search", "q": "qwen", "limit": 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["results"]).To(HaveLen(5))
		// More than the page gives is not an error, and a request over the cap is
		// cut at the cap.
		pages["/search"] = wrap("60", func() string {
			var b strings.Builder
			for i := range 60 {
				b.WriteString(item("m"+strings.Repeat("a", i), badge("1b"), ""))
			}
			return b.String()
		}())
		out, err = read(map[string]any{"tool": "search", "q": "qwen", "limit": 80})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["results"]).To(HaveLen(50))
	})

	It("cuts the text at 200 bytes", func() {
		_, err := read(map[string]any{"tool": "search", "q": strings.Repeat("é", 300)})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(query().Get("q"))).To(BeNumerically("<=", 200))
		Expect(len(query().Get("q"))).To(BeNumerically(">", 150))
	})

	It("follows the redirect of the site that moves the parameters", func() {
		srv.Close()
		moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/search" && !r.URL.Query().Has("moved") {
				http.Redirect(w, r, "/search?moved=1&q=qwen", http.StatusSeeOther)
				return
			}
			_, _ = w.Write([]byte(pages["/search"]))
		}))
		DeferCleanup(moved.Close)
		rt = search.New(search.Options{BaseURL: moved.URL})
		out, err := read(map[string]any{"tool": "search", "q": "qwen"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["results"]).To(HaveLen(14))
	})

	It("answers an empty list with no error for a page with no results", func() {
		pages["/search"] = page("search-empty.html")
		out, err := read(map[string]any{"tool": "search", "q": "zzqxjkvv"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(HaveKey("error"))
		Expect(out["results"]).To(BeEmpty())
	})

	It("answers an error that names the page when the layout is not the expected one", func() {
		pages["/search"] = "<html><body><h1>Maintenance</h1></body></html>"
		out, err := read(map[string]any{"tool": "search", "q": "qwen"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(ContainSubstring("Ollama search page"))
		Expect(out["results"]).To(BeEmpty())

		pages["/search"] = wrap("20", "<li>new markup</li>")
		out, err = read(map[string]any{"tool": "search", "q": "qwen"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(ContainSubstring("lists none"))
	})

	It("says in a plain sentence that the site failed, with the status and no address in it", func() {
		status = http.StatusBadGateway
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(Equal("Ollama answered status 502"))
		Expect(out["results"]).To(BeEmpty())
	})

	It("refuses an oversized page", func() {
		pages["/search"] = strings.Repeat("a", 5<<20)
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(ContainSubstring("larger"))
	})

	It("does not say where it was pointed when the site cannot be reached", func() {
		srv.Close()
		out, err := read(map[string]any{"tool": "search", "q": "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out["error"]).To(Equal("Ollama did not answer"))
	})

	Describe("variants", func() {
		It("reads the tags page of the model, only on request", func() {
			_, err := read(map[string]any{"tool": "search", "q": "qwen"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].URL.Path).To(Equal("/search"), "a search never reads a tags page")
			got = nil

			out, err := read(map[string]any{"tool": "search", "q": "qwen3", "variants": "qwen3"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].URL.Path).To(Equal("/library/qwen3/tags"))
			Expect(got[0].Header.Get("User-Agent")).To(ContainSubstring("LocalAI"))
			vs, ok := out["variants"].([]any)
			Expect(ok).To(BeTrue())
			Expect(vs).To(HaveLen(58))
			first := vs[0].(map[string]any)
			Expect(first["tag"]).To(Equal("latest"))
			Expect(first["uri"]).To(Equal("ollama://qwen3:latest"))
			Expect(first["size_bytes"]).To(BeNumerically("==", 5_200_000_000))
			Expect(first["context_tokens"]).To(BeNumerically("==", 40960))
		})

		It("refuses a name that is not a library model name", func() {
			for _, n := range []string{"../api/tags", "a/b", "x?y", "-a", "a b"} {
				_, err := read(map[string]any{"tool": "search", "q": "x", "variants": n})
				Expect(err).To(MatchError(protocol.ErrBadRequest), n)
			}
			Expect(got).To(BeEmpty())
		})

		It("answers an error with the status, or one that names the page for a layout it does not know", func() {
			status = http.StatusNotFound
			out, err := read(map[string]any{"tool": "search", "q": "x", "variants": "nope"})
			Expect(err).NotTo(HaveOccurred())
			Expect(out["error"]).To(Equal("Ollama answered status 404"))
			Expect(out["variants"]).To(BeEmpty())

			status = http.StatusOK
			pages["/library/qwen3/tags"] = "<html><body>redesign</body></html>"
			out, err = read(map[string]any{"tool": "search", "q": "x", "variants": "qwen3"})
			Expect(err).NotTo(HaveOccurred())
			Expect(out["error"]).To(ContainSubstring("tags page of qwen3"))
		})
	})
})

var _ = Describe("the manifest", func() {
	load := func() contract.Manifest {
		raw, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
		return m
	}

	It("is valid for this host and declares the one tool of the code", func() {
		m := load()
		h := contract.HostCompatibility{Host: 1, API: 2, Protocol: 1, SDK: contract.HostSDKVersion, DataFormat: 1, FeatureLevel: contract.HostFeatureLevel, OS: "linux", Arch: "amd64"}
		Expect(contract.ValidateManifest(m, h)).To(Succeed())
		Expect(m.Capabilities.Tools).To(Equal(search.Declared()))
		Expect(m.Capabilities.UI).To(BeNil(), "the host draws the rows and the filters")
		Expect(m.Capabilities.Network).To(BeNil(), "like the other search add-ons, it declares no network capability")
		src := m.Capabilities.ModelSearch.Source
		Expect(src.ID).To(Equal("ollama"), "the stored choice and the saved queries name this ID")
		Expect(src.Title).To(Equal("Ollama"))
		Expect(src.HasVariants).To(BeFalse(), "the host does not call the variants argument yet")
		Expect(src.ListsLatest).To(BeTrue())
		Expect(src.P2P).To(BeFalse())
		Expect(m.Capabilities.ModelSearch.Sorts).To(Equal([]string{"downloads", "updated", "latest"}))
		var ids []string
		for _, f := range m.Capabilities.ModelSearch.Filters {
			Expect(f.Type).To(Equal(contract.FilterBool))
			ids = append(ids, f.ID)
		}
		Expect(ids).To(Equal([]string{search.FilterTools, search.FilterVision, search.FilterThinking}))
		Expect(m.Capabilities.ToolDefinitions[0].Kind).To(Equal("read"))
	})

	It("takes the arguments that the host sends, bool filters included", func() {
		m := load()
		def := m.Capabilities.ToolDefinitions[0]
		src := modelsearch.Source{ID: "ollama", Filters: m.Capabilities.ModelSearch.Filters}
		own, err := modelsearch.ValidateFilters(src, map[string]string{"tools": "true", "vision": "false"})
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(HaveKey("tools"))
		req := contract.ModelSearchRequest{Query: "qwen", Limit: 20, Sort: "latest", Format: "gguf", Filters: map[string]any{"tools": true}}
		raw, _ := json.Marshal(req)
		_, bound, err := toolargs.BindDefinition(def, raw)
		Expect(err).NotTo(HaveOccurred())
		var a map[string]any
		Expect(json.Unmarshal(bound, &a)).To(Succeed())
		Expect(a["tool"]).To(Equal("search"))
		Expect(a["filters"]).To(Equal(map[string]any{"tools": true}))
	})
})
