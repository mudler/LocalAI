// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The backend returns a successful but deferred completion. Advancing only the
// injected clock exercises the real 60s worker deadline without a minute's sleep.
type timeoutBackend struct {
	Collections
	complete func()
	calls    []string
}

func (*timeoutBackend) Allowed(context.Context, string) bool           { return true }
func (*timeoutBackend) List(context.Context, string) ([]string, error) { return []string{"docs"}, nil }
func (b *timeoutBackend) BeginCapture(ctx context.Context, i memory.Identity, key, owner string, auth func(context.Context) error) (lagi.MemoryFence, error) {
	if err := auth(ctx); err != nil {
		return lagi.MemoryFence{}, err
	}
	b.calls = append(b.calls, "reserve")
	return lagi.MemoryFence{Key: key, Owner: owner, Generation: 1}, nil
}
func (b *timeoutBackend) ReconcileCapture(context.Context, memory.Identity, string) (lagi.MemoryReceipt, error) {
	b.calls = append(b.calls, "reconcile")
	return lagi.MemoryReceipt{}, nil
}
func (b *timeoutBackend) WriteCapture(ctx context.Context, _ memory.Identity, _ lagi.MemoryFence, _ lagi.MemoryWrite, auth func(context.Context) error) (lagi.MemoryReceipt, error) {
	if err := auth(ctx); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	b.calls = append(b.calls, "write")
	b.complete()
	return lagi.MemoryReceipt{State: lagi.MemorySaved, EntryID: "late-success"}, nil
}

var _ = Describe("durable worker timeout", func() {
	for _, kind := range []string{"JSON", "SQLite", "PostgreSQL"} {
		kind := kind
		It("persists real expired completions with bounded backoff on "+kind, func() {
			ctx := context.Background()
			var st store.Store
			if kind == "JSON" {
				var err error
				st, err = store.NewJSON(filepath.Join(GinkgoT().TempDir(), "json"))
				Expect(err).NotTo(HaveOccurred())
			} else {
				var db *gorm.DB
				var err error
				if kind == "SQLite" {
					db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "queue.db")), &gorm.Config{})
				} else {
					raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
					if raw == "" {
						Skip("LOCALAI_TEST_POSTGRES_URL is not set")
					}
					db, err = gorm.Open(postgres.Open(raw), &gorm.Config{})
					Expect(err).NotTo(HaveOccurred())
					schema := fmt.Sprintf("worker_timeout_%d", time.Now().UnixNano())
					Expect(db.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
					sqlDB, e := db.DB()
					Expect(e).NotTo(HaveOccurred())
					sqlDB.SetMaxOpenConns(1)
					Expect(db.Exec("SET search_path TO " + schema).Error).To(Succeed())
					defer func() { Expect(db.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed()) }()
				}
				Expect(err).NotTo(HaveOccurred())
				sqlDB, e := db.DB()
				Expect(e).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = sqlDB.Close() })
				st, err = store.NewGORM(db)
				Expect(err).NotTo(HaveOccurred())
			}
			as, err := agents.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "docs", ToolStorage: "content", AutomaticIngestion: "incoming_only"}})).To(Succeed())
			Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent"})).To(Succeed())
			_, err = st.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "source", Text: "remember", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: store.CaptureIncoming}})
			Expect(err).NotTo(HaveOccurred())
			clock := time.Now().Truncate(time.Second)
			b := &timeoutBackend{complete: func() { clock = clock.Add(63 * time.Second) }}
			svc := &Service{d: Deps{Store: st, Agents: as, Collections: b}}
			for attempt := 1; attempt <= 3; attempt++ {
				claim, e := st.ClaimCapture(ctx, "worker", clock, 60*time.Second)
				Expect(e).NotTo(HaveOccurred())
				var statuses []string
				publish := func(r store.CaptureRecord, status memory.PublishStatus) {
					records, e := st.ListCaptures(ctx, "alice", "chat")
					Expect(e).NotTo(HaveOccurred())
					Expect(records[0].Revision).To(Equal(r.Revision))
					if status.Status == "RetryWaiting" || status.Status == "Failed" {
						Expect(records[0].State).To(Equal(status.Status))
					}
					statuses = append(statuses, status.Status)
				}
				Expect(svc.runCapture(ctx, claim, b, st.(store.CaptureMemoryStore), func() time.Time { return clock }, publish)).To(Succeed())
				records, e := st.ListCaptures(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				r := records[0]
				state, e := r.ReducerState()
				Expect(e).NotTo(HaveOccurred())
				if attempt < 3 {
					Expect(r.RuleID).To(Equal("R06-wait"))
					waiting := state.(memory.RetryWaiting)
					delay := []int64{1, 5}[attempt-1]
					Expect(waiting.Due).To(Equal(clock.Unix() + delay))
					Expect(statuses[len(statuses)-1]).To(Equal("RetryWaiting"))
					_, e = st.ClaimCapture(ctx, "other", clock, 60*time.Second)
					Expect(e).To(MatchError(store.ErrNoCapture))
					clock = time.Unix(waiting.Due, 0)
				} else {
					Expect(r.RuleID).To(Equal("R06-exhausted"))
					Expect(r.State).To(Equal("Failed"))
					Expect(statuses[len(statuses)-1]).To(Equal("Failed"))
				}
			}
			Expect(b.calls).To(Equal([]string{"reserve", "reconcile", "write", "reserve", "reconcile", "write", "reserve", "reconcile", "write"}))
		})
	}
})
