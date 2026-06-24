package downloader_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
)

var _ = Describe("DownloadFilesWithContext", func() {
	It("runs the post-download hook after fetching each file", func() {
		payload := []byte("downloaded-bytes")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(payload)
		}))
		DeferCleanup(server.Close)

		dest := filepath.Join(GinkgoT().TempDir(), "model.bin")
		hookCalled := false

		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: dest,
			FileIndex:   0,
			TotalFiles:  1,
			AfterDownload: func(path string) error {
				hookCalled = true
				got, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(got)).To(Equal(string(payload)))
				return nil
			},
		}}, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(hookCalled).To(BeTrue())
	})

	It("weights progress by bytes and preserves transfer sinks", func() {
		payloads := map[string][]byte{
			"/small": bytes.Repeat([]byte("s"), 10),
			"/large": bytes.Repeat([]byte("l"), 90),
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			payload := payloads[r.URL.Path]
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(payload)
			}
		}))
		DeferCleanup(server.Close)

		var mu sync.Mutex
		percentages := []float64{}
		events := []downloader.TransferProgress{}
		tasks := []downloader.FileTask{}
		for index, path := range []string{"/small", "/large"} {
			tasks = append(tasks, downloader.FileTask{
				URI:         downloader.URI(server.URL + path),
				Destination: filepath.Join(GinkgoT().TempDir(), filepath.Base(path)),
				FileIndex:   index,
				TotalFiles:  2,
			})
		}

		err := downloader.DownloadFilesWithContext(context.Background(), tasks, func(_ string, _, _ string, percentage float64) {
			mu.Lock()
			defer mu.Unlock()
			percentages = append(percentages, percentage)
		}, downloader.WithTransferProgress(func(event downloader.TransferProgress) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}))

		Expect(err).NotTo(HaveOccurred())
		Expect(percentages).NotTo(BeEmpty())
		Expect(percentages[0]).To(BeNumerically("~", 10.0, 0.01))
		Expect(percentages).To(HaveEach(BeNumerically("<=", 100)))
		Expect(events).To(HaveLen(2))
	})

	It("falls back to per-file progress when a size cannot be resolved", func() {
		payload := []byte("downloaded")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(payload)
		}))
		DeferCleanup(server.Close)

		percentages := []float64{}
		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{{
			URI:         downloader.URI(server.URL),
			Destination: filepath.Join(GinkgoT().TempDir(), "fallback.bin"),
			TotalFiles:  1,
		}}, func(_ string, _, _ string, percentage float64) {
			percentages = append(percentages, percentage)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(percentages).NotTo(BeEmpty())
		Expect(percentages[len(percentages)-1]).To(Equal(float64(100)))
	})
})
