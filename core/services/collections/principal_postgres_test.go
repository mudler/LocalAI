// SPDX-License-Identifier: MIT
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/localrecall/rag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mudler/LocalAI/core/config"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/localrecall/rag/engine"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The production Service, registry and vector store are real. Only embeddings
// and the cluster transport are fixtures. Each frontend has a separate disk.
func principalPostgresServices(withoutBus ...bool) (*Service, *Service, string) {
	raw := os.Getenv("MEMORY_DATABASE_URL")
	if raw == "" {
		Skip("MEMORY_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, raw)
	Expect(err).NotTo(HaveOccurred())
	schema := fmt.Sprintf("principal_probe_%d", time.Now().UnixNano())
	_, err = conn.Exec(ctx, "CREATE SCHEMA "+schema)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_, e := conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		Expect(e).NotTo(HaveOccurred())
		Expect(conn.Close(ctx)).To(Succeed())
	})
	u, err := url.Parse(raw)
	Expect(err).NotTo(HaveOccurred())
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	var bus messaging.Broadcaster = testutil.NewFakeBus()
	if len(withoutBus) > 0 && withoutBus[0] {
		bus = nil
	}
	open := func() *Service {
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "postgres"
		cfg.Collections.DatabaseURL = u.String()
		cfg.Collections.PostgresNamespace = schema
		cfg.Collections.EmbeddingModel = "test"
		s, e := New(cfg, bus, WithHTTPClient(embeddingClient()))
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		return s
	}
	first, second := open(), open()
	// Scoped relations deliberately ignore DSN search_path. Drop only this
	// fixture deployment's deterministic schemas after closing its backends.
	DeferCleanup(func() {
		first.Close()
		second.Close()
		for _, user := range []string{"", userA, userB} {
			identity, _ := json.Marshal([]string{"localai.collections.v1", schema, user})
			scope := engine.NewPostgresScope(string(identity))
			_, e := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{scope.Schema}.Sanitize()+" CASCADE")
			Expect(e).NotTo(HaveOccurred())
		}
	})
	return first, second, u.String()
}

var _ = Describe("PostgreSQL principal isolation reproduction", func() {
	It("isolates same-name legacy uploads, searches and exact deletion", func() {
		a, b, dbURL := principalPostgresServices()
		ctx := context.Background()
		const name = "same_notes"
		Expect(a.CreateForUser(userA, name)).To(Succeed())
		// Before B owns a local collection, a guessed name is denied.
		_, denied := b.SearchForUser(ctx, userB, name, "private", 10)
		Expect(denied).To(HaveOccurred())
		Expect(b.CreateForUser(userB, name)).To(Succeed())
		entryA, err := a.UploadForUser(ctx, userA, name, "a.txt", strings.NewReader("private alpha content"))
		Expect(err).NotTo(HaveOccurred())
		entryB, err := b.UploadForUser(ctx, userB, name, "b.txt", strings.NewReader("private beta content"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entryB).NotTo(Equal(entryA))
		hitsA, err := a.SearchForUser(ctx, userA, name, "private", 10)
		Expect(err).NotTo(HaveOccurred())
		hitsB, err := b.SearchForUser(ctx, userB, name, "private", 10)
		Expect(err).NotTo(HaveOccurred())
		entriesB, err := b.ListEntriesForUser(userB, name)
		Expect(err).NotTo(HaveOccurred())
		_, _, contentErr := b.EntryContentForUser(userB, name, entryA)
		_, deleteErr := b.DeleteEntryForUser(userB, name, entryA)
		after, err := a.SearchForUser(ctx, userA, name, "private", 10)
		Expect(err).NotTo(HaveOccurred())
		registry, err := engine.ListPostgresCollections(ctx, dbURL)
		Expect(err).NotTo(HaveOccurred())
		root, err := b.ListForUser("")
		Expect(err).NotTo(HaveOccurred())
		fmt.Fprintf(GinkgoWriter, "legacy summary: registryCount=%d root=%q entriesB=%q contentErr=%v deleteErr=%v hitCounts=%d/%d after=%d\n", len(registry), root, entriesB, contentErr, deleteErr, len(hitsA), len(hitsB), len(after))
		Expect(hitsA).To(HaveLen(1), "A must not search B's document")
		Expect(hitsB).To(HaveLen(1), "B must not search A's document")
		Expect(hitsA[0].Content).To(Equal("private alpha content"))
		Expect(hitsB[0].Content).To(Equal("private beta content"))
		Expect(root).To(BeEmpty())
		Expect(contentErr).To(HaveOccurred())
		Expect(deleteErr).To(HaveOccurred())
	})

	It("isolates same-name memory writes, capture keys and exact deletion", func() {
		a, b, dbURL := principalPostgresServices()
		ctx := context.Background()
		const name = "same_memory"
		Expect(a.CreateForUser(userA, name)).To(Succeed())
		Expect(b.CreateForUser(userB, name)).To(Succeed())
		write := func(s *Service, user, key, content string) (lagi.MemoryReceipt, error) {
			hash, err := lagi.MemoryPayloadHash(content, nil)
			Expect(err).NotTo(HaveOccurred())
			return s.WriteMemory(ctx, user, name, lagi.MemoryWrite{Key: key, Content: content, PayloadHash: hash}, func(context.Context) error { return nil })
		}
		ra, err := write(a, userA, "shared_capture_key", "private alpha memory")
		Expect(err).NotTo(HaveOccurred())
		rb, sameKeyErr := write(b, userB, "shared_capture_key", "private beta memory")
		unique, err := write(b, userB, "beta_capture_key", "private beta unique memory")
		Expect(err).NotTo(HaveOccurred())
		hitsA, err := a.SearchForUser(ctx, userA, name, "private", 10)
		Expect(err).NotTo(HaveOccurred())
		hitsB, err := b.SearchForUser(ctx, userB, name, "private", 10)
		Expect(err).NotTo(HaveOccurred())
		deleted, deleteErr := b.DeleteMemory(ctx, userB, name, ra.EntryID, func(context.Context) error { return nil })
		reconciled, reconcileErr := a.ReconcileMemory(ctx, userA, name, "shared_capture_key")
		registry, err := engine.ListPostgresCollections(ctx, dbURL)
		Expect(err).NotTo(HaveOccurred())
		fmt.Fprintf(GinkgoWriter, "memory summary: sameKeyErr=%v hitCounts=%d/%d deleteState=%s deleteErr=%v Aafter=%s reconcileErr=%v registryCount=%d\n", sameKeyErr, len(hitsA), len(hitsB), deleted.State, deleteErr, reconciled.State, reconcileErr, len(registry))
		Expect(sameKeyErr).NotTo(HaveOccurred(), "capture keys must be principal-isolated")
		Expect(deleteErr).To(HaveOccurred(), "B must not delete A's exact memory ID")
		Expect(hitsA).To(HaveLen(1))
		Expect(hitsA[0].Content).To(Equal("private alpha memory"))
		Expect(hitsB).To(HaveLen(2))
		Expect(reconciled.State).To(Equal(lagi.MemorySaved))
		Expect(reconcileErr).NotTo(HaveOccurred())
		Expect(rb.EntryID).NotTo(Equal(ra.EntryID))
		Expect(unique.EntryID).NotTo(Equal(ra.EntryID))
	})

	It("reports unresolved legacy data without denying unrelated scoped collections", func() {
		a, b, raw := principalPostgresServices()
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, raw)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(conn.Close(ctx)).To(Succeed()) })
		_, err = conn.Exec(ctx, `CREATE TABLE collection_config(collection_name text PRIMARY KEY);INSERT INTO collection_config VALUES('ambiguous')`)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.CreateForUser(userA, "fresh")).To(Succeed())
		names, err := b.ListForUser(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"fresh"}))
		status, err := b.LegacyStatusForUser(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.ActionRequired).To(BeTrue())
		Expect(status.Message).NotTo(ContainSubstring("ambiguous"))
		Expect(b.CheckForUser(ctx, userA, "fresh")).To(Succeed())
		hash, err := lagi.MemoryPayloadHash("fresh content", nil)
		Expect(err).NotTo(HaveOccurred())
		receipt, err := b.WriteMemory(ctx, userA, "fresh", lagi.MemoryWrite{Key: "fresh", Content: "fresh content", PayloadHash: hash}, func(context.Context) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.EntryID).NotTo(BeEmpty())
		checks := []func() error{
			func() error { return a.CreateForUser(userA, "ambiguous") },
			func() error { return b.CheckForUser(ctx, userA, "ambiguous") },
			func() error { _, e := b.SearchForUser(ctx, userA, "ambiguous", "text", 10); return e },
			func() error {
				_, e := b.UploadForUser(ctx, userA, "ambiguous", "a.txt", strings.NewReader("text"))
				return e
			},
			func() error { _, e := b.ListEntriesForUser(userA, "ambiguous"); return e },
			func() error { _, _, e := b.EntryContentForUser(userA, "ambiguous", "entry"); return e },
			func() error { _, e := b.EntryFilePathForUser(userA, "ambiguous", "entry"); return e },
			func() error { return b.ResetForUser(userA, "ambiguous") },
			func() error { _, e := b.DeleteEntryForUser(userA, "ambiguous", "entry"); return e },
			func() error { return b.AddSourceForUser(userA, "ambiguous", "http://invalid", 10) },
			func() error { return b.RemoveSourceForUser(userA, "ambiguous", "http://invalid") },
			func() error { _, e := b.ListSourcesForUser(userA, "ambiguous"); return e },
			func() error {
				_, e := b.WriteMemory(ctx, userA, "ambiguous", lagi.MemoryWrite{Key: "key", Content: "fresh content", PayloadHash: hash}, func(context.Context) error { return nil })
				return e
			},
			func() error {
				_, e := b.WriteMemoryFenced(ctx, userA, "ambiguous", lagi.MemoryFence{Key: "key", Owner: "owner", Generation: 1}, lagi.MemoryWrite{Key: "key", Content: "fresh content", PayloadHash: hash}, func(context.Context) error { return nil })
				return e
			},
			func() error { _, e := b.BeginMemoryAttempt(ctx, userA, "ambiguous", "key", "owner"); return e },
			func() error { _, e := b.ReconcileMemory(ctx, userA, "ambiguous", "key"); return e },
			func() error {
				_, e := b.DeleteMemory(ctx, userA, "ambiguous", "entry", func(context.Context) error { return nil })
				return e
			},
		}
		for _, check := range checks {
			Expect(errors.Is(check(), ErrLegacyCollectionActionRequired)).To(BeTrue())
		}
		Expect(b.EntryExistsForUser(userA, "ambiguous", "entry")).To(BeFalse())
		Expect(os.MkdirAll(a.storage.CollectionsDir(userB), 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(a.storage.CollectionsDir(userB), "collection-local_only.json"), []byte(`{}`), 0600)).To(Succeed())
		Expect(errors.Is(a.CreateForUser(userB, "local_only"), ErrLegacyCollectionActionRequired)).To(BeTrue())
		var count int
		Expect(conn.QueryRow(ctx, "SELECT count(*) FROM collection_config WHERE collection_name='ambiguous'").Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
	})

	It("rejects startup autoload before external sources, embeddings or identity writes", func() {
		a, _, raw := principalPostgresServices()
		ctx := context.Background()
		var sourceCalls, embeddingCalls atomic.Int32
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sourceCalls.Add(1)
			_, _ = fmt.Fprint(w, "source content")
		}))
		DeferCleanup(source.Close)
		Expect(a.CreateForUser(userA, "restart_denied")).To(Succeed())
		Expect(a.CreateForUser(userA, "stale_sidecar")).To(Succeed())
		backend, err := a.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		shared := backend.(*sharedCollections)
		kb := shared.state.Collections["restart_denied"]
		// Persist source configuration without scheduling a fetch in this process.
		Expect(kb.AddExternalSource(&rag.ExternalSource{URL: source.URL, UpdateInterval: time.Hour})).To(Succeed())
		cfg := a.config(a.storage.CollectionsDir(userA), a.storage.AssetsDir(userA))
		a.scopeConfig(cfg, userA)
		a.Close()
		Expect(os.RemoveAll(filepath.Join(cfg.CollectionDBPath, "embedding-models"))).To(Succeed())
		conn, err := pgx.Connect(ctx, raw)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(conn.Close(ctx)).To(Succeed()) })
		_, err = conn.Exec(ctx, `CREATE TABLE collection_config(collection_name text PRIMARY KEY);INSERT INTO collection_config VALUES('restart_denied')`)
		Expect(err).NotTo(HaveOccurred())
		_, err = conn.Exec(ctx, "DELETE FROM "+pgx.Identifier{cfg.PostgresScope.Schema, "collection_config"}.Sanitize()+" WHERE collection_name='stale_sidecar'")
		Expect(err).NotTo(HaveOccurred())
		client := embeddingClient()
		original := client.Transport
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { embeddingCalls.Add(1); return original.RoundTrip(r) })
		restarted, err := New(a.appConfig, a.bus, WithHTTPClient(client))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(restarted.Close)
		opened, err := restarted.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		restartedState := opened.(*sharedCollections).state
		// Constructor completion is the synchronous side-effect boundary: neither
		// rejected name may enter the source manager or the engine cache.
		Expect(restartedState.Collections).NotTo(HaveKey("restart_denied"))
		Expect(restartedState.Collections).NotTo(HaveKey("stale_sidecar"))
		Consistently(func() int32 { return sourceCalls.Load() }, 200*time.Millisecond, 10*time.Millisecond).Should(BeZero())
		Expect(embeddingCalls.Load()).To(BeZero())
		_, err = os.Stat(filepath.Join(cfg.CollectionDBPath, "embedding-models"))
		Expect(os.IsNotExist(err)).To(BeTrue())
		var count int
		Expect(conn.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{cfg.PostgresScope.Schema, "collection_config"}.Sanitize()+" WHERE collection_name='stale_sidecar'").Scan(&count)).To(Succeed())
		Expect(count).To(BeZero())
		Expect(errors.Is(restarted.CheckForUser(ctx, userA, "restart_denied"), ErrLegacyCollectionActionRequired)).To(BeTrue())
		Expect(restarted.CheckForUser(ctx, userA, "stale_sidecar")).To(MatchError(ContainSubstring("collection not found")))
	})
	It("discovers warmed principal collections through PostgreSQL without a bus", func() {
		a, b, _ := principalPostgresServices(true)
		ctx := context.Background()
		warm, err := b.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.CreateForUser(userA, "busless")).To(Succeed())
		names, err := b.ListForUser(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"busless"}))
		Expect(b.CheckForUser(ctx, userA, "busless")).To(Succeed())
		same, err := b.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(BeIdenticalTo(warm))
		hash, err := lagi.MemoryPayloadHash("busless memory", nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = b.WriteMemory(ctx, userA, "busless", lagi.MemoryWrite{Key: "key", Content: "busless memory", PayloadHash: hash}, func(context.Context) error { return nil })
		Expect(err).NotTo(HaveOccurred())
		hits, err := a.SearchForUser(ctx, userA, "busless", "memory", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Content).To(Equal("busless memory"))
	})

	It("discovers a collection after another frontend warms the principal backend", func() {
		a, b, _ := principalPostgresServices()
		ctx := context.Background()
		warm, err := b.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.CreateForUser(userA, "later_notes")).To(Succeed())
		names, err := b.ListForUser(userA)
		Expect(err).NotTo(HaveOccurred())
		_, accessErr := b.ListEntriesForUser(userA, "later_notes")
		_, searchErr := b.SearchForUser(ctx, userA, "later_notes", "private", 10)
		hash, err := lagi.MemoryPayloadHash("later private memory", nil)
		Expect(err).NotTo(HaveOccurred())
		receipt, captureErr := b.WriteMemory(ctx, userA, "later_notes", lagi.MemoryWrite{Key: "later", Content: "later private memory", PayloadHash: hash}, func(context.Context) error { return nil })
		stillWarm, err := b.Backend(userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(stillWarm).To(BeIdenticalTo(warm))
		fmt.Fprintf(GinkgoWriter, "warmed names=%q accessErr=%v searchErr=%v capture=%+v captureErr=%v\n", names, accessErr, searchErr, receipt, captureErr)
		Expect(names).To(ContainElement("later_notes"))
		Expect(accessErr).NotTo(HaveOccurred())
		Expect(searchErr).NotTo(HaveOccurred())
		Expect(captureErr).NotTo(HaveOccurred())
	})
})
