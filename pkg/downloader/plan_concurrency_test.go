package downloader_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
)

var _ = Describe("DownloadFilesWithConcurrency", func() {
	// slowServer holds every request open until it has seen `hold` of them at
	// once, or the client gives up. A sequential executor can never satisfy a
	// hold above one, so this doubles as proof that parallelism really happens
	// rather than just being configured.
	slowServer := func(delay time.Duration) (*httptest.Server, *int32) {
		var inFlight int32
		var peak int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				observed := atomic.LoadInt32(&peak)
				if current <= observed || atomic.CompareAndSwapInt32(&peak, observed, current) {
					break
				}
			}
			time.Sleep(delay)
			atomic.AddInt32(&inFlight, -1)
			_, _ = w.Write([]byte("payload"))
		}))
		return server, &peak
	}

	tasksFor := func(server *httptest.Server, dir string, count int) []downloader.FileTask {
		tasks := make([]downloader.FileTask, 0, count)
		for i := 0; i < count; i++ {
			tasks = append(tasks, downloader.FileTask{
				URI:         downloader.URI(fmt.Sprintf("%s/file-%d", server.URL, i)),
				Destination: filepath.Join(dir, fmt.Sprintf("file-%d.bin", i)),
				FileIndex:   i,
				TotalFiles:  count,
			})
		}
		return tasks
	}

	It("overlaps transfers up to the limit and no further", func() {
		server, peak := slowServer(60 * time.Millisecond)
		DeferCleanup(server.Close)

		tasks := tasksFor(server, GinkgoT().TempDir(), 8)
		err := downloader.DownloadFilesWithConcurrency(context.Background(), tasks, nil, 3)

		Expect(err).NotTo(HaveOccurred())
		Expect(*peak).To(BeNumerically(">", 1), "downloads never overlapped, so the limit was not applied")
		Expect(*peak).To(BeNumerically("<=", 3), "more transfers ran at once than the configured limit")
	})

	It("keeps a concurrency of one strictly sequential", func() {
		server, peak := slowServer(10 * time.Millisecond)
		DeferCleanup(server.Close)

		tasks := tasksFor(server, GinkgoT().TempDir(), 5)
		err := downloader.DownloadFilesWithConcurrency(context.Background(), tasks, nil, 1)

		Expect(err).NotTo(HaveOccurred())
		Expect(*peak).To(Equal(int32(1)), "a limit of one must never overlap transfers")
	})

	It("treats a non-positive concurrency as sequential", func() {
		server, peak := slowServer(10 * time.Millisecond)
		DeferCleanup(server.Close)

		tasks := tasksFor(server, GinkgoT().TempDir(), 4)
		err := downloader.DownloadFilesWithConcurrency(context.Background(), tasks, nil, 0)

		Expect(err).NotTo(HaveOccurred())
		Expect(*peak).To(Equal(int32(1)))
	})

	It("reports the first hook error and stops starting new work", func() {
		server, _ := slowServer(0)
		DeferCleanup(server.Close)

		var started int32
		tasks := tasksFor(server, GinkgoT().TempDir(), 24)
		for i := range tasks {
			index := i
			tasks[i].AfterDownload = func(string) error {
				atomic.AddInt32(&started, 1)
				if index == 0 {
					return fmt.Errorf("verification failed for shard %d", index)
				}
				time.Sleep(20 * time.Millisecond)
				return nil
			}
		}

		err := downloader.DownloadFilesWithConcurrency(context.Background(), tasks, nil, 2)

		Expect(err).To(MatchError(ContainSubstring("verification failed for shard 0")))
		Expect(atomic.LoadInt32(&started)).To(BeNumerically("<", int32(len(tasks))),
			"the executor kept starting work after a failure instead of cancelling")
	})

	It("returns the caller's cancellation rather than running the plan", func() {
		server, _ := slowServer(0)
		DeferCleanup(server.Close)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var ran int32
		tasks := tasksFor(server, GinkgoT().TempDir(), 3)
		for i := range tasks {
			tasks[i].AfterDownload = func(string) error {
				atomic.AddInt32(&ran, 1)
				return nil
			}
		}

		err := downloader.DownloadFilesWithConcurrency(ctx, tasks, nil, 4)

		Expect(err).To(MatchError(context.Canceled))
		Expect(atomic.LoadInt32(&ran)).To(BeZero())
	})

	It("serializes the status callback so callers need no locking of their own", func() {
		server, _ := slowServer(5 * time.Millisecond)
		DeferCleanup(server.Close)

		// A deliberately unsynchronized counter: if the executor let two
		// callbacks in at once, -race would flag this write.
		unguarded := 0

		tasks := tasksFor(server, GinkgoT().TempDir(), 6)
		err := downloader.DownloadFilesWithConcurrency(context.Background(), tasks, func(string, string, string, float64) {
			unguarded++
		}, 4)

		Expect(err).NotTo(HaveOccurred())
		Expect(unguarded).To(BeNumerically(">", 0))
	})
})
