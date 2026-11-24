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

var _ = Describe("Download response header timeout", func() {
	var filePath string
	var savedHeaderTimeout time.Duration

	BeforeEach(func() {
		dir, err := os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		filePath = dir + "/respheader_model"
		savedHeaderTimeout = DownloadResponseHeaderTimeout
	})

	AfterEach(func() {
		DownloadResponseHeaderTimeout = savedHeaderTimeout
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".partial")
	})

	It("aborts a request whose response headers never arrive instead of hanging forever", func() {
		// The server accepts the connection and reads the request, then never
		// writes a status line. The stall watchdog cannot help here: it wraps
		// the response body, and there is no response yet. Without a
		// ResponseHeaderTimeout on the transport, Do() parks forever and the
		// whole install freezes with zero bytes transferred.
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release // never write headers
		}))
		defer server.Close()
		defer close(release)

		DownloadResponseHeaderTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timeout awaiting response headers"))
	})

	It("classifies a response header timeout as retryable so the plan resumes", func() {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer server.Close()
		defer close(release)

		DownloadResponseHeaderTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(IsRetryable(context.Background(), err)).To(BeTrue(),
			"a wedged origin must be retried, not treated as a permanent failure")
	})

	It("aborts a resume probe whose response headers never arrive", func() {
		// The HEAD that probes for Range support runs before the body request
		// and uses the same client, so it is a second unguarded wait. A
		// leftover .partial is what puts that probe on the path.
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-release
		}))
		defer server.Close()
		defer close(release)

		Expect(os.WriteFile(filePath+".partial", make([]byte, 128), 0600)).To(Succeed())

		DownloadResponseHeaderTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()

		var err error
		Eventually(done, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timeout awaiting response headers"))
		Expect(IsRetryable(context.Background(), err)).To(BeTrue(),
			"a wedged resume probe must be retried, not aborted permanently")
	})

	It("does not bound the body once headers have arrived, so long downloads are not truncated", func() {
		// The deliberate "no body deadline" property must survive: headers come
		// back promptly, then the body trickles for longer than the header
		// timeout. This must succeed.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Length", "4")
			w.WriteHeader(http.StatusOK)
			for i := 0; i < 4; i++ {
				_, _ = w.Write([]byte{byte('a' + i)})
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(150 * time.Millisecond)
			}
		}))
		defer server.Close()

		DownloadResponseHeaderTimeout = 300 * time.Millisecond

		done := make(chan error, 1)
		go func() {
			done <- URI(server.URL).DownloadFileWithContext(
				context.Background(), filePath, "", 1, 1,
				func(s1, s2, s3 string, f float64) {})
		}()

		var err error
		Eventually(done, "10s").Should(Receive(&err))
		Expect(err).ToNot(HaveOccurred())
		content, readErr := os.ReadFile(filePath)
		Expect(readErr).ToNot(HaveOccurred())
		Expect(string(content)).To(Equal("abcd"))
	})
})
