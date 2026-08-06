package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stampVersion pins a recognisable build version for the duration of a spec so
// the expected User-Agent is not the empty-version ("source build") form, which
// would still match if the version were dropped from the header.
func stampVersion() {
	GinkgoHelper()
	saved := internal.Version
	internal.Version = "v9.9.9"
	DeferCleanup(func() { internal.Version = saved })
}

// expectUserAgent fails when the header is not exactly what internal.UserAgent
// produces, and separately when it does not name the build version — the second
// check is what catches a header that is set but carries the wrong identity.
func expectUserAgent(site, got string) {
	GinkgoHelper()
	Expect(got).To(Equal(internal.UserAgent()), "%s: wrong User-Agent", site)
	Expect(got).To(ContainSubstring("LocalAI/v9.9.9"), "%s: User-Agent does not name the build version", site)
}

func specTempDir() string {
	GinkgoHelper()
	dir, err := os.MkdirTemp("", "downloader-useragent-spec-*")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

var _ = Describe("the outbound User-Agent", func() {
	BeforeEach(stampVersion)

	// The gallery index is fetched through this package. Without a User-Agent
	// the request is indistinguishable from any other Go program, which is both
	// unhelpful to the hosts serving us and inconsistent with pkg/oci, which has
	// always identified itself.
	It("is sent by ReadWithCallback", func() {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
			_, _ = w.Write([]byte("- name: a\n"))
		}))
		DeferCleanup(srv.Close)

		uri := downloader.URI(srv.URL)
		Expect(uri.ReadWithCallback(specTempDir(), func(string, []byte) error { return nil })).To(Succeed())

		expectUserAgent("gallery read", got)
	})

	// Model files are the bulk of what LocalAI pulls; they go through
	// newDownloadRequest, which every download and every resume probe shares.
	It("is sent by DownloadFile", func() {
		seen := make(chan string, 8)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen <- r.Header.Get("User-Agent")
			w.Header().Set("Accept-Ranges", "bytes")
			_, _ = w.Write([]byte("payload"))
		}))
		DeferCleanup(srv.Close)

		uri := downloader.URI(srv.URL + "/file.bin")
		target := filepath.Join(specTempDir(), "file.bin")
		Expect(uri.DownloadFile(target, "", 1, 1, func(string, string, string, float64) {})).To(Succeed())

		close(seen)
		n := 0
		for ua := range seen {
			n++
			expectUserAgent("download", ua)
		}
		Expect(n).ToNot(BeZero(), "server saw no requests")
	})

	// ContentLength builds its own HEAD request rather than going through
	// newDownloadRequest, so it needs its own coverage.
	It("is sent by ContentLength's HEAD", func() {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
			w.Header().Set("Content-Length", "7")
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(srv.Close)

		size, err := downloader.URI(srv.URL + "/file.bin").ContentLength(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(size).To(BeEquivalentTo(7))

		expectUserAgent("content-length HEAD", got)
	})

	// When the HEAD carries no Content-Length, ContentLength falls back to a
	// one-byte Range GET built at a third, separate site.
	It("is sent by ContentLength's Range GET fallback", func() {
		var rangeUA string
		var sawRange bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				// No Content-Length: this is what pushes ContentLength onto the
				// Range fallback path.
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			sawRange = true
			rangeUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Range", "bytes 0-0/4242")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		}))
		DeferCleanup(srv.Close)

		size, err := downloader.URI(srv.URL + "/file.bin").ContentLength(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(size).To(BeEquivalentTo(4242))
		Expect(sawRange).To(BeTrue(), "server never saw the Range GET; the fallback path was not exercised")

		expectUserAgent("content-length Range GET", rangeUA)
	})

	// The HuggingFace safety scan is the one outbound request in this package
	// that does not live in uri.go, and it was the easiest one to overlook.
	It("is sent by the HuggingFace safety scan", func() {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("User-Agent")
			_, _ = w.Write([]byte(`{"repositoryId":"owner/repo","scansDone":true}`))
		}))
		DeferCleanup(srv.Close)

		savedEndpoint := downloader.HF_ENDPOINT
		downloader.HF_ENDPOINT = srv.URL
		DeferCleanup(func() { downloader.HF_ENDPOINT = savedEndpoint })

		uri := downloader.URI(srv.URL + "/owner/repo/resolve/main/model.gguf")
		_, err := downloader.HuggingFaceScan(uri)
		Expect(err).ToNot(HaveOccurred())

		expectUserAgent("huggingface scan", got)
	})
})
