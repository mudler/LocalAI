// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These are real stores; only the model HTTP endpoint is deterministic.
func memoryTestBackend(engine, name, root, url string, handler http.Handler) *backendInProcess {
	srv := httptest.NewServer(handler)
	DeferCleanup(srv.Close)
	b, _ := NewInProcessBackend(&Config{LLMAPIURL: srv.URL + "/v1", CollectionDBPath: filepath.Join(root, "db"), FileAssets: filepath.Join(root, "assets"), VectorEngine: engine, DatabaseURL: url, EmbeddingModel: "deterministic", MaxChunkingSize: 12})
	impl := b.(*backendInProcess)
	err := b.CreateCollection(name)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		impl.Close()
		for _, kb := range impl.state.Collections {
			if kb != nil {
				kb.Close()
			}
		}
	})
	return impl
}
func memoryEmbedding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
}
func memoryRequest(key, content string) MemoryWrite {
	h, err := MemoryPayloadHash(content, nil)
	Expect(err).NotTo(HaveOccurred())
	return MemoryWrite{Key: key, Content: content, PayloadHash: h}
}

var _ = Describe("Durable memory adapter", func() {
	DescribeTable("preserves receipts and exact deletion across reopen", func(engine string) {
		url := os.Getenv("MEMORY_DATABASE_URL")
		if engine == "postgres" && url == "" {
			Skip("MEMORY_DATABASE_URL is not set")
		}
		name := fmt.Sprintf("adapter_%d", time.Now().UnixNano())
		root := GinkgoT().TempDir()
		b := memoryTestBackend(engine, name, root, url, http.HandlerFunc(memoryEmbedding))
		ctx := context.Background()
		caps, err := b.MemoryCapabilities(ctx, name)
		Expect(err).NotTo(HaveOccurred())
		Expect(caps.Ready).To(BeTrue())
		Expect(caps.Supported).To(BeTrue())
		Expect(caps.ExactDelete).To(BeTrue())
		Expect(caps.DurableReceipts).To(BeTrue())
		Expect(caps.DistributedFencing).To(Equal((engine == "postgres")))
		req := memoryRequest("key", "one two three four five six seven eight nine ten")
		absent, err := b.ReconcileMemory(ctx, name, req.Key)
		Expect(err).NotTo(HaveOccurred())
		Expect(absent.State).To(Equal(MemoryAbsent))
		receipt, err := b.WriteMemory(ctx, name, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.EntryID).NotTo(BeEmpty())
		Expect(receipt.Deleted).To(BeFalse())
		Expect(len(receipt.ChunkIDs)).To(BeNumerically(">=", 2))
		replay, err := b.WriteMemory(ctx, name, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.EntryID).To(Equal(receipt.EntryID))
		bad := memoryRequest(req.Key, "different")
		_, err = b.WriteMemory(ctx, name, bad)
		Expect(err).To(MatchError(ErrMemoryConflict))
		hits, err := b.Search(ctx, name, "one", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].EntryID).To(Equal(receipt.EntryID))
		Expect(hits[0].ID == receipt.EntryID).To(BeFalse())
		for _, id := range []string{"memory.txt", "*", "../" + receipt.EntryID} {
			_, err = b.DeleteMemoryEntry(ctx, name, id)
			Expect(err).To(MatchError(ErrMemoryNotFound))
		}
		_, err = b.DeleteMemoryEntry(ctx, name, receipt.EntryID)
		Expect(err).NotTo(HaveOccurred())
		// Close actual engines before reopening: chromem ownership must be released.
		for _, kb := range b.state.Collections {
			kb.Close()
		}
		b.Close()
		b = memoryTestBackend(engine, name, root, url, http.HandlerFunc(memoryEmbedding))
		tombstone, err := b.WriteMemory(ctx, name, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(!tombstone.Deleted).To(BeFalse())
		Expect(tombstone.EntryID).To(Equal(receipt.EntryID))
		recovered, err := b.ReconcileMemory(ctx, name, req.Key)
		Expect(err).NotTo(HaveOccurred())
		Expect(!recovered.Deleted).To(BeFalse())
		_, err = b.WriteMemory(ctx, name, bad)
		Expect(err).To(MatchError(ErrMemoryConflict))
		Expect(b.EntryExists(name, receipt.EntryID)).To(BeFalse(), fmt.Sprint("deleted entry recreated"))
		err = b.Reset(name)
		Expect(err).To(MatchError(ErrMemoryConflict))
	}, Entry("chromem", "chromem"), Entry("postgres", "postgres"))

	It("fences writes across two real PostgreSQL pools", func() {
		url := os.Getenv("MEMORY_DATABASE_URL")
		if url == "" {
			Skip("MEMORY_DATABASE_URL is not set")
		}
		name := fmt.Sprintf("adapter_race_%d", time.Now().UnixNano())
		b1 := memoryTestBackend("postgres", name, GinkgoT().TempDir(), url, http.HandlerFunc(memoryEmbedding))
		b2 := memoryTestBackend("postgres", name, GinkgoT().TempDir(), url, http.HandlerFunc(memoryEmbedding))
		ctx := context.Background()
		req := memoryRequest("shared", "one two three four five six")
		f1, err := b1.BeginMemoryAttempt(ctx, name, req.Key, "one")
		Expect(err).NotTo(HaveOccurred())
		f2, err := b2.BeginMemoryAttempt(ctx, name, req.Key, "two")
		Expect(err).NotTo(HaveOccurred())
		_, err = b1.WriteMemoryFenced(ctx, name, f1, req)
		Expect(err).To(MatchError(ErrMemoryStaleFence))
		var wg sync.WaitGroup
		for _, b := range []*backendInProcess{b1, b2} {
			wg.Add(1)
			go func(b *backendInProcess) {
				defer wg.Done()
				defer GinkgoRecover()
				_, err := b.WriteMemoryFenced(ctx, name, f2, req)
				Expect(err).NotTo(HaveOccurred())
			}(b)
		}
		wg.Wait()
		r, err := b1.ReconcileMemory(ctx, name, req.Key)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(MemorySaved))
		text, _, err := b2.GetEntryContent(name, r.EntryID)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal(req.Content))
		_, err = b2.DeleteMemoryEntry(ctx, name, r.EntryID)
		Expect(err).NotTo(HaveOccurred())
		r, err = b1.WriteMemoryFenced(ctx, name, f2, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(!r.Deleted).To(BeFalse())
	})

	// An embedding failure occurs after the durable preparing manifest; reopening
	// and reconciling must finish that exact payload rather than use Upload.
	DescribeTable("reconciles preparing manifests after reopen", func(engine string) {
		url := os.Getenv("MEMORY_DATABASE_URL")
		if engine == "postgres" && url == "" {
			Skip("MEMORY_DATABASE_URL is not set")
		}
		name := fmt.Sprintf("adapter_partial_%d", time.Now().UnixNano())
		root := GinkgoT().TempDir()
		var mu sync.Mutex
		fail := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			broken := fail
			mu.Unlock()
			if broken {
				http.Error(w, "injected embedding error", http.StatusBadRequest)
				return
			}
			memoryEmbedding(w, r)
		})
		b := memoryTestBackend(engine, name, root, url, handler)
		req := memoryRequest("partial", "one two three four five six seven eight nine")
		mu.Lock()
		fail = true
		mu.Unlock()
		if _, err := b.WriteMemory(context.Background(), name, req); err == nil {
			Fail(fmt.Sprint("expected embedding failure"))
		}
		for _, kb := range b.state.Collections {
			kb.Close()
		}
		b.Close()
		b = memoryTestBackend(engine, name, root, url, http.HandlerFunc(memoryEmbedding))
		r, err := b.ReconcileMemory(context.Background(), name, req.Key)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(MemorySaved))
		Expect(r.PayloadHash).To(Equal(req.PayloadHash))
		text, _, err := b.GetEntryContent(name, r.EntryID)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(Equal(req.Content))
		replay, err := b.WriteMemory(context.Background(), name, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.EntryID).To(Equal(r.EntryID))
	}, Entry("chromem", "chromem"), Entry("postgres", "postgres"))

	It("fails closed for unsupported engines, missing collections and canceled calls", func() {
		ctx := context.Background()
		for _, engine := range []string{"localai", "unknown", ""} {
			b := &backendInProcess{cfg: &Config{VectorEngine: engine}}
			caps, err := b.MemoryCapabilities(ctx, "principal-collection")
			Expect(err).NotTo(HaveOccurred())
			Expect(caps.Supported).To(BeFalse())
			req := memoryRequest("key", "text")
			_, err = b.WriteMemory(ctx, "principal-collection", req)
			Expect(err).To(MatchError(ErrMemoryUnsupported))
			_, err = b.BeginMemoryAttempt(ctx, "principal-collection", req.Key, "owner")
			Expect(err).To(MatchError(ErrMemoryUnsupported))
			_, err = b.ReconcileMemory(ctx, "principal-collection", req.Key)
			Expect(err).To(MatchError(ErrMemoryUnsupported))
			_, err = b.DeleteMemoryEntry(ctx, "principal-collection", "entry")
			Expect(err).To(MatchError(ErrMemoryUnsupported))
		}
		b := memoryTestBackend("chromem", "exists", GinkgoT().TempDir(), "", http.HandlerFunc(memoryEmbedding))
		if _, err := b.WriteMemory(ctx, "missing", memoryRequest("key", "text")); err == nil {
			Fail(fmt.Sprint("created missing collection"))
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := b.WriteMemory(canceled, "exists", memoryRequest("key", "text"))
		Expect(err).To(MatchError(context.Canceled))
	})
})
