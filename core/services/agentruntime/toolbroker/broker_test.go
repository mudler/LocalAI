// SPDX-License-Identifier: MIT
package toolbroker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("broker shell", func() {
	var b *Broker
	var dep Dependencies
	var auth Authority
	var now time.Time
	var manager *ownership.Manager
	var db *gorm.DB
	var path string
	var request BeginRequest
	var count atomic.Int32
	ctx := context.Background()
	open := func() (*ownership.Manager, *store.Store, *gorm.DB) {
		d, e := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(e).NotTo(HaveOccurred())
		sql, e := d.DB()
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sql.Close()).To(Succeed()) })
		m, e := ownership.New(d)
		Expect(e).NotTo(HaveOccurred())
		s, e := store.New(d)
		Expect(e).NotTo(HaveOccurred())
		return m, s, d
	}
	catalog := func() Catalog {
		c, e := NewCatalog([]ToolSpec{{ID: "read", Name: "read", Class: ReadTool, Revision: "1"}, {ID: "write", Name: "write", Class: WriteTool, Revision: "1"}})
		Expect(e).NotTo(HaveOccurred())
		return c
	}
	makeBroker := func(d Dependencies) *Broker {
		x, e := NewBroker(d, catalog())
		Expect(e).NotTo(HaveOccurred())
		return x
	}
	begin := func() (CallHandle, View) {
		h, v, e := b.Begin(ctx, request)
		Expect(e).NotTo(HaveOccurred())
		return h, v
	}
	exec := func(context.Context, Execution) (ToolOutput, error) {
		count.Add(1)
		return NewOutput([]byte(`{"private":"raw-sentinel","ok":true}`)), nil
	}
	invoke := func(h CallHandle) Outcome {
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		o, e := b.Invoke(ctx, c, exec)
		Expect(e).NotTo(HaveOccurred())
		return o
	}
	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "broker.db")
		var s *store.Store
		manager, s, db = open()
		now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		count.Store(0)
		Expect(s.Create(ctx, "alice", "chat")).To(Succeed())
		_, e := s.Admit(ctx, "alice", "chat", "turn", nil)
		Expect(e).NotTo(HaveOccurred())
		lease, e := manager.Acquire(ctx, ownership.Key{Principal: "alice", Conversation: "chat"}, "host", now, time.Minute)
		Expect(e).NotTo(HaveOccurred())
		cfg := fixture(AllowAll)
		cfg.ApprovalTTL = int64(10 * time.Second)
		request = BeginRequest{Config: cfg}
		auth = Authority{Scope: cfg.Binding.Scope(), Revisions: cfg.Binding.revisions, Lease: lease, Policy: AllowAll, Selection: Selection{Scope: cfg.Binding.Scope(), Selected: []ToolID{"read", "write"}, ACL: []ToolID{"read", "write"}, Resources: []ToolID{"read", "write"}}}
		dep = Dependencies{Clock: func() time.Time { return now }, Current: func(context.Context, Binding) (Authority, error) { return auth, nil }, Reader: s, Journal: manager, Approvals: func(_ context.Context, d ApprovalDecision) (Approval, error) { return d.Approval, nil }, Audit: func(context.Context, AuditEntry) error { return nil }, Helpers: func(context.Context, Scope, string) (HelperGrant, error) { return HelperGrant{}, ErrInvalid }, Sanitize: func(context.Context, Binding, ToolOutput) (Output, error) {
			return NewOutput([]byte(`{"ok":true,"projected":true}`)), nil
		}}
		b = makeBroker(dep)
	})
	It("executes once and persists only projected output", func() {
		h, v := begin()
		Expect(v.Phase).To(Equal("Authorized"))
		o := invoke(h)
		Expect(o.Status).To(Equal("Completed"))
		Expect(count.Load()).To(Equal(int32(1)))
		rows, e := manager.Records(ctx, auth.Lease, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(string(rows[0].Result)).To(Equal(`{"ok":true,"projected":true}`))
	})
	It("does not execute from a running phase without a newly checked RunExecutor effect", func() {
		h, _ := begin()
		claim, err := b.ClaimCall(ctx, h)
		Expect(err).NotTo(HaveOccurred())
		// A prior transition's phase is not an execution capability. InvokeRequested
		// in runningBody is ignored and emits no RunExecutor.
		c := h.call
		c.state = step(c.state, InvokeRequested{b.input(c)}).State
		c.state = step(c.state, AdmissionPassed{Input: b.input(c), Evidence: Admission{true, true, true, true, true}}).State
		_, err = b.Invoke(ctx, claim, exec)
		Expect(err).NotTo(HaveOccurred())
		Expect(count.Load()).To(BeZero())
	})
	It("consumes checked RunExecutor only once for its exact broker call and binding", func() {
		h, _ := begin()
		_, err := b.ClaimCall(ctx, h)
		Expect(err).NotTo(HaveOccurred())
		effects, err := b.applyChecked(ctx, h.call, InvokeRequested{b.input(h.call)})
		Expect(err).NotTo(HaveOccurred())
		Expect(effects.runs).To(HaveLen(1))
		_, ok := b.takeRun(ctx, &brokerCall{state: h.call.state}, effects)
		Expect(ok).To(BeFalse())
		binding, ok := b.takeRun(ctx, h.call, effects)
		Expect(ok).To(BeTrue())
		Expect(binding).To(Equal(request.Config.Binding))
		_, ok = b.takeRun(ctx, h.call, effects)
		Expect(ok).To(BeFalse())
	})
	It("rejects a checked RunExecutor after binding changes", func() {
		h, _ := begin()
		_, err := b.ClaimCall(ctx, h)
		Expect(err).NotTo(HaveOccurred())
		effects, err := b.applyChecked(ctx, h.call, InvokeRequested{b.input(h.call)})
		Expect(err).NotTo(HaveOccurred())
		original := h.call.state
		h.call.state.config.Binding = BindingMismatchWitnesses(original.Binding())[0]
		_, ok := b.takeRun(ctx, h.call, effects)
		Expect(ok).To(BeFalse())
		h.call.state = original
		// A mismatched capability stays consumed, even if the state is restored.
		_, ok = b.takeRun(ctx, h.call, effects)
		Expect(ok).To(BeFalse())
		Expect(count.Load()).To(BeZero())
	})
	for _, boundary := range []string{"prepare", "dispatch", "final invocation"} {
		for _, late := range []time.Duration{0, time.Nanosecond} {
			It(fmt.Sprintf("rejects approval expiry after trusted admission at %s boundary plus %s", boundary, late), func() {
				request.Config.Policy = RequireApproval
				auth.Policy = RequireApproval
				h, v := begin()
				Expect(v.Phase).To(Equal("WaitingApproval"))
				deadline := now.Add(time.Second)
				v, err := b.Decide(ctx, ApprovalDecision{Handle: h, Allow: true, Approval: Approval{Binding: request.Config.Binding, Actor: HumanActor, ActorID: "human", ExpiresAt: deadline.UnixNano()}})
				Expect(err).NotTo(HaveOccurred())
				Expect(v.Phase).To(Equal("Authorized"))
				advanced := false
				b.dep.Current = func(context.Context, Binding) (Authority, error) {
					atBoundary := false
					switch p := h.call.state.phase.(type) {
					case Claiming:
						atBoundary = boundary == "prepare" && p.stage == awaitingPrepare || boundary == "dispatch" && p.stage == awaitingDispatch
					case Executing:
						atBoundary = boundary == "final invocation" && p.stage == runningBody
					}
					if atBoundary && !advanced {
						now = deadline.Add(late)
						advanced = true
					}
					return auth, nil
				}
				claim, err := b.ClaimCall(ctx, h)
				if boundary == "final invocation" {
					Expect(err).NotTo(HaveOccurred())
					_, err = b.Invoke(ctx, claim, exec)
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(MatchError(ErrUnavailable))
				}
				Expect(advanced).To(BeTrue())
				Expect(count.Load()).To(BeZero())
				rows, err := manager.Records(ctx, auth.Lease, now, "turn")
				Expect(err).NotTo(HaveOccurred())
				switch boundary {
				case "prepare":
					Expect(rows).To(BeEmpty())
				case "dispatch":
					Expect(rows).To(HaveLen(1))
					Expect(rows[0].Status).To(Equal(ownership.Prepared))
				case "final invocation":
					Expect(rows).To(HaveLen(1))
					Expect(rows[0].Status).To(Equal(ownership.Dispatched))
				}
				v, err = b.View(h)
				Expect(err).NotTo(HaveOccurred())
				if boundary == "final invocation" {
					Expect(v.Phase).To(Equal("Unknown"))
				} else {
					Expect(v.Phase).To(Equal("Denied"))
				}
			})
		}
	}
	It("keeps nonwaiting AllowAll free of an approval deadline after trusted admission", func() {
		h, _ := begin()
		advanced := false
		b.dep.Current = func(context.Context, Binding) (Authority, error) {
			if !advanced {
				now = now.Add(11 * time.Second)
				advanced = true
			}
			return auth, nil
		}
		o := invoke(h)
		Expect(o.Status).To(Equal("Completed"))
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("requires every trusted dependency", func() {
		d := dep
		d.Current = nil
		_, e := NewBroker(d, catalog())
		Expect(e).To(HaveOccurred())
		d = dep
		d.Sanitize = nil
		_, e = NewBroker(d, catalog())
		Expect(e).To(HaveOccurred())
	})
	It("rejects zero handles and claims", func() {
		_, e := b.ClaimCall(ctx, CallHandle{})
		Expect(e).To(HaveOccurred())
		_, e = b.Invoke(ctx, Claim{}, exec)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(BeZero())
	})
	It("binds claims to the creating broker", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		other := makeBroker(dep)
		_, e = other.Invoke(ctx, c, exec)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(BeZero())
	})
	It("consumes a claim once even with concurrent invokes", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = b.Invoke(ctx, c, exec) }()
		}
		wg.Wait()
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("lets independent database handles dispatch only one body", func() {
		m, s, _ := open()
		d := dep
		d.Journal = m
		d.Reader = s
		other := makeBroker(d)
		h, _ := begin()
		h2, _, e := other.Begin(ctx, request)
		Expect(e).NotTo(HaveOccurred())
		var wg sync.WaitGroup
		for _, x := range []struct {
			b *Broker
			h CallHandle
		}{{b, h}, {other, h2}} {
			wg.Add(1)
			go func(x struct {
				b *Broker
				h CallHandle
			}) {
				defer wg.Done()
				c, e := x.b.ClaimCall(ctx, x.h)
				if e == nil {
					_, _ = x.b.Invoke(ctx, c, exec)
				}
			}(x)
		}
		wg.Wait()
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("reuses completed records after broker restart without a new claim", func() {
		h, _ := begin()
		invoke(h)
		b = makeBroker(dep)
		h, _ = begin()
		_, e := b.ClaimCall(ctx, h)
		Expect(e).To(MatchError(ErrRecorded))
		v, e := b.View(h)
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Phase).To(Equal("Completed"))
		Expect(v.Result.Bytes()).To(ContainSubstring("projected"))
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("does not retry a dispatched intent after restart", func() {
		h, _ := begin()
		_, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		b = makeBroker(dep)
		h, _ = begin()
		_, e = b.ClaimCall(ctx, h)
		Expect(e).To(HaveOccurred())
		v, _ := b.View(h)
		Expect(v.Phase).To(Equal("Unknown"))
		Expect(count.Load()).To(BeZero())
	})
	It("conflicts on exact argument bytes under one host request ID", func() {
		h, _ := begin()
		invoke(h)
		c := request.Config
		c.Binding, _ = NewBinding(c.Binding.scope, c.Binding.request, c.Binding.tool, []byte("different"), c.Binding.revisions, c.Binding.providerKey)
		request.Config = c
		b = makeBroker(dep)
		h, _ = begin()
		_, e := b.ClaimCall(ctx, h)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("conflicts on tool identity under one host request ID", func() {
		h, _ := begin()
		invoke(h)
		c := request.Config
		c.Binding, _ = NewBinding(c.Binding.scope, c.Binding.request, "write", c.Binding.Arguments(), c.Binding.revisions, c.Binding.providerKey)
		request.Config = c
		b = makeBroker(dep)
		h, _ = begin()
		_, e := b.ClaimCall(ctx, h)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("rejects an expired lease before claim", func() {
		h, _ := begin()
		now = now.Add(time.Minute)
		_, e := b.ClaimCall(ctx, h)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(BeZero())
	})
	It("leaves dispatched unresolved on lease loss before Invoke", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		now = now.Add(time.Minute)
		o, _ := b.Invoke(ctx, c, exec)
		Expect(o.Status).To(Equal("Unknown"))
		Expect(count.Load()).To(BeZero())
	})
	It("rechecks the same ACL filter at Invoke", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		auth.Selection.ACL = nil
		o, _ := b.Invoke(ctx, c, exec)
		Expect(o.Status).To(Equal("Unknown"))
		Expect(count.Load()).To(BeZero())
	})
	It("requires a live matching running conversation", func() {
		Expect(dep.Reader.(*store.Store).Delete(ctx, "alice", "chat")).To(Succeed())
		_, v := begin()
		Expect(v.Phase).To(Equal("Denied"))
		Expect(count.Load()).To(BeZero())
	})
	It("keeps body failure Unknown with no replay", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		o, _ := b.Invoke(ctx, c, func(context.Context, Execution) (ToolOutput, error) {
			count.Add(1)
			return Output{}, errors.New("raw-sentinel")
		})
		Expect(o.Status).To(Equal("Unknown"))
		_, e = b.ClaimCall(ctx, h)
		Expect(e).To(HaveOccurred())
		Expect(count.Load()).To(Equal(int32(1)))
	})
	It("keeps executed but unpersisted completion Unknown", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		o, _ := b.Invoke(ctx, c, func(context.Context, Execution) (ToolOutput, error) {
			count.Add(1)
			Expect(manager.Release(ctx, auth.Lease, now)).To(Succeed())
			return NewOutput([]byte(`{"ok":true}`)), nil
		})
		Expect(o.Status).To(Equal("Unknown"))
		Expect(count.Load()).To(Equal(int32(1)))
		rows := []struct{ Status string }{}
		Expect(db.Table("runtime_ownership_tools").Find(&rows).Error).To(Succeed())
		Expect(rows[0].Status).To(Equal("dispatched"))
	})
	It("fails closed when projection fails", func() {
		dep.Sanitize = func(context.Context, Binding, ToolOutput) (Output, error) {
			return Output{}, errors.New("raw-sentinel")
		}
		b = makeBroker(dep)
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		o, _ := b.Invoke(ctx, c, exec)
		Expect(o.Status).To(Equal("Unknown"))
		rows, e := manager.Records(ctx, auth.Lease, now, "turn")
		Expect(e).NotTo(HaveOccurred())
		Expect(rows[0].Result).To(BeEmpty())
	})
	It("waits for human approval for externally tainted writes even AllowAll", func() {
		request.Config.ExternalTaintedWrite = true
		auth.ExternalTaintedWrite = true
		h, v := begin()
		Expect(v.Phase).To(Equal("WaitingApproval"))
		a := Approval{Binding: request.Config.Binding, Actor: HostActor, ActorID: "host", ExpiresAt: now.Add(time.Second).UnixNano()}
		v, e := b.Decide(ctx, ApprovalDecision{Handle: h, Approval: a, Allow: true})
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Phase).To(Equal("WaitingApproval"))
		a.Actor = HumanActor
		v, e = b.Decide(ctx, ApprovalDecision{Handle: h, Approval: a, Allow: true})
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Phase).To(Equal("Authorized"))
		invoke(h)
	})
	It("rejects approval at the exact expiry boundary", func() {
		request.Config.Policy = RequireApproval
		auth.Policy = RequireApproval
		h, _ := begin()
		now = now.Add(10 * time.Second)
		v, e := b.Decide(ctx, ApprovalDecision{Handle: h, Allow: true, Approval: Approval{Binding: request.Config.Binding, Actor: HumanActor, ActorID: "human", ExpiresAt: now.UnixNano()}})
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Phase).To(Equal("Denied"))
	})
	It("cancellation invalidates an already dispatched claim", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		Expect(b.Cancel(ctx, h, CancelUser)).To(Succeed())
		o, _ := b.Invoke(ctx, c, exec)
		Expect(o.Status).To(Equal("Unknown"))
		Expect(count.Load()).To(BeZero())
	})
	It("does not leak private shell values via formatting or JSON", func() {
		h, _ := begin()
		c, e := b.ClaimCall(ctx, h)
		Expect(e).NotTo(HaveOccurred())
		for _, x := range []any{h, c, request, auth, Execution{binding: request.Config.Binding}} {
			Expect(fmt.Sprintf("%v %+v %#v", x, x, x)).NotTo(ContainSubstring("sentinel"))
			data, e := json.Marshal(x)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(data)).NotTo(ContainSubstring("sentinel"))
		}
	})
})
