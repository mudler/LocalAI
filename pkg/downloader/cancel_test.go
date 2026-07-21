package downloader_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Download cancellation", func() {
	var filePath string

	// streamingRangeServer serves data one small chunk at a time with a short
	// pause between chunks, so a context cancellation can land mid-transfer.
	// It honors a `bytes=N-` Range request so a second attempt can resume.
	streamingRangeServer := func(data []byte) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "HEAD" {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			start := 0
			if rh := r.Header.Get("Range"); rh != "" {
				_, _ = fmt.Sscanf(strings.TrimPrefix(rh, "bytes="), "%d-", &start)
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
			if start > 0 {
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			f, _ := w.(http.Flusher)
			for i := start; i < len(data); i += 256 {
				end := i + 256
				if end > len(data) {
					end = len(data)
				}
				if _, err := w.Write(data[i:end]); err != nil {
					return
				}
				if f != nil {
					f.Flush()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}))
	}

	BeforeEach(func() {
		filePath = GinkgoT().TempDir() + "/cancel_model"
	})

	AfterEach(func() {
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".partial")
	})

	It("keeps the .partial file when the context is cancelled so the download can resume", func() {
		data := make([]byte, 8192)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		server := streamingRangeServer(data)
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel()
		}()

		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, "", 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())

		info, statErr := os.Stat(filePath + ".partial")
		Expect(statErr).ToNot(HaveOccurred(),
			"a cancelled download must leave its .partial behind so the retry resumes instead of restarting from zero")
		Expect(info.Size()).To(BeNumerically(">", 0))
		Expect(info.Size()).To(BeNumerically("<", int64(len(data))))
	})

	It("discards the .partial when the cancellation cause is ErrUserCancelled", func() {
		data := make([]byte, 8192)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		server := streamingRangeServer(data)
		defer server.Close()

		// A deliberate user abort: cancel WITH the ErrUserCancelled cause. The
		// half-finished download should not linger on disk.
		ctx, cancel := context.WithCancelCause(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel(ErrUserCancelled)
		}()

		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, "", 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.Canceled)).To(BeTrue())

		Expect(filePath+".partial").ToNot(BeAnExistingFile(),
			"a deliberate user cancel must not leave a dangling .partial behind")
	})

	It("resumes from the preserved .partial after a cancellation and completes", func() {
		data := make([]byte, 8192)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(data)
		sha := fmt.Sprintf("%x", sum)
		server := streamingRangeServer(data)
		defer server.Close()

		// First attempt: cancel mid-stream.
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel()
		}()
		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		partialInfo, statErr := os.Stat(filePath + ".partial")
		Expect(statErr).ToNot(HaveOccurred())
		resumedFrom := partialInfo.Size()
		Expect(resumedFrom).To(BeNumerically(">", 0))

		// Second attempt: fresh context, must resume and finish with a valid SHA.
		err = URI(server.URL).DownloadFileWithContext(context.Background(), filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).ToNot(HaveOccurred())
		final, rerr := os.ReadFile(filePath)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(final).To(Equal(data))
	})
})
