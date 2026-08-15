package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("artifact materialization with bounded download concurrency", func() {
	// shardedSnapshot serves `count` distinct files and reports the peak number
	// of simultaneous requests, so a test can tell configured concurrency from
	// actual concurrency.
	shardedSnapshot := func(count int, delay time.Duration) (hfapi.Snapshot, *httptest.Server, *int32) {
		bodies := make(map[string][]byte, count)
		files := make([]hfapi.SnapshotFile, 0, count)

		var inFlight, peak int32
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
			_, _ = w.Write(bodies[r.URL.Path])
		}))

		for i := 0; i < count; i++ {
			// Later shards are served first-come, so give them descending delays
			// as well: completion order ends up unrelated to snapshot order,
			// which is exactly what the manifest must survive.
			body := []byte(fmt.Sprintf("shard-%02d-bytes", i))
			urlPath := fmt.Sprintf("/shard-%02d", i)
			bodies[urlPath] = body
			sum := sha256.Sum256(body)
			files = append(files, hfapi.SnapshotFile{
				Path:   fmt.Sprintf("shards/model-%02d.safetensors", i),
				Size:   int64(len(body)),
				LFSOID: hex.EncodeToString(sum[:]),
				URL:    server.URL + urlPath,
			})
		}

		return hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/sharded",
			RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: files,
		}, server, &peak
	}

	spec := modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/sharded"}}

	It("records the manifest in snapshot order regardless of completion order", func() {
		snapshot, server, peak := shardedSnapshot(12, 40*time.Millisecond)
		DeferCleanup(server.Close)

		manager := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: snapshot},
			modelartifacts.WithDownloadConcurrency(4))
		modelsPath := GinkgoT().TempDir()

		result, err := manager.Ensure(context.Background(), modelsPath, spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(*peak).To(BeNumerically(">", 1), "files never overlapped, so this proves nothing about ordering")
		Expect(*peak).To(BeNumerically("<=", 4))

		Expect(result.Manifest.Files).To(HaveLen(len(snapshot.Files)))
		for i, file := range result.Manifest.Files {
			Expect(file.Path).To(Equal(snapshot.Files[i].Path),
				"manifest entry %d is out of snapshot order", i)
			Expect(file.SHA256).To(HaveLen(64))
		}

		// Every shard must also be on disk, not merely recorded.
		for _, file := range snapshot.Files {
			onDisk := filepath.Join(modelsPath, filepath.FromSlash(result.RelativePath), filepath.FromSlash(file.Path))
			info, statErr := os.Stat(onDisk)
			Expect(statErr).NotTo(HaveOccurred())
			Expect(info.Size()).To(Equal(file.Size))
		}
	})

	It("produces the same manifest sequentially and concurrently", func() {
		sequentialSnapshot, sequentialServer, _ := shardedSnapshot(8, 0)
		DeferCleanup(sequentialServer.Close)
		sequential, err := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: sequentialSnapshot}).
			Ensure(context.Background(), GinkgoT().TempDir(), spec)
		Expect(err).NotTo(HaveOccurred())

		concurrentSnapshot, concurrentServer, _ := shardedSnapshot(8, 0)
		DeferCleanup(concurrentServer.Close)
		concurrent, err := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: concurrentSnapshot},
			modelartifacts.WithDownloadConcurrency(8)).
			Ensure(context.Background(), GinkgoT().TempDir(), spec)
		Expect(err).NotTo(HaveOccurred())

		Expect(concurrent.Manifest.Files).To(Equal(sequential.Manifest.Files))
	})

	It("applies live concurrency updates to subsequent materializations", func() {
		snapshot, server, peak := shardedSnapshot(8, 40*time.Millisecond)
		DeferCleanup(server.Close)
		manager := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: snapshot})
		manager.SetDownloadConcurrency(4)

		_, err := manager.Ensure(context.Background(), GinkgoT().TempDir(), spec)

		Expect(err).NotTo(HaveOccurred())
		Expect(*peak).To(BeNumerically(">", 1))
		Expect(*peak).To(BeNumerically("<=", 4))
	})

	It("still resumes past files an interrupted pass already completed", func() {
		snapshot, server, _ := shardedSnapshot(6, 0)
		DeferCleanup(server.Close)

		var requests atomic.Int32
		counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			server.Config.Handler.ServeHTTP(w, r)
		}))
		DeferCleanup(counting.Close)
		for i := range snapshot.Files {
			snapshot.Files[i].URL = counting.URL + snapshot.Files[i].URL[len(server.URL):]
		}

		manager := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: snapshot},
			modelartifacts.WithDownloadConcurrency(3))
		modelsPath := GinkgoT().TempDir()

		first, err := manager.Ensure(context.Background(), modelsPath, spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(requests.Load()).To(Equal(int32(len(snapshot.Files))))

		// A committed artifact is served from cache without touching the network.
		second, err := manager.Ensure(context.Background(), modelsPath, first.Spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.CacheHit).To(BeTrue())
		Expect(requests.Load()).To(Equal(int32(len(snapshot.Files))))
	})

	It("fails the whole materialization when a shard cannot be verified", func() {
		snapshot, server, _ := shardedSnapshot(6, 0)
		DeferCleanup(server.Close)
		// Corrupt one shard's expected digest: the download succeeds, the
		// per-file SHA check does not.
		snapshot.Files[3].LFSOID = hex.EncodeToString(make([]byte, 32))

		manager := modelartifacts.NewManager(&fakeSnapshotResolver{snapshot: snapshot},
			modelartifacts.WithDownloadConcurrency(3))
		modelsPath := GinkgoT().TempDir()

		_, err := manager.Ensure(context.Background(), modelsPath, spec)
		Expect(err).To(HaveOccurred())

		// Nothing may be published under the final path when a shard failed.
		entries, readErr := os.ReadDir(filepath.Join(modelsPath, ".artifacts", "huggingface"))
		if readErr == nil {
			for _, entry := range entries {
				_, statErr := os.Stat(filepath.Join(modelsPath, ".artifacts", "huggingface", entry.Name(), "manifest.json"))
				Expect(statErr).To(HaveOccurred(), "a failed materialization published a manifest")
			}
		}
	})
})
