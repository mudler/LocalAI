package downloader_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

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

	It("reaches but never exceeds the configured concurrency limit", func() {
		var active atomic.Int32
		var maximum atomic.Int32
		started := make(chan struct{}, 5)
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			_, _ = w.Write([]byte(r.URL.Path))
		}))
		DeferCleanup(server.Close)

		tasks := make([]downloader.FileTask, 5)
		for i := range tasks {
			tasks[i] = downloader.FileTask{
				URI: downloader.URI(server.URL + "/file"), Destination: filepath.Join(GinkgoT().TempDir(), "file"),
				FileIndex: i, TotalFiles: len(tasks),
			}
		}
		done := make(chan error, 1)
		go func() {
			done <- downloader.DownloadFilesWithContext(context.Background(), tasks, nil, downloader.WithFileConcurrency(2))
		}()
		Eventually(started).Should(Receive())
		Eventually(started).Should(Receive())
		Consistently(active.Load, 100*time.Millisecond).Should(Equal(int32(2)))
		close(release)
		Expect(<-done).NotTo(HaveOccurred())
		Expect(maximum.Load()).To(Equal(int32(2)))
	})

	It("cancels a blocked sibling and returns the permanent task error", func() {
		blockedStarted := make(chan struct{})
		blockedCanceled := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/blocked" {
				close(blockedStarted)
				<-r.Context().Done()
				close(blockedCanceled)
				return
			}
			<-blockedStarted
			w.WriteHeader(http.StatusNotFound)
		}))
		DeferCleanup(server.Close)

		err := downloader.DownloadFilesWithContext(context.Background(), []downloader.FileTask{
			{URI: downloader.URI(server.URL + "/blocked"), Destination: filepath.Join(GinkgoT().TempDir(), "blocked"), TotalFiles: 2},
			{URI: downloader.URI(server.URL + "/missing"), Destination: filepath.Join(GinkgoT().TempDir(), "missing"), FileIndex: 1, TotalFiles: 2},
		}, nil, downloader.WithFileConcurrency(2))

		Expect(err).To(MatchError(ContainSubstring("404")))
		Eventually(blockedCanceled).Should(BeClosed())
	})
})
