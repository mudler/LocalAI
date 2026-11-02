// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Dispatch through the actual worker entry point, not an identity-recording stub.
type deliveryWorker struct{ svc *Service }

func (w deliveryWorker) StartChat(ctx context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error) {
	reply := w.svc.ServeStart(ctx, req)
	return &reply, nil
}

var _ = Describe("generic durable delivery receipts", func() {
	for _, backend := range []string{"JSON", "SQLite", "PostgreSQL"} {
		for _, kind := range []string{"off", "plain"} {
			It("replays "+kind+" through completion, shared stores and reopen on "+backend, func() {
				ctx := context.Background()
				path := filepath.Join(GinkgoT().TempDir(), "chat")
				dsn := path
				var open func() store.Store
				if backend == "JSON" {
					open = func() store.Store { st, e := store.NewJSON(path); Expect(e).NotTo(HaveOccurred()); return st }
				} else {
					if backend == "PostgreSQL" {
						raw := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
						Expect(raw).NotTo(BeEmpty(), "PostgreSQL fixture is required")
						admin, e := gorm.Open(postgres.Open(raw), &gorm.Config{})
						Expect(e).NotTo(HaveOccurred())
						schema := fmt.Sprintf("capture_visibility_%d", time.Now().UnixNano())
						Expect(admin.Exec("CREATE SCHEMA " + schema).Error).To(Succeed())
						DeferCleanup(func() {
							Expect(admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error).To(Succeed())
							db, _ := admin.DB()
							_ = db.Close()
						})
						u, e := url.Parse(raw)
						Expect(e).NotTo(HaveOccurred())
						q := u.Query()
						q.Set("search_path", schema)
						u.RawQuery = q.Encode()
						dsn = u.String()
					}
					open = func() store.Store {
						var db *gorm.DB
						var e error
						if backend == "SQLite" {
							db, e = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
						} else {
							db, e = gorm.Open(postgres.Open(dsn), &gorm.Config{})
						}
						Expect(e).NotTo(HaveOccurred())
						sqlDB, e := db.DB()
						Expect(e).NotTo(HaveOccurred())
						DeferCleanup(func() { _ = sqlDB.Close() })
						st, e := store.NewGORM(db)
						Expect(e).NotTo(HaveOccurred())
						return st
					}
				}
				st := open()
				as, err := agents.NewJSON("")
				Expect(err).NotTo(HaveOccurred())
				Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Off", Memory: &agents.MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}})).To(Succeed())
				agent := "agent"
				if kind == "plain" {
					agent = ""
				}
				Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: agent, Model: "m"})).To(Succeed())
				h := studiotest.NewGateHarness()
				makeService := func(st store.Store) *Service {
					svc, e := New(Deps{Store: st, Agents: as, Provider: provider.New(), Harness: h, StateDir: GinkgoT().TempDir()})
					Expect(e).NotTo(HaveOccurred())
					DeferCleanup(func() { _ = svc.Close(ctx) })
					return svc
				}
				svc := makeService(st)
				shared := st // JSON directories have a single process owner, not independent handles.
				if backend != "JSON" {
					shared = open()
				}
				other := makeService(shared)
				req := workerctl.ChatStartRequest{Principal: "alice", ChatID: "chat", Text: "same words", RequestID: "delivery-1"}
				var wg sync.WaitGroup
				results := make([]workerctl.ChatStartReply, 2)
				for i, worker := range []*Service{svc, other} {
					wg.Add(1)
					go func(i int, worker *Service) {
						defer GinkgoRecover()
						defer wg.Done()
						results[i] = worker.ServeStart(ctx, req)
					}(i, worker)
				}
				wg.Wait()
				Expect(results[0].Error).To(BeEmpty())
				Expect(results[1]).To(Equal(results[0]))
				Eventually(h.Sent, 10*time.Second).Should(Equal([]string{"same words"}))
				h.Gate <- struct{}{}
				Eventually(func() store.TurnStatus { info, _ := st.Turn(ctx, "alice", "chat"); return info.Last }, 10*time.Second).Should(Equal(store.TurnDone))
				Expect(svc.Close(ctx)).To(Succeed())
				Expect(other.Close(ctx)).To(Succeed())
				reopened := open()
				retry := makeService(reopened)
				req.Text = "changed on retry"
				Expect(retry.ServeStart(ctx, req)).To(Equal(results[0]))
				Expect(h.Sent()).To(Equal([]string{"same words"}))
				frozen, found, e := reopened.MemoryInput(ctx, "alice", "chat", "memory:delivery-1")
				Expect(e).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(frozen.Text).To(Equal("same words"))
				Expect(frozen.Pending.Text).To(Equal("same words"))
				_, _, e = reopened.MemoryInput(ctx, "bob", "chat", "memory:delivery-1")
				Expect(e).To(HaveOccurred())
				req.RequestID, req.Text = "delivery-2", "same words"
				Expect(retry.ServeStart(ctx, req).Error).To(BeEmpty())
				Eventually(h.Sent, 10*time.Second).Should(Equal([]string{"same words", "same words"}))
				h.Gate <- struct{}{}
				Eventually(func() bool { info, _ := reopened.Turn(ctx, "alice", "chat"); return !info.Running }, 10*time.Second).Should(BeTrue())
				records, e := reopened.ListCaptures(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				Expect(records).To(BeEmpty())
			})
		}
	}
	It("replays the same trigger fire through a real worker after completion", func() {
		ctx := context.Background()
		st, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		h := studiotest.NewGateHarness()
		worker, err := New(Deps{Store: st, Provider: provider.New(), Harness: h, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = worker.Close(ctx) })
		frontend, err := New(Deps{Store: st, Provider: provider.New(), Workers: deliveryWorker{worker}, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = frontend.Close(ctx) })
		dir := GinkgoT().TempDir()
		ts, err := triggers.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		tr := triggers.Trigger{ID: "trigger", Owner: "alice", Name: "Daily", Model: "m", Prompt: "trigger words", Kind: triggers.KindSchedule}
		Expect(ts.PutTrigger(ctx, tr)).To(Succeed())
		fire := triggers.Run{ID: "fire", TriggerID: tr.ID, DueKey: "due", Status: triggers.StatusRunning}
		ok, err := ts.Claim(ctx, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		host, err := NewTriggers(frontend, TriggerOptions{Store: ts})
		Expect(err).NotTo(HaveOccurred())
		id, err := host.start(ctx, tr, fire)
		Expect(err).NotTo(HaveOccurred())
		Eventually(h.Sent, 10*time.Second).Should(HaveLen(1))
		Expect(h.Sent()[0]).To(HavePrefix("trigger words"))
		original := h.Sent()
		h.Gate <- struct{}{}
		Eventually(func() bool { info, _ := st.Turn(ctx, "alice", id); return !info.Running }, 10*time.Second).Should(BeTrue())
		host.Close()
		ts, err = triggers.NewJSON(dir)
		Expect(err).NotTo(HaveOccurred())
		host, err = NewTriggers(frontend, TriggerOptions{Store: ts})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host.Close)
		replay, err := host.start(ctx, tr, fire)
		Expect(err).NotTo(HaveOccurred())
		Expect(replay).To(Equal(id))
		Expect(h.Sent()).To(Equal(original))
		records, err := st.ListCaptures(ctx, "alice", id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
	})
})
