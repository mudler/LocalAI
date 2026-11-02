//go:build auth

package application

import (
	"context"
	"errors"
	"fmt"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memoryaudit"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lostCaptureResponse drops only the response, after the real backend has committed.
// It never retains the protected callback context or authorization callback.
type lostCaptureResponse struct {
	studioCollections
	cancel    context.CancelFunc
	writes    atomic.Int32
	committed lagi.MemoryReceipt
}

func (b *lostCaptureResponse) WriteCapture(ctx context.Context, i memory.Identity, f lagi.MemoryFence, w lagi.MemoryWrite, check func(context.Context) error) (lagi.MemoryReceipt, error) {
	b.writes.Add(1)
	r, err := b.studioCollections.WriteCapture(ctx, i, f, w, check)
	if err == nil {
		b.committed = r
		b.cancel()
		return lagi.MemoryReceipt{}, context.Canceled
	}
	return r, err
}

var _ = Describe("principal memory application adapter", func() {
	DescribeTable("preserves captures while collections start and saves after readiness", func(expiredWriting bool) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(server.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.Enabled = true
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.APIURL = server.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		Expect(cs.CreateForUser("", "startup_memory")).To(Succeed())
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{applicationConfig: cfg}
		adapter := studioCollections{a}
		clock := time.Now().Truncate(time.Second)
		saved := make(chan store.CaptureRecord, 1)
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter,
			CaptureWorker: studio.CaptureWorkerOptions{Owner: "startup-worker", Now: func() time.Time { return clock }, Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
				if status.Status == "Saved" {
					saved <- r
				}
			}}})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		ag := agents.Agent{ID: "writer", Principal: studio.AnonymousPrincipal, Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "startup_memory"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "startup_memory", ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = chats.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: studio.AnonymousPrincipal, ChatID: chat.ID, SourceID: "startup-input", Text: "remember after startup", Policy: store.CapturePolicy{AgentID: ag.ID, Destination: "startup_memory", Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		if expiredWriting {
			claim, e := chats.ClaimCapture(ctx, "previous-worker", clock, time.Minute)
			Expect(e).NotTo(HaveOccurred())
			for _, event := range []memory.Event{memory.AuthorizationGranted{}, memory.PayloadPersisted{}, memory.AuthorizationGranted{}} {
				state, e := claim.Record.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				var env memory.Envelope
				switch v := state.(type) {
				case memory.Authorizing:
					env = v.Work.Fence
				case memory.PersistingPayload:
					env = v.Work.Fence
				}
				env.Now = clock.Unix()
				switch event.(type) {
				case memory.AuthorizationGranted:
					event = memory.AuthorizationGranted{Envelope: env}
				case memory.PayloadPersisted:
					event = memory.PayloadPersisted{Envelope: env}
				}
				transition := memory.Reduce(state, event)
				Expect(transition.IgnoredReason).To(BeEmpty())
				Expect(chats.ApplyCapture(ctx, claim, transition)).To(Succeed())
				records, e := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
				Expect(e).NotTo(HaveOccurred())
				claim.Record = records[0]
			}
			records, e := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
			Expect(e).NotTo(HaveOccurred())
			Expect(records[0].State).To(Equal("Writing"))
			clock = records[0].Deadline.Add(time.Second)
		}
		before, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		DeferCleanup(func() { cancel(); Eventually(done).Should(Receive(MatchError(context.Canceled))) })
		// This is the real startup window, even before AwaitCollections is called.
		Consistently(func() []store.CaptureRecord {
			r, e := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
			Expect(e).NotTo(HaveOccurred())
			return r
		}, 150*time.Millisecond, 10*time.Millisecond).Should(Equal(before))
		a.collectionsService.Store(cs)
		Eventually(saved, 4*time.Second).Should(Receive())
		records, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records[0].State).To(Equal("Saved"))
		Expect(records[0].Generation).To(BeNumerically(">", before[0].Generation))
	}, Entry("queued", false), Entry("expired Writing", true))

	DescribeTable("captures text through the real worker and fenced application adapter", func(engine string, crash bool) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(server.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = engine
		if engine == "postgres" {
			cfg.Collections.DatabaseURL = os.Getenv("MEMORY_DATABASE_URL")
			if cfg.Collections.DatabaseURL == "" {
				Skip("MEMORY_DATABASE_URL is not set")
			}
		}
		cfg.Collections.APIURL = server.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		name := fmt.Sprintf("worker_mem_%d", time.Now().UnixNano())
		Expect(cs.CreateForUser("", name)).To(Succeed())
		queuePath := GinkgoT().TempDir()
		chats, err := store.NewJSON(queuePath)
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{}
		a.collectionsService.Store(cs)
		adapter := studioCollections{a}
		saved := make(chan store.CaptureRecord, 1)
		workerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var clock atomic.Int64
		clock.Store(time.Now().Unix())
		lost := &lostCaptureResponse{studioCollections: adapter, cancel: cancel}
		var backend studio.Collections = adapter
		if crash {
			backend = lost
		}
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: backend,
			CaptureWorker: studio.CaptureWorkerOptions{Owner: "first-worker", Now: func() time.Time { return time.Unix(clock.Load(), 0) }, Publish: func(record store.CaptureRecord, status memory.PublishStatus) {
				if status.Status == "Saved" {
					saved <- record
				}
			}}})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() {
			stop, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			Expect(svc.Close(stop)).To(Succeed())
		})
		ag := agents.Agent{ID: "writer", Principal: studio.AnonymousPrincipal, Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}}, Memory: &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		const text = "Remember this exact incoming text"
		_, err = chats.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: studio.AnonymousPrincipal, ChatID: chat.ID, SourceID: "incoming-1", Text: text, Policy: store.CapturePolicy{AgentID: ag.ID, Destination: name, Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		var record store.CaptureRecord
		if crash {
			Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
			before, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(before).To(HaveLen(1))
			Expect(before[0].State).To(Equal("Writing"))
			Expect(before[0].InputType).To(Equal("FrozenPayload"))
			Expect(lost.committed.EntryID).NotTo(BeEmpty())
			// A new service/store instance recovers the expired durable claim.
			clock.Store(before[0].Deadline.Add(time.Second).Unix())
			chats, err = store.NewJSON(queuePath)
			Expect(err).NotTo(HaveOccurred())
			restarted, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter,
				CaptureWorker: studio.CaptureWorkerOptions{Owner: "restarted-worker", Now: func() time.Time { return time.Unix(clock.Load(), 0) }, Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
					if status.Status == "Saved" {
						saved <- r
					}
				}}})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(restarted.Close(ctx)).To(Succeed()) })
			a.studioService = restarted
			workerCtx, cancel = context.WithCancel(ctx)
			defer cancel()
			go func() { done <- restarted.ProcessMemoryCaptures(workerCtx) }()
			Eventually(saved, 5*time.Second).Should(Receive(&record))
			Expect(record.Generation).To(BeNumerically(">", before[0].Generation))
			Expect(record.Revision).To(BeNumerically(">", before[0].Revision))
			Expect(record.Owner).To(BeEmpty())
			Expect(record.BackendFence.Owner).To(Equal("restarted-worker"))
			Expect(record.RuleID).To(Equal("R05-saved"))
			Expect(lost.writes.Load()).To(Equal(int32(1)))
		} else {
			Eventually(saved, 5*time.Second).Should(Receive(&record))
		}
		cancel()
		Eventually(done, 3*time.Second).Should(Receive(MatchError(context.Canceled)))
		records, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].State).To(Equal("Saved"))
		Expect(records[0].RuleID).To(Equal("R05-saved"))
		Expect(record.ID).To(Equal(records[0].ID))
		Expect(records[0].InputType).To(Equal("FrozenPayload"))
		Expect(records[0].BackendFence).NotTo(BeNil())
		receipt, err := adapter.ReconcileCapture(ctx, records[0].Identity, records[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(lagi.MemorySaved))
		if crash {
			Expect(receipt.EntryID).To(Equal(lost.committed.EntryID))
		}
		state, err := records[0].ReducerState()
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.EntryID).To(Equal(state.(memory.Saved).EntryID))
		hits, err := adapter.Search(ctx, studio.AnonymousPrincipal, name, text, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Content).To(Equal(text))
		Expect(hits[0].EntryID).To(Equal(receipt.EntryID))
		// A current policy revocation must also close the adapter boundary.
		ag.Memory.AutomaticIngestion = "off"
		Expect(as.Put(ctx, ag)).To(Succeed())
		_, err = adapter.ReconcileCapture(ctx, records[0].Identity, records[0].ID)
		Expect(errors.Is(err, studio.ErrMemoryDenied)).To(BeTrue())
	},
		Entry("chromem happy text", "chromem", false),
		Entry("PostgreSQL happy text", "postgres", false),
		Entry("chromem committed response lost and restart recovery", "chromem", true),
		Entry("PostgreSQL committed response lost and restart recovery", "postgres", true),
	)

	It("writes, retries, and deletes exact documents with content-free outcomes on a real PostgreSQL backend", func() {
		ctx := context.Background()
		url := os.Getenv("MEMORY_DATABASE_URL")
		if url == "" {
			Skip("MEMORY_DATABASE_URL is not set")
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(server.Close)
		cfg := &config.ApplicationConfig{}
		cfg.Collections.StateDir = GinkgoT().TempDir()
		cfg.Collections.CollectionDBPath = filepath.Join(cfg.Collections.StateDir, "db")
		cfg.Collections.VectorEngine = "postgres"
		cfg.Collections.DatabaseURL = url
		cfg.Collections.APIURL = server.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cfg.Collections.MaxChunkingSize = 1024
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		name := fmt.Sprintf("studio_mem_%d", time.Now().UnixNano())
		Expect(cs.CreateForUser("", name)).To(Succeed())
		chats, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		audit, err := memoryaudit.NewJSONAudit("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{studioMemoryAudit: audit}
		a.collectionsService.Store(cs)
		adapter := studioCollections{a}
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() { _ = svc.Close(ctx) })
		ag := agents.Agent{ID: "writer", Principal: studio.AnonymousPrincipal, Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, DeleteEntries: true}}}, Memory: &agents.MemorySettings{Version: 1, AddMemory: true, Destination: name, ToolStorage: "content", AutomaticIngestion: "off"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		req := studio.MemoryRequest{MemoryAuthorization: studio.MemoryAuthorization{Principal: studio.AnonymousPrincipal, AgentID: ag.ID, ChatID: chat.ID, Source: "tool", Destination: name, Storage: "content"}, Content: "PRIVATE EXACT CONTENT", Key: "stable-call"}
		check := func(context.Context) error { return nil }
		receipt, err := adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.EntryID).NotTo(BeEmpty())
		retry, err := adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(retry).To(Equal(receipt))
		authz := req.MemoryAuthorization
		authz.Delete = true
		_, err = adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: "*"}, check)
		Expect(err).To(HaveOccurred())
		_, err = adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: "guessed"}, check)
		Expect(err).To(HaveOccurred())
		deleted, err := adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: receipt.EntryID}, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(deleted.EntryID).To(Equal(receipt.EntryID))
		events, err := audit.Events(ctx, 50)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(6))
		Expect(events[0].Outcome).To(Equal("deleted"))
		Expect(events[2].Outcome).To(Equal("failed"))
		for _, e := range events {
			Expect(e.Principal).To(Equal(studio.AnonymousPrincipal))
			Expect(e.Agent).To(Equal(ag.ID))
			Expect(e.Chat).To(Equal(chat.ID))
			Expect(e.Collection).To(Equal(name))
			Expect(fmt.Sprint(e)).NotTo(ContainSubstring(req.Content))
		}
		// Deletion does not reset the collection; another producer can still write.
		req.Key = "another-call"
		_, err = adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		req.Key = "revoked"
		_, err = adapter.WriteMemory(ctx, req, func(context.Context) error {
			ag.Memory.AddMemory = false
			Expect(as.Put(ctx, ag)).To(Succeed())
			return studio.ErrMemoryDenied
		})
		Expect(err).To(HaveOccurred())
	})
})
