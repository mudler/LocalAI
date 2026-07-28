package downloader_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
)

// flakyRangeServer serves payload, honours Range requests, and aborts the
// connection part-way through the body for the first failUntil GET requests.
// Aborting mid-body is how a peer-cancelled HTTP/2 stream or a dropped
// connection presents to the client: the read fails, the write never does.
func flakyRangeServer(payload []byte, failUntil int) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var seenRanges []string
	gets := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
			return
		}

		rangeHeader := r.Header.Get("Range")
		mu.Lock()
		gets++
		attempt := gets
		seenRanges = append(seenRanges, rangeHeader)
		mu.Unlock()

		start := 0
		if strings.HasPrefix(rangeHeader, "bytes=") {
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rangeHeader, "bytes="), "-"))
			Expect(err).ToNot(HaveOccurred())
			start = n
		}
		body := payload[start:]

		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if start > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if attempt <= failUntil {
			// Send a prefix, then kill the connection: the client's Read fails
			// with an unexpected EOF while the local file write succeeded.
			_, _ = w.Write(body[:len(body)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write(body)
	}))

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seenRanges...)
	}
}

var _ = Describe("download failure diagnostics", func() {
	var (
		payload    []byte
		payloadSHA string
		destDir    string
		destPath   string
		noProgress = func(string, string, string, float64) {}
	)

	BeforeEach(func() {
		payload = make([]byte, 65536)
		_, err := rand.Read(payload)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(payload)
		payloadSHA = fmt.Sprintf("%x", sum[:])

		destDir = GinkgoT().TempDir()
		destPath = filepath.Join(destDir, "model.gguf")
	})

	// Defect 1: io.Copy folds read and write failures into a single error, and
	// the download path labelled every one of them "failed to write file".
	// A peer-cancelled stream then reads as a filesystem problem, which is
	// exactly the wrong place to look.
	It("does not report a failed response read as a write failure", func() {
		server, _ := flakyRangeServer(payload, 1)
		DeferCleanup(server.Close)

		err := downloader.URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).ToNot(ContainSubstring("failed to write file"),
			"the local write succeeded; only the response body read failed")
		Expect(err.Error()).To(ContainSubstring(server.URL),
			"a read-side failure should name the source it was reading from")
	})

	// The partial, not the final blob path, is the file actually being written.
	It("names the partial file when the local write is the side that failed", func() {
		// A directory in place of the partial makes every write fail while the
		// response body stays healthy.
		Expect(os.MkdirAll(destPath+downloader.PartialFileSuffix, 0750)).To(Succeed())

		server, _ := flakyRangeServer(payload, 0)
		DeferCleanup(server.Close)

		err := downloader.URI(server.URL).DownloadFile(destPath, payloadSHA, 1, 1, noProgress)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(destPath + downloader.PartialFileSuffix))
	})
})

var _ = Describe("DownloadFilesWithContext retries", func() {
	var (
		payload    []byte
		payloadSHA string
		destPath   string
	)

	BeforeEach(func() {
		payload = make([]byte, 65536)
		_, err := rand.Read(payload)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(payload)
		payloadSHA = fmt.Sprintf("%x", sum[:])

		destPath = filepath.Join(GinkgoT().TempDir(), "model.gguf")

		original := downloader.DownloadRetryBaseDelay
		downloader.DownloadRetryBaseDelay = time.Millisecond
		DeferCleanup(func() { downloader.DownloadRetryBaseDelay = original })
	})

	// Defect 2: one cancelled stream half-way through a multi-file plan threw
	// away every file already fetched. The .partial resume machinery existed
	// but nothing retried, so it was unreachable.
	It("retries a transient stream failure and resumes from the partial", func() {
		server, ranges := flakyRangeServer(payload, 2)
		DeferCleanup(server.Close)

		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: destPath,
			SHA256:      payloadSHA,
			FileIndex:   1,
			TotalFiles:  1,
		}}, nil)
		Expect(err).ToNot(HaveOccurred())

		got, err := os.ReadFile(destPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(payload))

		seen := ranges()
		Expect(seen).To(HaveLen(3), "expected two failed attempts and one success, got %v", seen)
		Expect(seen[0]).To(BeEmpty())
		for _, r := range seen[1:] {
			Expect(r).To(HavePrefix("bytes="), "retries must resume, not restart: %v", seen)
			Expect(r).ToNot(Equal("bytes=0-"), "retries must resume, not restart: %v", seen)
		}
	})

	It("does not retry a permanent failure", func() {
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusNotFound)
		}))
		DeferCleanup(server.Close)

		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: destPath,
			FileIndex:   1,
			TotalFiles:  1,
		}}, nil)
		Expect(err).To(HaveOccurred())
		Expect(attempts).To(Equal(1), "a 404 is permanent and must not be retried")
	})

	It("does not retry once the caller's context is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(http.StatusOK)
				return
			}
			attempts++
			cancel()
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:len(payload)/2])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler)
		}))
		DeferCleanup(server.Close)

		err := downloader.DownloadFilesWithContext(ctx, []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: destPath,
			SHA256:      payloadSHA,
			FileIndex:   1,
			TotalFiles:  1,
		}}, nil)
		Expect(err).To(HaveOccurred())
		Expect(attempts).To(Equal(1), "a cancelled caller must not be retried through")
	})
})
