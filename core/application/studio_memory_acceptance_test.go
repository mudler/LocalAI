//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This bounded acceptance slice uses actual saved-agent tools, chat acceptance,
// the durable worker and application adapter. It is not a Slack/trigger transport
// test. Only the model and embedding HTTP response are deterministic fixtures.
var _ = Describe("shared collection saved-agent acceptance", func() {
	DescribeTable("lets a read-only peer search a curator capture without mutation or principal escalation", func(engine string) {
		ctx := context.Background()
		embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(embeddings.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = engine
		if engine == "postgres" {
			cfg.Collections.DatabaseURL = os.Getenv("MEMORY_DATABASE_URL")
			if cfg.Collections.DatabaseURL == "" {
				Skip("MEMORY_DATABASE_URL is not set; real PostgreSQL acceptance requires it")
			}
		}
		cfg.Collections.APIURL = embeddings.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		name := fmt.Sprintf("peer_memory_%d", time.Now().UnixNano())
		const principal = "a9d95bda-b870-4a0b-9f6d-d186d080b123"
		const text = "Curator reference: the launch window is Tuesday"
		Expect(cs.CreateForUser(principal, name)).To(Succeed())
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{}
		a.collectionsService.Store(cs)
		adapter := studioCollections{a}
		var entryID string
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, message string) []studiotest.ToolStep {
			if message == text {
				return []studiotest.ToolStep{{Say: "Recorded."}}
			}
			return []studiotest.ToolStep{
				{Tool: "search_collection", Args: fmt.Sprintf(`{"collection":%q,"query":"launch window"}`, name)},
				{Tool: "add_memory", Args: `{"content":"unauthorized peer write"}`},
				{Tool: "delete_memory", Args: fmt.Sprintf(`{"collection":%q,"entry_id":%q}`, name, entryID)},
				{Say: "Done."},
			}
		}}
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter, Harness: h})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		curator := agents.Agent{ID: "curator", Principal: principal, Name: "Curator", Model: "m",
			CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}},
			Memory:                &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, curator)).To(Succeed())
		writer, err := svc.Create(ctx, principal, studio.CreateInput{AgentID: curator.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Send(ctx, principal, writer.ID, text)
		Expect(err).NotTo(HaveOccurred())
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled))) })
		Eventually(func(g Gomega) {
			statuses, e := svc.MemoryStatuses(ctx, principal, writer.ID)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(statuses).To(HaveLen(1))
			g.Expect(statuses[0].Status).To(Equal("saved"))
			entryID = statuses[0].EntryID
			g.Expect(entryID).NotTo(BeEmpty())
		}, 10*time.Second).Should(Succeed())
		Expect(h.ToolNames(writer.ID)).NotTo(ContainElement("add_memory"), "automatic capture does not enable explicit writes")

		reader := agents.Agent{ID: "reader", Principal: principal, Name: "Read-only peer", Model: "m",
			CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, Search: true}}},
			Memory:                &agents.MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}}
		Expect(as.Put(ctx, reader)).To(Succeed())
		peer, err := svc.Create(ctx, principal, studio.CreateInput{AgentID: reader.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Send(ctx, principal, peer.ID, "find the launch window")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { return len(h.Results()) }, 5*time.Second).Should(Equal(3))
		results := h.Results()
		Expect(results[0].Err).NotTo(HaveOccurred())
		Expect(results[0].Out).To(ContainSubstring(text))
		Expect(results[0].Out).To(ContainSubstring(entryID))
		Expect(results[1].Err).To(MatchError(ContainSubstring("no tool named")))
		Expect(results[2].Err).To(MatchError(ContainSubstring("no tool named")))
		Expect(h.ToolNames(peer.ID)).NotTo(ContainElement("add_memory"))
		Expect(h.ToolNames(peer.ID)).NotTo(ContainElement("delete_memory"))
		Eventually(func(g Gomega) {
			turn, e := svc.Turn(ctx, principal, peer.ID)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(turn.Running).To(BeFalse())
			g.Expect(turn.Last).NotTo(BeEmpty())
		}, 5*time.Second).Should(Succeed())
		statuses, err := svc.MemoryStatuses(ctx, principal, peer.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses).To(BeEmpty(), "ordinary read-only peer messages must not create captures")
		hits, err := adapter.Search(ctx, principal, name, "launch window", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1), "denied add/delete must leave the exact curator document intact")
		Expect(hits[0].EntryID).To(Equal(entryID))
		// A second principal can save the same association, but cannot use it to
		// escape collection ownership through the actual search tool.
		stranger := reader
		stranger.ID = "stranger"
		stranger.Principal = "b7d95bda-b870-4a0b-9f6d-d186d080b456"
		Expect(as.Put(ctx, stranger)).To(Succeed())
		foreign, err := svc.Create(ctx, stranger.Principal, studio.CreateInput{AgentID: stranger.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.Send(ctx, stranger.Principal, foreign.ID, "search another principal's collection")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { return len(h.Results()) }, 5*time.Second).Should(Equal(6))
		Expect(h.Results()[3].Err).To(HaveOccurred(), "an agent association cannot grant another principal's collection")
		// Reusing an already-open chat must reload a revoked search grant.
		reader.CollectionPermissions.Grants[0].Search = false
		Expect(as.Put(ctx, reader)).To(Succeed())
		_, err = svc.Send(ctx, principal, peer.ID, "search after revocation")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int { return len(h.Results()) }, 5*time.Second).Should(Equal(9))
		Expect(h.Results()[6].Err).To(HaveOccurred())
	}, Entry("Chromem", "chromem"), Entry("PostgreSQL", "postgres"))
})
