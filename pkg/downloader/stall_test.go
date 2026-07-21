package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Download stall timeout", func() {
	var filePath string
	var savedTimeout time.Duration

	BeforeEach(func() {
		filePath = GinkgoT().TempDir() + "/stall_model"
		savedTimeout = DownloadStallTimeout
	})

	AfterEach(func() {
		DownloadStallTimeout = savedTimeout
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".partial")
	})

	It("aborts a download that stalls mid-stream instead of hanging forever", func() {
		// Server sends a chunk, flushes, then blocks forever without closing
		// the connection — a silently-dropped TCP stream. Without a stall
		// guard the body Read blocks indefinitely and DownloadFile never
		// returns.
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 4096))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-release // hang: no more data, never close
		}))
		defer server.Close()
		defer close(release)

		DownloadStallTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stall"))
	})

	It("preserves the .partial file when a download stalls so it can resume", func() {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 4096))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-release
		}))
		defer server.Close()
		defer close(release)

		DownloadStallTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()
		Eventually(done, "5s").Should(Receive(HaveOccurred()))

		info, statErr := os.Stat(filePath + ".partial")
		Expect(statErr).ToNot(HaveOccurred(), "the .partial must survive a stall so the next attempt can resume")
		Expect(info.Size()).To(BeNumerically(">", 0))
	})

	It("does not abort a slow-but-steady download", func() {
		// One byte every 100ms keeps the idle clock from ever expiring even
		// though the total transfer outlasts the stall timeout.
		payload := make([]byte, 12)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			f, _ := w.(http.Flusher)
			for i := range payload {
				_, _ = w.Write(payload[i : i+1])
				if f != nil {
					f.Flush()
				}
				time.Sleep(100 * time.Millisecond)
			}
		}))
		defer server.Close()

		DownloadStallTimeout = 300 * time.Millisecond

		err := URI(server.URL).DownloadFileWithContext(
			context.Background(), filePath, "", 1, 1,
			func(s1, s2, s3 string, f float64) {})
		Expect(err).ToNot(HaveOccurred())
	})
})
