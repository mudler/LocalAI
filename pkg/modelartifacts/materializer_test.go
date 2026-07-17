package modelartifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type fakeSnapshotResolver struct {
	mu       sync.Mutex
	snapshot hfapi.Snapshot
	err      error
	calls    int
}

func (f *fakeSnapshotResolver) ResolveSnapshot(context.Context, hfapi.SnapshotRequest) (hfapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snapshot, f.err
}

func (f *fakeSnapshotResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ = Describe("controller artifact materializer", func() {
	It("requires an injected token before resolving a private artifact", func() {
		resolver := &fakeSnapshotResolver{}
		_, err := modelartifacts.NewManager(resolver).Ensure(context.Background(), GinkgoT().TempDir(),
			modelartifacts.Spec{Source: modelartifacts.Source{
				Type: modelartifacts.SourceTypeHuggingFace, Repo: "owner/private", TokenEnv: modelartifacts.HuggingFaceTokenEnv,
			}})
		Expect(err).To(MatchError(ContainSubstring("non-empty HF_TOKEN")))
		Expect(resolver.callCount()).To(BeZero())
	})

	It("downloads, commits, and reuses a pinned snapshot", func() {
		weight := []byte("weight-bytes")
		sum := sha256.Sum256(weight)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer hf-secret"))
			w.Header().Set("Content-Length", "12")
			_, _ = w.Write(weight)
		}))
		DeferCleanup(server.Close)

		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			RequestedRevision: "main", ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: []hfapi.SnapshotFile{{Path: "nested/model.safetensors", Size: 12, LFSOID: hex.EncodeToString(sum[:]), URL: server.URL + "/model"}},
		}}
		manager := modelartifacts.NewManager(resolver, modelartifacts.WithHuggingFaceToken("hf-secret"))
		modelsPath := GinkgoT().TempDir()
		spec := modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo", TokenEnv: "HF_TOKEN"}}

		result, err := manager.Ensure(context.Background(), modelsPath, spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.CacheHit).To(BeFalse())
		Expect(result.Spec.Resolved.Revision).To(Equal("0123456789abcdef0123456789abcdef01234567"))
		// A snapshot with a single file records it as the primary file so the
		// load target is the file, not the snapshot directory.
		Expect(result.Spec.Resolved.PrimaryFile).To(Equal("nested/model.safetensors"))
		Expect(result.RelativePath).To(HavePrefix(".artifacts/huggingface/"))
		Expect(os.ReadFile(filepath.Join(modelsPath, filepath.FromSlash(result.RelativePath), "nested", "model.safetensors"))).To(Equal(weight))

		manifestBytes, err := os.ReadFile(filepath.Join(modelsPath, filepath.Dir(filepath.FromSlash(result.RelativePath)), "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(manifestBytes)).NotTo(ContainSubstring("hf-secret"))

		cached, err := manager.Ensure(context.Background(), modelsPath, result.Spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(cached.CacheHit).To(BeTrue())
		Expect(requests.Load()).To(Equal(int32(1)))
		Expect(resolver.callCount()).To(Equal(1))
	})

	It("leaves the primary file unset for a multi-file snapshot", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
		DeferCleanup(server.Close)
		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files: []hfapi.SnapshotFile{
				{Path: "config.json", Size: 1, URL: server.URL + "/config"},
				{Path: "model.safetensors", Size: 1, URL: server.URL + "/model"},
			},
		}}
		result, err := modelartifacts.NewManager(resolver).Ensure(context.Background(), GinkgoT().TempDir(),
			modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}})
		Expect(err).NotTo(HaveOccurred())
		// Multi-file snapshots are consumed as a directory (e.g. transformers), so
		// no single file is promoted.
		Expect(result.Spec.Resolved.PrimaryFile).To(BeEmpty())
	})

	It("rejects a path escape before opening a destination", func() {
		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files:            []hfapi.SnapshotFile{{Path: "../escape", Size: 1, URL: "https://example.invalid/file"}},
		}}
		modelsPath := GinkgoT().TempDir()
		_, err := modelartifacts.NewManager(resolver).Ensure(context.Background(), modelsPath,
			modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}})
		Expect(err).To(MatchError(ContainSubstring("unsafe Hub path")))
		Expect(filepath.Join(modelsPath, "escape")).NotTo(BeAnExistingFile())
	})

	It("does not commit after cancellation", func() {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		DeferCleanup(server.Close)
		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files:            []hfapi.SnapshotFile{{Path: "model.bin", Size: 10, URL: server.URL}},
		}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		modelsPath := GinkgoT().TempDir()
		go func() {
			_, err := modelartifacts.NewManager(resolver).Ensure(ctx, modelsPath,
				modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}})
			done <- err
		}()
		<-started
		cancel()
		Expect(<-done).To(MatchError(context.Canceled))
		entries, err := filepath.Glob(filepath.Join(modelsPath, ".artifacts", "huggingface", "*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("emits resolving, downloading, verifying, and committing phases", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
		DeferCleanup(server.Close)
		resolver := &fakeSnapshotResolver{snapshot: hfapi.Snapshot{
			Endpoint: "https://huggingface.co", Repo: "owner/repo",
			ResolvedRevision: "0123456789abcdef0123456789abcdef01234567",
			Files:            []hfapi.SnapshotFile{{Path: "config.json", Size: 1, URL: server.URL}},
		}}
		var phases []modelartifacts.Phase
		ctx := modelartifacts.WithProgressSink(context.Background(), func(event modelartifacts.ProgressEvent) {
			if len(phases) == 0 || phases[len(phases)-1] != event.Phase {
				phases = append(phases, event.Phase)
			}
		})
		_, err := modelartifacts.NewManager(resolver).Ensure(ctx, GinkgoT().TempDir(),
			modelartifacts.Spec{Source: modelartifacts.Source{Type: "huggingface", Repo: "owner/repo"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(phases).To(Equal([]modelartifacts.Phase{
			modelartifacts.PhaseResolving, modelartifacts.PhaseDownloading, modelartifacts.PhaseVerifying, modelartifacts.PhaseCommitting,
		}))
	})
})
