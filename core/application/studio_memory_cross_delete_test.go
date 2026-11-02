//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/memoryaudit"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cross-producer memory deletion application acceptance", func() {
	It("lets a delete-only peer tombstone a capture before lost-response recovery without recreation", func() {
		ctx := context.Background()
		embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(embeddings.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.APIURL = embeddings.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		const principal = "a9d95bda-b870-4a0b-9f6d-d186d080b123"
		const name = "cross_producer_memory"
		const text = "Automatically captured exact document"
		Expect(cs.CreateForUser(principal, name)).To(Succeed())
		queuePath := GinkgoT().TempDir()
		chats, err := store.NewJSON(queuePath)
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		audit, err := memoryaudit.NewJSONAudit("")
		Expect(err).NotTo(HaveOccurred())
		app := &Application{studioMemoryAudit: audit}
		app.collectionsService.Store(cs)
		adapter := studioCollections{app}
		workerCtx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		lost := &lostCaptureResponse{studioCollections: adapter, cancel: cancel}
		var entryID string
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, message string) []studiotest.ToolStep {
			if message == text {
				return []studiotest.ToolStep{{Say: "Captured."}}
			}
			return []studiotest.ToolStep{{Tool: "delete_memory", Args: fmt.Sprintf(`{"collection":%q,"entry_id":%q}`, name, entryID)}, {Say: "Deleted."}}
		}}
		var clock atomic.Int64
		clock.Store(time.Now().Unix())
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: lost, Harness: h, CaptureWorker: studio.CaptureWorkerOptions{Owner: "original-capture-worker", Now: func() time.Time { return time.Unix(clock.Load(), 0) }}})
		Expect(err).NotTo(HaveOccurred())
		app.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		producer := agents.Agent{ID: "producer", Principal: principal, Name: "Producer", Model: "m", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}}, Memory: &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, producer)).To(Succeed())
		writer, err := svc.Create(ctx, principal, studio.CreateInput{AgentID: producer.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Send(ctx, principal, writer.ID, text)
		Expect(err).NotTo(HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
		before, err := chats.ListCaptures(ctx, principal, writer.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(before).To(HaveLen(1))
		Expect(before[0].State).To(Equal("Writing"))
		Expect(before[0].InputType).To(Equal("FrozenPayload"))
		entryID = lost.committed.EntryID
		Expect(entryID).NotTo(BeEmpty())
		Expect(lost.committed.State).To(Equal(lagi.MemorySaved))
		hits, err := cs.SearchForUser(ctx, principal, name, "captured", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].EntryID).To(Equal(entryID))
		Expect(hits[0].Content).To(Equal(text))
		deleter := agents.Agent{ID: "delete-only-peer", Principal: principal, Name: "Delete-only peer", Model: "m", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, DeleteEntries: true}}}, Memory: &agents.MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}}
		Expect(as.Put(ctx, deleter)).To(Succeed())
		peer, err := svc.Create(ctx, principal, studio.CreateInput{AgentID: deleter.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Send(ctx, principal, peer.ID, "delete the producer document")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { return len(h.Results()) }, 5*time.Second).Should(Equal(1))
		result := h.Results()[0]
		Expect(result.Err).NotTo(HaveOccurred())
		var deletion studio.MemoryResult
		Expect(json.Unmarshal([]byte(result.Out), &deletion)).To(Succeed())
		Expect(deletion.EntryID).To(Equal(entryID))
		Expect(deletion.Destination).To(Equal(name))
		Expect(h.ToolNames(peer.ID)).To(ContainElement("delete_memory"))
		Expect(h.ToolNames(peer.ID)).NotTo(ContainElement("search_collection"))
		Expect(h.ToolNames(peer.ID)).NotTo(ContainElement("add_memory"))
		tombstone, err := cs.ReconcileMemory(ctx, principal, name, before[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(tombstone.State).To(Equal(lagi.MemoryDeleted))
		Expect(tombstone.EntryID).To(Equal(entryID))
		Expect(cs.EntryExistsForUser(principal, name, entryID)).To(BeFalse())
		// Reopen the actual queue while the original claim remains Writing. A new
		// owner must reconcile the exact tombstone instead of writing a replacement.
		clock.Store(before[0].Deadline.Add(time.Second).Unix())
		reopened, err := store.NewJSON(queuePath)
		Expect(err).NotTo(HaveOccurred())
		terminal := make(chan store.CaptureRecord, 1)
		restarted, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: reopened, Agents: as, Provider: provider.New(), Collections: adapter, CaptureWorker: studio.CaptureWorkerOptions{Owner: "recovery-worker", Now: func() time.Time { return time.Unix(clock.Load(), 0) }, Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
			if status.Status == "Skipped" {
				terminal <- r
			}
		}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(restarted.Close(ctx)).To(Succeed()) })
		app.studioService = restarted
		recoveryCtx, stop := context.WithCancel(ctx)
		recoveryDone := make(chan error, 1)
		go func() { recoveryDone <- restarted.ProcessMemoryCaptures(recoveryCtx) }()
		DeferCleanup(func() { stop(); Eventually(recoveryDone, 3*time.Second).Should(Receive(MatchError(context.Canceled))) })
		var skipped store.CaptureRecord
		Eventually(terminal, 5*time.Second).Should(Receive(&skipped))
		Expect(skipped.ID).To(Equal(before[0].ID))
		Expect(skipped.Identity).To(Equal(before[0].Identity))
		Expect(skipped.State).To(Equal("Skipped"))
		Expect(skipped.RuleID).To(Equal("R07-deleted"))
		Expect(skipped.Generation).To(BeNumerically(">", before[0].Generation))
		Expect(skipped.BackendFence.Owner).To(Equal("recovery-worker"))
		Expect(lost.writes.Load()).To(Equal(int32(1)))
		final, err := cs.ReconcileMemory(ctx, principal, name, before[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(lagi.MemoryDeleted))
		Expect(final.EntryID).To(Equal(entryID))
		entries, err := cs.ListEntriesForUser(principal, name)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
		hits, err = cs.SearchForUser(ctx, principal, name, "captured", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(BeEmpty())
	})
})
