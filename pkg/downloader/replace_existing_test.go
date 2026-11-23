package downloader_test

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Reinstalling a file whose expected hash changed must not delete the working
// copy before the replacement is complete and verified.
var _ = Describe("DownloadFile replacing an existing file", func() {
	var (
		oldContent = []byte("old weights")
		newContent = []byte("new weights, a bit longer")
		newSHA     = fmt.Sprintf("%x", sha256.Sum256(newContent))
		oldSHA     = fmt.Sprintf("%x", sha256.Sum256(oldContent))
		dest       string
		noProgress = func(string, string, string, float64) {}
	)

	BeforeEach(func() {
		dest = filepath.Join(GinkgoT().TempDir(), "model.bin")
		Expect(os.WriteFile(dest, oldContent, 0600)).To(Succeed())
	})

	serve := func(body []byte, hits *atomic.Int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = w.Write(body)
		}))
		DeferCleanup(srv.Close)
		return srv
	}

	It("replaces the old file once the new one is verified", func() {
		var hits atomic.Int32
		srv := serve(newContent, &hits)

		Expect(URI(srv.URL).DownloadFile(dest, newSHA, 1, 1, noProgress)).To(Succeed())

		got, err := os.ReadFile(dest)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(newContent))
		Expect(dest + ".partial").ToNot(BeAnExistingFile())
	})

	It("keeps the old file when the downloaded bytes do not match the hash", func() {
		var hits atomic.Int32
		srv := serve([]byte("tampered content"), &hits)

		err := URI(srv.URL).DownloadFile(dest, newSHA, 1, 1, noProgress)
		Expect(err).To(HaveOccurred())

		got, rerr := os.ReadFile(dest)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(got).To(Equal(oldContent))
	})

	It("keeps the old file when the server fails", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		DeferCleanup(srv.Close)

		Expect(URI(srv.URL).DownloadFile(dest, newSHA, 1, 1, noProgress)).ToNot(Succeed())

		got, err := os.ReadFile(dest)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(oldContent))
	})

	It("keeps the old file when the connection drops mid-transfer", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(len(newContent)))
			_, _ = w.Write(newContent[:5])
			w.(http.Flusher).Flush()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		DeferCleanup(srv.Close)

		Expect(URI(srv.URL).DownloadFile(dest, newSHA, 1, 1, noProgress)).ToNot(Succeed())

		got, err := os.ReadFile(dest)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(oldContent))
	})

	It("skips the download when the hash already matches", func() {
		var hits atomic.Int32
		srv := serve(newContent, &hits)

		Expect(URI(srv.URL).DownloadFile(dest, oldSHA, 1, 1, noProgress)).To(Succeed())

		Expect(hits.Load()).To(BeZero())
		got, err := os.ReadFile(dest)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(oldContent))
	})
})
