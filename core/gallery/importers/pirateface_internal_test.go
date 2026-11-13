package importers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pirate Face import", func() {
	const sha = "2e8040ceae7815abe0dcb3540b9995eaa1fa0d2ca9e797d0a635ae4433c68c2d"
	var (
		srv        *httptest.Server
		savedPF    string
		savedHFAPI func(string) (*hfapi.ModelDetails, error)
		lookups    int
	)

	BeforeEach(func() {
		lookups = 0
		savedPF, savedHFAPI = downloader.PirateFaceEndpoint, hfMetadata
		hfMetadata = func(string) (*hfapi.ModelDetails, error) { return nil, errors.New("offline") }
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/torrents" || r.URL.Query().Get("repo") != "acme/tiny-GGUF" {
				http.NotFound(w, r)
				return
			}
			lookups++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"repoId": "acme/tiny-GGUF", "revision": strings.Repeat("a", 40), "quant": "q1",
				"files": []map[string]string{
					{"path": "README.md", "sha256": strings.Repeat("1", 64)},
					{"path": "tiny-Q4_K_M.gguf", "sha256": sha},
				},
				"quants": []map[string]any{{"quant": "q1", "files": []map[string]string{
					{"path": "tiny-Q4_K_M.gguf", "sha256": sha},
				}}, {"quant": "q2", "files": []map[string]string{
					{"path": "tiny-Q8_0.gguf", "sha256": strings.Repeat("2", 64)},
				}}},
			})
		}))
		downloader.PirateFaceEndpoint = srv.URL
	})
	AfterEach(func() {
		srv.Close()
		downloader.PirateFaceEndpoint, hfMetadata = savedPF, savedHFAPI
	})

	It("recognises page URLs and the shorthand, and nothing else", func() {
		ref, ok := pirateFaceImportRef(srv.URL + "/acme/tiny-GGUF")
		Expect(ok).To(BeTrue())
		Expect(ref.RepoID()).To(Equal("acme/tiny-GGUF"))
		ref, ok = pirateFaceImportRef("pirateface://acme/tiny-GGUF/tiny-Q4_K_M.gguf")
		Expect(ok).To(BeTrue())
		Expect(ref.Path).To(Equal("tiny-Q4_K_M.gguf"))
		_, ok = pirateFaceImportRef("https://huggingface.co/acme/tiny-GGUF")
		Expect(ok).To(BeFalse())
		_, ok = pirateFaceImportRef("huggingface://acme/tiny-GGUF")
		Expect(ok).To(BeFalse())
	})

	It("lists the files of every torrent as pirateface:// URLs", func() {
		ref, _ := pirateFaceImportRef(srv.URL + "/acme/tiny-GGUF")
		details, err := pirateFaceModelDetails(ref)
		Expect(err).ToNot(HaveOccurred())
		var urls []string
		for _, f := range details.Files {
			urls = append(urls, f.URL)
		}
		Expect(urls).To(ConsistOf(
			"pirateface://acme/tiny-GGUF/README.md",
			"pirateface://acme/tiny-GGUF/tiny-Q4_K_M.gguf",
			"pirateface://acme/tiny-GGUF/tiny-Q8_0.gguf"))
		Expect(details.ReadmeFile).ToNot(BeNil())
	})

	It("imports a model page URL into a config that downloads over BitTorrent", func() {
		cfg, err := DiscoverModelConfig(srv.URL+"/acme/tiny-GGUF", json.RawMessage(`{}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.ConfigFile).To(ContainSubstring("backend: llama-cpp"))
		Expect(cfg.Files).ToNot(BeEmpty())
		Expect(cfg.Files[0].URI).To(HavePrefix("pirateface://acme/tiny-GGUF/"))
		Expect(cfg.Files[0].URI).To(HaveSuffix(".gguf"))
		Expect(cfg.Files[0].Filename).To(HaveSuffix(".gguf"))
		for _, f := range cfg.Files {
			Expect(f.URI).ToNot(ContainSubstring("huggingface"))
		}
		Expect(lookups).To(BeNumerically(">", 0))
	})

	It("imports the pirateface:// shorthand the same way", func() {
		cfg, err := DiscoverModelConfig("pirateface://acme/tiny-GGUF", json.RawMessage(`{}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Files[0].URI).To(HavePrefix("pirateface://acme/tiny-GGUF/"))
	})

	It("imports one file named in a pirateface:// URI", func() {
		cfg, err := DiscoverModelConfig("pirateface://acme/tiny-GGUF/tiny-Q4_K_M.gguf", json.RawMessage(`{}`))
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Files).To(HaveLen(1))
		Expect(cfg.Files[0].URI).To(Equal("pirateface://acme/tiny-GGUF/tiny-Q4_K_M.gguf"))
		Expect(cfg.Files[0].Filename).To(Equal("tiny-Q4_K_M.gguf"))
	})

	It("reports a repository the index does not list", func() {
		_, err := DiscoverModelConfig(srv.URL+"/acme/unknown", json.RawMessage(`{}`))
		Expect(err).To(HaveOccurred())
	})
})
