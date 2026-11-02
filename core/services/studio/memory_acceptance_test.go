// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"net/http"
	"time"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type acceptanceBackend struct {
	*timeoutBackend
	writes  []lagi.MemoryWrite
	receipt lagi.MemoryReceipt
	fail    bool
	block   func(context.Context)
}

func (b *acceptanceBackend) ReconcileCapture(context.Context, memory.Identity, string) (lagi.MemoryReceipt, error) {
	b.calls = append(b.calls, "reconcile")
	return b.receipt, nil
}
func (b *acceptanceBackend) WriteCapture(ctx context.Context, _ memory.Identity, _ lagi.MemoryFence, w lagi.MemoryWrite, auth func(context.Context) error) (lagi.MemoryReceipt, error) {
	if err := auth(ctx); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	b.calls = append(b.calls, "write")
	b.writes = append(b.writes, w)
	if b.block != nil {
		b.block(ctx)
		return lagi.MemoryReceipt{}, ctx.Err()
	}
	if b.fail {
		return lagi.MemoryReceipt{}, errors.New("temporary backend failure")
	}
	return lagi.MemoryReceipt{State: lagi.MemorySaved, EntryID: "exact-entry"}, nil
}

// Record real durable reducer transitions without substituting the reducer or store.
type acceptanceStore struct {
	store.Store
	store.CaptureMemoryStore
	rules *[]string
}

func (s *acceptanceStore) ApplyCapture(ctx context.Context, c store.CaptureClaim, tr memory.Transition) error {
	if err := s.Store.ApplyCapture(ctx, c, tr); err != nil {
		return err
	}
	*s.rules = append(*s.rules, tr.RuleID)
	return nil
}

type captureFixture struct {
	ctx          context.Context
	path         string
	st           store.Store
	as           agents.Store
	svc          *Service
	backend      *acceptanceBackend
	clock        time.Time
	summaries    int
	rules        []string
	afterSummary func()
}

func newCaptureFixture(summary bool) *captureFixture {
	f := &captureFixture{ctx: context.Background(), path: GinkgoT().TempDir(), clock: time.Now().Truncate(time.Second)}
	var err error
	f.st, err = store.NewJSON(f.path)
	Expect(err).NotTo(HaveOccurred())
	f.st = &acceptanceStore{Store: f.st, CaptureMemoryStore: f.st.(store.CaptureMemoryStore), rules: &f.rules}
	f.as, err = agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	mode := "incoming_only"
	policy := store.CaptureIncoming
	if summary {
		mode = "summary"
		policy = store.CaptureSummary
	}
	f.policy(mode)
	Expect(f.st.CreateChat(f.ctx, session.ChatRecord{ID: "chat", Principal: "alice", AgentID: "agent", Model: "summary-model"})).To(Succeed())
	_, err = f.st.AcceptMemoryInput(f.ctx, store.MemoryAcceptance{Principal: "alice", ChatID: "chat", SourceID: "input", Text: "original input", Policy: store.CapturePolicy{AgentID: "agent", Destination: "docs", Mode: policy}})
	Expect(err).NotTo(HaveOccurred())
	if summary {
		Expect(f.st.ClaimTurn(f.ctx, "alice", "chat", "run")).To(Succeed())
		Expect(f.st.CompleteMemoryTurn(f.ctx, store.MemoryTurnCompletion{Principal: "alice", ChatID: "chat", RunID: "run", ReplyID: "reply", ReplyText: "answer", Status: store.TurnDone, IncomingIDs: []string{"input"}})).To(Succeed())
	}
	f.backend = &acceptanceBackend{timeoutBackend: &timeoutBackend{}}
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.summaries++
		if f.afterSummary != nil {
			f.afterSummary()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"frozen summary"},"finish_reason":"stop"}]}`))
	}))
	f.svc = &Service{d: Deps{Store: f.st, Agents: f.as, Collections: f.backend, Provider: p}}
	return f
}
func (f *captureFixture) policy(mode string) {
	Expect(f.as.Put(f.ctx, agents.Agent{ID: "agent", Principal: "alice", Name: "recorder", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs"}}}, Memory: &agents.MemorySettings{Version: 1, Destination: "docs", ToolStorage: "content", AutomaticIngestion: mode}})).To(Succeed())
}
func (f *captureFixture) record() store.CaptureRecord {
	records, err := f.st.ListCaptures(f.ctx, "alice", "chat")
	Expect(err).NotTo(HaveOccurred())
	Expect(records).To(HaveLen(1))
	return records[0]
}
func (f *captureFixture) run(owner string, publish func(store.CaptureRecord, memory.PublishStatus)) {
	claim, err := f.st.ClaimCapture(f.ctx, owner, f.clock, time.Minute)
	Expect(err).NotTo(HaveOccurred())
	f.rules = append(f.rules, claim.Transition.RuleID)
	Expect(f.svc.runCapture(f.ctx, claim, f.backend, f.st.(store.CaptureMemoryStore), func() time.Time { return f.clock }, publish)).To(Succeed())
}
func (f *captureFixture) due() {
	state, err := f.record().ReducerState()
	Expect(err).NotTo(HaveOccurred())
	f.clock = time.Unix(state.(memory.RetryWaiting).Due, 0)
}
func (f *captureFixture) restart() {
	var err error
	f.st, err = store.NewJSON(f.path)
	Expect(err).NotTo(HaveOccurred())
	f.st = &acceptanceStore{Store: f.st, CaptureMemoryStore: f.st.(store.CaptureMemoryStore), rules: &f.rules}
	deps := f.svc.d
	deps.Store = f.st
	f.svc = &Service{d: deps}
}

var _ = Describe("Task6 remaining acceptance", func() {
	It("R04-summary R04-durable R06-recover R08-retry-queued preserve frozen summary and key across restart and a new bounded cycle", func() {
		f := newCaptureFixture(true)
		f.backend.fail = true
		f.run("first", nil)
		Expect(f.rules).To(ContainElements("R02-claim", "R03-transform", "R04-summary", "R04-durable", "R05-write", "R06-wait"))
		original := f.record()
		Expect(original.RuleID).To(Equal("R06-wait"))
		Expect(f.summaries).To(Equal(1))
		Expect(original.InputType).To(Equal("FrozenPayload"))
		for i := 0; i < 2; i++ {
			f.due()
			f.restart()
			f.run("restarted", nil)
		}
		Expect(f.rules).To(ContainElement("R06-recover"))
		failed := f.record()
		Expect(failed.RuleID).To(Equal("R06-exhausted"))
		Expect(f.backend.writes).To(HaveLen(3))
		Expect(f.st.RetryCapture(f.ctx, "mallory", "chat", failed.ID)).To(HaveOccurred())
		Expect(f.st.RetryCapture(f.ctx, "alice", "chat", failed.ID)).To(Succeed())
		Expect(f.record().RuleID).To(Equal("R08-retry-queued"))
		f.restart()
		f.run("explicit-retry", nil)
		state, err := f.record().ReducerState()
		Expect(err).NotTo(HaveOccurred())
		Expect(state.(memory.RetryWaiting).Job.Attempts).To(Equal(1))
		f.due()
		f.backend.fail = false
		f.run("retry-success", nil)
		Expect(f.record().RuleID).To(Equal("R05-saved"))
		Expect(f.record().Identity).To(Equal(original.Identity))
		Expect(f.summaries).To(Equal(1))
		for _, w := range f.backend.writes {
			Expect(w).To(Equal(f.backend.writes[0]))
			Expect(w.Key).To(Equal(original.ID))
			Expect(w.Content).To(Equal("frozen summary"))
		}
	})
	It("R03-denied reauthorizes after summary and after queue delay without rerouting", func() {
		f := newCaptureFixture(true)
		f.afterSummary = func() { f.policy("off") }
		f.run("summary", nil)
		Expect(f.record().RuleID).To(Equal("R03-denied"))
		Expect(f.summaries).To(Equal(1))
		Expect(f.backend.calls).To(BeEmpty())
		g := newCaptureFixture(false)
		g.policy("off")
		g.clock = g.clock.Add(30 * time.Second)
		g.run("delayed", nil)
		Expect(g.record().RuleID).To(Equal("R03-denied"))
		Expect(g.backend.calls).To(BeEmpty())
		_, err := g.st.ClaimCapture(g.ctx, "again", g.clock.Add(time.Hour), time.Minute)
		Expect(err).To(MatchError(store.ErrNoCapture))
	})
	It("R07-deleted reconciles a backend tombstone without ever recreating it after restart", func() {
		f := newCaptureFixture(false)
		f.backend.fail = true
		f.run("ambiguous", nil)
		f.due()
		f.restart()
		f.backend.receipt = lagi.MemoryReceipt{State: lagi.MemoryDeleted}
		f.run("recover", nil)
		Expect(f.record().RuleID).To(Equal("R07-deleted"))
		Expect(f.backend.writes).To(HaveLen(1))
		f.restart()
		_, err := f.st.ClaimCapture(f.ctx, "later", f.clock.Add(time.Hour), time.Minute)
		Expect(err).To(MatchError(store.ErrNoCapture))
		Expect(f.st.RetryCapture(f.ctx, "alice", "chat", f.record().ID)).To(MatchError(store.ErrCaptureConflict))
	})
	It("R02-claim publishes across two services with synchronous FakeBus reload reentry and polling after missed fanout", func() {
		f := newCaptureFixture(false)
		second := &Service{d: f.svc.d}
		net := testutil.NewFakeBus()
		f.svc.bus = NewBroadcastBus(net, nil)
		seen := 0
		sub, err := net.Subscribe(eventSubject(key("alice", "chat")), func(_ []byte) {
			records, e := second.d.Store.ListCaptures(f.ctx, "alice", "chat")
			Expect(e).NotTo(HaveOccurred())
			Expect(records).To(HaveLen(1))
			seen++
			// Reenter the durable writer as well as its reader. No publish may hold its lock.
			_, e = second.d.Store.ClaimCapture(f.ctx, "second", f.clock, time.Minute)
			Expect(e).To(MatchError(store.ErrNoCapture))
		})
		Expect(err).NotTo(HaveOccurred())
		f.run("first", func(r store.CaptureRecord, status memory.PublishStatus) {
			if status.Status == "Authorizing" && r.InputType == "ContentInput" {
				Expect(r.RuleID).To(Equal("R02-claim"))
			}
			Expect(f.record().Revision).To(Equal(r.Revision))
			Expect(r.State).To(Equal(status.Status))
			if status.Status != "Saved" {
				f.svc.bus.Publish(key("alice", "chat"), session.Event{Kind: session.KindText, Text: status.Status})
			}
		})
		Expect(seen).To(BeNumerically(">", 0))
		Expect(sub.Unsubscribe()).To(Succeed())
		// The terminal notification was deliberately dropped; a fresh store reload is truth.
		second.d.Store, err = store.NewJSON(f.path)
		Expect(err).NotTo(HaveOccurred())
		records, err := second.d.Store.ListCaptures(f.ctx, "alice", "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(records[0].RuleID).To(Equal("R05-saved"))
	})
	It("bounds idle and in-flight shutdown by caller cancellation", func() {
		for _, pending := range []bool{false, true} {
			f := newCaptureFixture(false)
			entered := make(chan struct{})
			if pending {
				f.backend.block = func(ctx context.Context) { close(entered); <-ctx.Done() }
			} else {
				f.run("complete", nil)
			}
			ctx, cancel := context.WithCancel(f.ctx)
			DeferCleanup(cancel)
			done := make(chan error, 1)
			go func() { defer GinkgoRecover(); done <- f.svc.ProcessMemoryCaptures(ctx) }()
			if pending {
				Eventually(entered, time.Second).Should(BeClosed())
			}
			cancel()
			Eventually(done, time.Second).Should(Receive(MatchError(context.Canceled)))
		}
	})
})
