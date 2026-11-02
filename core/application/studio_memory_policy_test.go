//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
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
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("queued memory live-policy application acceptance", func() {
	DescribeTable("skips frozen routing after a saved-agent edit without rerouting", func(change string) {
		ctx := context.Background()
		var embeddings atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			embeddings.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(server.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.APIURL = server.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		const destinationA = "original_memory"
		const destinationB = "edited_memory"
		for _, name := range []string{destinationA, destinationB} {
			Expect(cs.CreateForUser("", name)).To(Succeed())
		}
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		app := &Application{}
		app.collectionsService.Store(cs)
		adapter := studioCollections{app}
		terminal := make(chan store.CaptureRecord, 1)
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter, CaptureWorker: studio.CaptureWorkerOptions{Owner: "policy-worker", Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
			if status.Status == "Skipped" {
				terminal <- r
			}
		}}})
		Expect(err).NotTo(HaveOccurred())
		app.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		ag := agents.Agent{ID: "policy-writer", Principal: studio.AnonymousPrincipal, Name: "Writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: destinationA}, {Collection: destinationB}}}, Memory: &agents.MemorySettings{Version: 1, Destination: destinationA, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = chats.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: studio.AnonymousPrincipal, ChatID: chat.ID, SourceID: "accepted-before-edit", Text: "Do not move this queued memory", Policy: store.CapturePolicy{AgentID: ag.ID, Destination: destinationA, Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		before, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(before).To(HaveLen(1))
		Expect(before[0].State).To(Equal("Queued"))
		Expect(before[0].Identity.Destination).To(Equal(destinationA))
		switch change {
		case "destination":
			ag.Memory.Destination = destinationB
		case "disabled":
			ag.Memory.AutomaticIngestion = "off"
		}
		Expect(as.Put(ctx, ag)).To(Succeed())
		// No worker has run before the persisted policy change.
		embeddingBaseline := embeddings.Load()
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 3*time.Second).Should(Receive(MatchError(context.Canceled))) })
		var skipped store.CaptureRecord
		Eventually(terminal, 5*time.Second).Should(Receive(&skipped))
		after, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveLen(1))
		Expect(after[0].ID).To(Equal(before[0].ID))
		Expect(after[0].Identity).To(Equal(before[0].Identity))
		Expect(after[0].State).To(Equal("Skipped"))
		Expect(after[0].RuleID).To(Equal("R03-denied"))
		Expect(after[0].BackendFence).To(BeNil())
		Expect(skipped.ID).To(Equal(before[0].ID))
		Expect(embeddings.Load()).To(Equal(embeddingBaseline))
		// Inspect both real stores directly: adapter reconciliation is correctly
		// unauthorized now, and cannot serve as evidence that storage stayed empty.
		for _, name := range []string{destinationA, destinationB} {
			entries, e := cs.ListEntriesForUser("", name)
			Expect(e).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
			receipt, e := cs.ReconcileMemory(ctx, "", name, before[0].ID)
			Expect(e).NotTo(HaveOccurred())
			Expect(receipt.State).To(Equal(lagi.MemoryAbsent))
			Expect(receipt.EntryID).To(BeEmpty())
			hits, e := cs.SearchForUser(ctx, "", name, "queued memory", 10)
			Expect(e).NotTo(HaveOccurred())
			Expect(hits).To(BeEmpty())
		}
	}, Entry("destination A changed to B", "destination"), Entry("automatic ingestion disabled", "disabled"))
})
