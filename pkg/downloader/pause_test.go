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

var _ = Describe("Download pause and resume", func() {
	var filePath string

	pauseRangeServer := func(data []byte) *httptest.Server {
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
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
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
		dir, err := os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		filePath = dir + "/pause_model"
	})

	AfterEach(func() {
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".partial")
	})

	It("preserves the .partial when paused with ErrUserPaused (critical: no delete)", func() {
		data := make([]byte, 8192)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		server := pauseRangeServer(data)
		defer server.Close()

		ctx, cancel := context.WithCancelCause(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel(ErrUserPaused)
		}()

		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, "", 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUserPaused)).To(BeTrue(), "should return ErrUserPaused, not context.Canceled")

		info, statErr := os.Stat(filePath + ".partial")
		Expect(statErr).ToNot(HaveOccurred(),
			"CRITICAL: .partial must exist after pause — a deleted .partial means resume is impossible")
		Expect(info.Size()).To(BeNumerically(">", 0))
		Expect(info.Size()).To(BeNumerically("<", int64(len(data))))
	})

	It("resumes a paused download from the .partial offset and completes with correct SHA", func() {
		data := make([]byte, 16384)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(data)
		sha := fmt.Sprintf("%x", sum)
		server := pauseRangeServer(data)
		defer server.Close()

		// First attempt: pause mid-stream with ErrUserPaused.
		ctx, cancel := context.WithCancelCause(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel(ErrUserPaused)
		}()
		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUserPaused)).To(BeTrue())

		partialInfo, statErr := os.Stat(filePath + ".partial")
		Expect(statErr).ToNot(HaveOccurred())
		resumedFrom := partialInfo.Size()
		Expect(resumedFrom).To(BeNumerically(">", 0))

		// Second attempt: fresh context, must resume via Range and verify SHA.
		err = URI(server.URL).DownloadFileWithContext(context.Background(), filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).ToNot(HaveOccurred())

		final, rerr := os.ReadFile(filePath)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(final).To(Equal(data))
	})

	It("pauses and resumes multiple times", func() {
		data := make([]byte, 32768)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(data)
		sha := fmt.Sprintf("%x", sum)
		server := pauseRangeServer(data)
		defer server.Close()

		var prevSize int64

		for i := 0; i < 3; i++ {
			ctx, cancel := context.WithCancelCause(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel(ErrUserPaused)
			}()

			err := URI(server.URL).DownloadFileWithContext(ctx, filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrUserPaused)).To(BeTrue())

			info, statErr := os.Stat(filePath + ".partial")
			Expect(statErr).ToNot(HaveOccurred(), "round %d: .partial must survive pause", i)
			Expect(info.Size()).To(BeNumerically(">", prevSize),
				"round %d: download must have made progress after resume", i)
			prevSize = info.Size()
		}

		// Final resume: complete the download.
		err = URI(server.URL).DownloadFileWithContext(context.Background(), filePath, sha, 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).ToNot(HaveOccurred())

		final, rerr := os.ReadFile(filePath)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(final).To(Equal(data))
	})

	It("returns ErrUserPaused not context.Canceled (caller can distinguish)", func() {
		data := make([]byte, 4096)
		_, err := rand.Read(data)
		Expect(err).ToNot(HaveOccurred())
		server := pauseRangeServer(data)
		defer server.Close()

		ctx, cancel := context.WithCancelCause(context.Background())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel(ErrUserPaused)
		}()

		err = URI(server.URL).DownloadFileWithContext(ctx, filePath, "", 1, 1, func(s1, s2, s3 string, f float64) {})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrUserPaused)).To(BeTrue(),
			"must return ErrUserPaused so GalleryService can distinguish pause from failure")
		Expect(errors.Is(err, context.Canceled)).To(BeFalse(),
			"must NOT return context.Canceled — the caller would treat it as a system cancel, not a user pause")
	})
})
