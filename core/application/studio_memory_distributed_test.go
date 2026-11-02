//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Only request/reply transport and model/embedding responses are fixtures. The
// BroadcastBus, chat-start server/client, SQL claims and application adapters are
// real. This does not claim distributed NATS or process-crash coverage.
type memoryAcceptanceBus struct{ *testutil.FakeBus }

func (b *memoryAcceptanceBus) Request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	reply, ok := b.DeliverReply(subject, data)
	if !ok {
		return nil, nats.ErrNoResponders
	}
	return reply, nil
}

var _ = Describe("PostgreSQL two-frontend memory acceptance", func() {
	It("runs a remote chat worker, races independent SQL capture owners and reloads saved status after lost fanout and restart", func() {
		raw, memoryURL := os.Getenv("LOCALAI_TEST_POSTGRES_URL"), os.Getenv("MEMORY_DATABASE_URL")
		if raw == "" && memoryURL == "" {
			Skip("set both PostgreSQL fixture URLs; make test-memory-distributed-postgres requires them")
		}
		Expect(raw).NotTo(BeEmpty(), "LOCALAI_TEST_POSTGRES_URL is mandatory when either fixture URL is set")
		Expect(memoryURL).NotTo(BeEmpty(), "MEMORY_DATABASE_URL is mandatory when either fixture URL is set")
		ctx := context.Background()
		admin, err := gorm.Open(postgres.Open(raw), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		schema := fmt.Sprintf("memory_distributed_%d", time.Now().UnixNano())
		Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
		DeferCleanup(func() {
			Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
			db, e := admin.DB()
			Expect(e).NotTo(HaveOccurred())
			Expect(db.Close()).To(Succeed())
		})
		u, err := url.Parse(raw)
		Expect(err).NotTo(HaveOccurred())
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(embeddings.Close)
		const principal = "a9d95bda-b870-4a0b-9f6d-d186d080b123"
		const text = "Distributed curator: the launch window is Tuesday"
		name := fmt.Sprintf("distributed_memory_%d", time.Now().UnixNano())
		net := &memoryAcceptanceBus{testutil.NewFakeBus()}
		// Principal collection discovery currently uses file metadata, not the
		// root-only PostgreSQL registry. Model the supported shared state mount.
		collectionState := GinkgoT().TempDir()
		type replica struct {
			svc     *studio.Service
			chats   store.Store
			agents  agents.Store
			adapter studioCollections
			h       *studiotest.ToolHarness
			close   func()
		}
		open := func(owner string, remote bool) replica {
			// Each replica opens its own pool, store, agent store, collection service
			// and application adapter. PostgreSQL, transport and principal collection
			// discovery metadata on the shared state mount are shared.
			db, e := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
			Expect(e).NotTo(HaveOccurred())
			sqlDB, e := db.DB()
			Expect(e).NotTo(HaveOccurred())
			st, e := store.NewGORM(db)
			Expect(e).NotTo(HaveOccurred())
			as, e := agents.NewGORM(db)
			Expect(e).NotTo(HaveOccurred())
			cfg := config.NewApplicationConfig()
			cfg.DataPath = GinkgoT().TempDir()
			cfg.Collections.StateDir = collectionState
			cfg.Collections.VectorEngine, cfg.Collections.DatabaseURL = "postgres", memoryURL
			cfg.Collections.APIURL, cfg.Collections.EmbeddingModel = embeddings.URL+"/v1", "test"
			cs, e := collections.New(cfg, net)
			Expect(e).NotTo(HaveOccurred())
			a := &Application{}
			a.collectionsService.Store(cs)
			adapter := studioCollections{a}
			h := &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Say: "Recorded on chat worker."}}
			}}
			d := studio.Deps{StateDir: GinkgoT().TempDir(), Store: st, Agents: as, Provider: provider.New(), Collections: adapter, Harness: h, Bus: studio.NewBroadcastBus(net, nil), CaptureWorker: studio.CaptureWorkerOptions{Owner: owner}}
			if remote {
				d.Workers = nodes.NewNATSChatStarter(net)
			}
			svc, e := studio.New(d)
			Expect(e).NotTo(HaveOccurred())
			a.studioService = svc
			var closed atomic.Bool
			closeReplica := func() {
				if closed.CompareAndSwap(false, true) {
					Expect(svc.Close(ctx)).To(Succeed())
					cs.Close()
					Expect(sqlDB.Close()).To(Succeed())
				}
			}
			DeferCleanup(closeReplica)
			return replica{svc, st, as, adapter, h, closeReplica}
		}
		first, second, worker := open("frontend-one", true), open("frontend-two", true), open("chat-worker", false)
		Expect(first.chats).NotTo(BeIdenticalTo(second.chats))
		Expect(first.agents).NotTo(BeIdenticalTo(second.agents))
		Expect(first.adapter.a.CollectionsService().CreateForUser(principal, name)).To(Succeed())
		// Each principal backend first opens after collection metadata exists. This
		// does not prove live discovery by an already-open principal backend.
		names, err := second.adapter.List(ctx, principal)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(ContainElement(name))
		ag := agents.Agent{ID: "distributed-curator", Principal: principal, Name: "Curator", Model: "m", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}}, Memory: &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(first.agents.Put(ctx, ag)).To(Succeed())
		sub, err := nodes.NewNATSChatStartServer(net).Serve(worker.svc.ServeStart)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sub.Unsubscribe()).To(Succeed()) })
		chat, err := first.svc.Create(ctx, principal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = first.svc.SendBackground(ctx, principal, chat.ID, text, nil)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			turn, e := second.svc.Turn(ctx, principal, chat.ID)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(turn.Running).To(BeFalse())
			g.Expect(turn.Last).NotTo(BeEmpty())
		}, 10*time.Second).Should(Succeed())
		Expect(worker.h.Sent(chat.ID)).To(Equal([]string{text}))
		Expect(first.h.Sent(chat.ID)).To(BeEmpty())
		Expect(second.h.Sent(chat.ID)).To(BeEmpty())
		records, err := second.chats.ListCaptures(ctx, principal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].RawText).To(ContainSubstring(text))

		// Synchronous subscriber re-enters through its own SQL handle AND the
		// publishing service. Context deadlines bound a regression that publishes
		// inside a SQL transaction/row lock; the callback is not a queued reader.
		var observed atomic.Int64
		var processing atomic.Int64
		listener, err := net.Subscribe("chats.events.*", func(raw []byte) {
			defer GinkgoRecover()
			var ev session.Event
			Expect(json.Unmarshal(raw, &ev)).To(Succeed())
			if ev.Kind != session.KindMemoryCapture {
				return
			}
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			statuses, e := second.svc.MemoryStatuses(c, principal, chat.ID)
			Expect(e).NotTo(HaveOccurred())
			Expect(statuses).To(HaveLen(1))
			if statuses[0].Status == "processing" {
				processing.Add(1)
			}
			_, e = second.chats.ClaimCapture(c, "subscriber-reentry", time.Now(), time.Minute)
			Expect(e).To(MatchError(store.ErrNoCapture))
			_, e = first.svc.MemoryStatuses(c, principal, chat.ID)
			Expect(e).NotTo(HaveOccurred())
			observed.Add(1)
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = listener.Unsubscribe() })
		// No UI subscriber receives any capture notification. Its later read must
		// recover from SQL, not a cached BroadcastBus event.
		events, stopEvents, err := second.svc.Events(ctx, principal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		stopEvents()
		Eventually(events).Should(BeClosed())
		gate := make(chan struct{})
		start := func(svc *studio.Service) func() {
			c, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { defer GinkgoRecover(); <-gate; done <- svc.ProcessMemoryCaptures(c) }()
			var stopped atomic.Bool
			stop := func() {
				if stopped.CompareAndSwap(false, true) {
					cancel()
					Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
				}
			}
			DeferCleanup(stop)
			return stop
		}
		stopFirst, stopSecond := start(first.svc), start(second.svc)
		close(gate)
		var entryID string
		Eventually(func(g Gomega) {
			statuses, e := second.svc.MemoryStatuses(ctx, principal, chat.ID)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(statuses).To(HaveLen(1))
			g.Expect(statuses[0].Status).To(Equal("saved"))
			entryID = statuses[0].EntryID
			g.Expect(entryID).NotTo(BeEmpty())
		}, 15*time.Second).Should(Succeed())
		stopFirst()
		stopSecond()
		Expect(observed.Load()).To(BeNumerically(">", 0))
		Expect(processing.Load()).To(BeNumerically(">", 0))
		records, err = second.chats.ListCaptures(ctx, principal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].State).To(Equal("Saved"))
		Expect(records[0].Generation).To(Equal(1), "one SQL claim wins the concurrent owners")
		Expect(records[0].Owner).To(BeEmpty(), "terminal capture releases the queue owner")
		Expect(records[0].BackendFence).NotTo(BeNil())
		Expect(records[0].BackendFence.Owner).To(BeElementOf("frontend-one", "frontend-two"))
		Expect(records[0].BackendFence.Generation).To(Equal(records[0].Generation))
		hits, err := second.adapter.Search(ctx, principal, name, "launch window", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].EntryID).To(Equal(entryID))
		Expect(hits[0].Content).To(ContainSubstring(text))
		Expect(listener.Unsubscribe()).To(Succeed())
		first.close()
		second.close()
		worker.close()
		reopened := open("new-owner", false)
		statuses, err := reopened.svc.MemoryStatuses(ctx, principal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].Status).To(Equal("saved"))
		Expect(statuses[0].EntryID).To(Equal(entryID))
		_, err = reopened.chats.ClaimCapture(ctx, "new-owner", time.Now().Add(2*time.Minute), time.Minute)
		Expect(err).To(MatchError(store.ErrNoCapture))
		hits, err = reopened.adapter.Search(ctx, principal, name, "launch window", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].EntryID).To(Equal(entryID))
	})
})
