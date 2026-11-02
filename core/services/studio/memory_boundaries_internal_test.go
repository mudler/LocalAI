// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type unavailableCaptureStore struct{ store.Store }

func (s unavailableCaptureStore) MemoryInput(context.Context, string, string, string) (store.AcceptedInput, bool, error) {
	return store.AcceptedInput{}, false, errors.New("capture lookup unavailable")
}

func (s unavailableCaptureStore) AcceptMemoryInput(context.Context, store.MemoryAcceptance) (store.AcceptedInput, error) {
	return store.AcceptedInput{}, errors.New("capture unavailable")
}
func (s unavailableCaptureStore) CompleteMemoryTurn(context.Context, store.MemoryTurnCompletion) error {
	return errors.New("capture unavailable")
}

var _ = Describe("automatic memory payload isolation", func() {
	It("answers background input when capture acceptance alone is unavailable", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_only"}})).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent", Model: "m"})).To(Succeed())
		h := studiotest.NewGateHarness()
		svc, err := New(Deps{Store: unavailableCaptureStore{st}, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		result := svc.ServeStart(ctx, workerctl.ChatStartRequest{Principal: "alice", ChatID: "chat", Text: "still answer", RequestID: "request"})
		Expect(result.Error).To(BeEmpty())
		Expect(result.Started).To(BeTrue())
		Eventually(h.Sent, time.Second*10).Should(Equal([]string{"still answer"}))
		h.Gate <- struct{}{}
		Eventually(func() store.TurnStatus { info, _ := st.Turn(ctx, "alice", "chat"); return info.Last }, time.Second*10).Should(Equal(store.TurnDone))
		records, err := st.ListCaptures(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].State).To(Equal("Failed"))
	})

	It("separates notes and attachment paths from accepted pending and direct text", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag := agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent", Model: "resolved-model"})).To(Succeed())
		svc, err := New(Deps{Store: st, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		payload := session.ComposeMessage("typed words", "extracted secret", []session.Attachment{{Path: "/private/file"}})
		pending, err := svc.acceptPendingMemory(ctx, "alice", "chat", "typed words", payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending.Text).To(Equal(payload))
		Expect(svc.acceptDirectMemory(ctx, "alice", "chat", "direct words")).NotTo(BeEmpty())
		records, err := st.ListCaptures(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		for _, r := range records {
			Expect(r.RawText).NotTo(ContainSubstring("secret"))
			Expect(r.RawText).NotTo(ContainSubstring("private"))
		}
		waiting, err := st.Pending(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(waiting).To(HaveLen(1))
	})
	It("releases a successful answer even if capture completion fails", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())

		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_only"}})).To(Succeed())
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent"})).To(Succeed())
		svc, err := New(Deps{Store: unavailableCaptureStore{st}, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		_, err = st.AcceptMemoryInput(ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "memory:input", Text: "input", Delivered: true, Policy: store.CapturePolicy{AgentID: "agent", Destination: "notes", Mode: store.CaptureIncoming}})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.ClaimTurn(ctx, "alice", "chat", "run")).To(Succeed())
		Expect(svc.completeMemoryRun(ctx, "alice", "chat", &run{id: "run", memoryInputs: []string{"memory:input"}, memoryReply: "answer"}, store.TurnDone)).To(Succeed())
		info, err := st.Turn(ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Running).To(BeFalse())
		Expect(info.Last).To(Equal(store.TurnDone))
		p, err := svc.acceptPendingMemory(ctx, "alice", "chat", "still answered", "still answered")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Text).To(Equal("still answered"))
	})
	It("does not accept an internal goal continuation as incoming", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		h := studiotest.NewGateHarness()
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.ClaimTurn(ctx, "alice", rec.ID, "run")).To(Succeed())
		Expect(svc.startClaimed(ctx, "alice", rec.ID, "run", session.Message{Text: "internal scheduling", Goal: true})).To(Succeed())
		Eventually(func() int { return len(h.Sent()) }, time.Second*10).Should(Equal(1))
		h.Gate <- struct{}{}
		Eventually(func() bool { info, _ := st.Turn(ctx, "alice", rec.ID); return !info.Running }, time.Second*10).Should(BeTrue())
		records, err := st.ListCaptures(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
	})
})

// Completion-only injection keeps immutable acceptance/membership healthy.
type unavailableCompletionStore struct{ store.Store }

func (s unavailableCompletionStore) CompleteMemoryTurn(context.Context, store.MemoryTurnCompletion) error {
	return errors.New("private failure body must never be retained")
}

var _ = Describe("capture failure visibility", func() {
	for _, backend := range []string{"JSON", "SQLite", "PostgreSQL"} {
		for _, stage := range []string{"acceptance", "completion", "summary completion"} {
			backend, stage := backend, stage
			It("persists safe "+stage+" failure after actual ServeStart and reopen on "+backend, func() {
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
				as, e := agents.NewJSON("")
				Expect(e).NotTo(HaveOccurred())
				ag := agents.Agent{ID: "agent", Principal: "alice", Name: "Recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "notes"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "notes", ToolStorage: "content", AutomaticIngestion: "incoming_and_replies"}}
				if stage == "summary completion" {
					ag.Memory.AutomaticIngestion = "summary"
				}
				Expect(as.Put(ctx, ag)).To(Succeed())
				Expect(st.CreateChat(ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent", Model: "m"})).To(Succeed())
				var injected store.Store = unavailableCaptureStore{st}
				if stage != "acceptance" {
					injected = unavailableCompletionStore{st}
				}
				h := studiotest.NewGateHarness()
				svc, e := New(Deps{Store: injected, Agents: as, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
				Expect(e).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = svc.Close(ctx) })
				result := svc.ServeStart(ctx, workerctl.ChatStartRequest{Principal: "alice", ChatID: "chat", Text: "private incoming body", RequestID: "stable-delivery"})
				Expect(result.Error).To(BeEmpty())
				Expect(result.Started).To(BeTrue())
				Eventually(h.Sent, 10*time.Second).Should(Equal([]string{"private incoming body"}))
				h.Gate <- struct{}{}
				Eventually(func() store.TurnStatus { info, _ := st.Turn(ctx, "alice", "chat"); return info.Last }, 10*time.Second).Should(Equal(store.TurnDone))
				Expect(svc.Close(ctx)).To(Succeed())
				reopened := open()
				records, e := reopened.ListCaptures(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				if stage != "acceptance" {
					kind, text := "reply", "working on: private incoming bodydone"
					if stage == "summary completion" {
						kind, text = "summary", "private incoming body\n\n"+text
					}
					var completed []store.CaptureRecord
					for _, r := range records {
						if r.Identity.Kind == kind {
							completed = append(completed, r)
						}
					}
					Expect(completed).To(HaveLen(1), "successful completion must retain a recoverable payload")
					r := completed[0]
					Expect(r.CaptureUnavailable).To(BeFalse())
					Expect(r.RawText).To(Equal(text))
					Expect(r.State).To(Equal("Queued"))
					Expect(r.SummaryModel).To(Equal("m"))
					Expect(r.Identity.Destination).To(Equal("notes"))
					completion := store.MemoryTurnCompletion{Principal: "alice", ChatID: "chat", RunID: r.Identity.Source, ReplyID: r.Identity.Source, ReplyText: "working on: private incoming bodydone", Status: store.TurnDone, IncomingIDs: []string{"memory:stable-delivery"}}
					Expect(reopened.CompleteMemoryTurn(ctx, completion)).To(Succeed())
					Expect(reopened.RecordMemoryCompletionFailure(ctx, completion)).To(Succeed())
					completion.ReplyText = "different"
					Expect(reopened.RecordMemoryCompletionFailure(ctx, completion)).To(MatchError(store.ErrCaptureConflict))
					ag.Memory.Destination = "changed"
					ag.CollectionPermissions.Grants = append(ag.CollectionPermissions.Grants, agents.CollectionGrant{Collection: "changed"})
					Expect(as.Put(ctx, ag)).To(Succeed())
					again, err := reopened.ListCaptures(ctx, "alice", "chat")
					Expect(err).NotTo(HaveOccurred())
					Expect(again).To(ConsistOf(records))
					claim, err := reopened.ClaimCapture(ctx, "recovery", time.Now(), time.Minute)
					Expect(err).NotTo(HaveOccurred())
					Expect(claim.Record.Identity.Destination).To(Equal("notes"))
					Expect(claim.Record.RawText).NotTo(BeEmpty())
					return
				}
				var failures []store.CaptureRecord
				for _, r := range records {
					if r.CaptureUnavailable {
						failures = append(failures, r)
					}
				}
				Expect(failures).To(HaveLen(1))
				r := failures[0]
				Expect(r.State).To(Equal("Failed"))
				Expect(r.Identity.Destination).To(Equal("notes"))
				Expect(r.Identity.Source).NotTo(BeEmpty())
				Expect(r.RawText).To(BeEmpty())
				Expect(string(r.StateJSON)).NotTo(ContainSubstring("private"))
				Expect(reopened.RetryCapture(ctx, "alice", "chat", r.ID)).To(MatchError(store.ErrCaptureConflict))
				_, e = reopened.ListCaptures(ctx, "bob", "chat")
				Expect(e).To(HaveOccurred())
				in := store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: r.Identity.Source, Text: "private incoming body", Policy: store.CapturePolicy{AgentID: "agent", Destination: "changed", Mode: store.CaptureReplies}}
				if stage == "acceptance" {
					Expect(reopened.RecordMemoryAcceptanceFailure(ctx, in)).To(Succeed())
					_, e = reopened.AcceptMemoryInput(ctx, in)
					Expect(e).To(MatchError(store.ErrCaptureConflict))
				}
				again, e := reopened.ListCaptures(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				// ListCaptures projects a map; order is not part of the storage contract.
				Expect(again).To(ConsistOf(records))
				in.Principal = "bob"
				Expect(reopened.RecordMemoryAcceptanceFailure(ctx, in)).To(HaveOccurred())
				in.Principal = "alice"
				in.SourceID = "off"
				in.Policy.Mode = store.CaptureOff
				Expect(reopened.RecordMemoryAcceptanceFailure(ctx, in)).To(Succeed())
				again, e = reopened.ListCaptures(ctx, "alice", "chat")
				Expect(e).NotTo(HaveOccurred())
				// ListCaptures projects a map; order is not part of the storage contract.
				Expect(again).To(ConsistOf(records))
				Expect(st.CreateChat(ctx, session.ChatRecord{ID: "plain", Principal: "alice", Model: "m"})).To(Succeed())
				in.ChatID = "plain"
				in.Policy.Mode = store.CaptureIncoming
				Expect(reopened.RecordMemoryAcceptanceFailure(ctx, in)).To(HaveOccurred())
			})
		}
	}
})
