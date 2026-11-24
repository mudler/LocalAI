// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// createRuntime is a stand-in addon that creates a backing per resource ID,
// idempotently by operation ID, with injectable lost replies.
type createRuntime struct {
	mu       sync.Mutex
	backing  map[string]bool
	receipts map[string]contract.Receipt
	applied  []string
	// commitFirst commits and loses the reply once; failFirst fails once
	// without creating anything; rejectNext refuses once.
	commitFirst, failFirst, rejectNext bool
	// hold runs after a creation was recorded and before it is answered.
	hold func()
}

func newCreateRuntime() *createRuntime {
	return &createRuntime{backing: map[string]bool{}, receipts: map[string]contract.Receipt{}}
}

func (r *createRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, nil
}

func (r *createRuntime) Preview(_ context.Context, e contract.Envelope) (contract.Preview, error) {
	return contract.Preview{OperationID: e.Request.OperationID, Revision: contract.Revision{Schema: 1}, ExpiresAt: time.Now().Add(100 * 24 * time.Hour),
		Summary: json.RawMessage(`{"count":1,"sample":{"action":"addon says something else"}}`)}, nil
}

func (r *createRuntime) Apply(_ context.Context, e contract.Envelope, _ contract.Revision) (contract.Receipt, error) {
	r.mu.Lock()
	id := e.Request.OperationID
	r.applied = append(r.applied, id+"@"+e.Request.ResourceID+":"+e.Request.Action)
	if prior, ok := r.receipts[id]; ok {
		r.mu.Unlock()
		return prior, nil
	}
	if r.failFirst {
		r.failFirst = false
		r.mu.Unlock()
		return contract.Receipt{}, errors.New("connection reset before the addon took it")
	}
	if r.rejectNext {
		r.rejectNext = false
		rc := contract.Receipt{OperationID: id, State: contract.ReceiptRejected}
		r.receipts[id] = rc
		r.mu.Unlock()
		return rc, nil
	}
	rc := contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1}}
	r.receipts[id] = rc
	r.backing[e.Request.ResourceID] = true
	lost, hold := r.commitFirst, r.hold
	r.commitFirst = false
	r.mu.Unlock()
	if hold != nil {
		hold()
	}
	if lost {
		return contract.Receipt{}, errors.New("reply lost")
	}
	return rc, nil
}

func (r *createRuntime) Receipt(_ context.Context, id string) (contract.Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rc, ok := r.receipts[id]; ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: id, State: contract.ReceiptUnknown}, nil
}

func (r *createRuntime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errors.New("no outputs")
}

func (r *createRuntime) has(id string) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.backing[id] }
func (r *createRuntime) appliedCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.applied...)
}

// createRig is an owner whose broker and policy share the stand-in addon, a
// switchable creation authority and a recorded existence check.
type createRig struct {
	dir       string
	store     *Store
	broker    *Broker
	policy    *Policy
	rt        *createRuntime
	instances *fixtureInstances
	clock     *clock
	accounts  *account
	tools     *mutableTools
	pending   int

	mu        sync.Mutex
	deny      bool
	existsErr error
	asked     []string
}

func newCreateRig(pending int) *createRig {
	rt := newCreateRuntime()
	c := &createRig{dir: GinkgoT().TempDir(), rt: rt, instances: &fixtureInstances{gen: 1, rt: rt}, clock: &clock{t: time.Now()},
		accounts: &account{blocked: map[string]bool{}}, tools: &mutableTools{tools: []string{"databases-create"}, kinds: map[string]string{"databases-create": contract.ToolKindCreate}}, pending: pending}
	c.open()
	DeferCleanup(func() { c.close() })
	return c
}

func (c *createRig) authority(context.Context, contract.Actor, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deny {
		return fmt.Errorf("creation authority withdrawn: %w", gateway.ErrForbidden)
	}
	return nil
}

func (c *createRig) exists(_ context.Context, key ResourceKey) (bool, error) {
	c.mu.Lock()
	c.asked = append(c.asked, key.ResourceID)
	err := c.existsErr
	c.mu.Unlock()
	if err != nil {
		return false, err
	}
	return c.rt.has(key.ResourceID), nil
}

func (c *createRig) askedFor(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.asked {
		if a == id {
			return true
		}
	}
	return false
}

func (c *createRig) open() {
	GinkgoHelper()
	store, err := OpenStore(filepath.Join(c.dir, "policy"), ModeNamedUser)
	Expect(err).NotTo(HaveOccurred())
	broker, err := OpenBroker(filepath.Join(c.dir, "proposals"), BrokerOptions{Runtime: c.instances, Tools: c.tools, Now: c.clock.now, BackingExists: c.exists})
	Expect(err).NotTo(HaveOccurred())
	p := New(store, Options{Revalidate: c.accounts.check, CreateAuthority: c.authority, Proposals: broker, DrainTimeout: 2 * time.Second, MaxPendingPerUser: c.pending})
	broker.Attach(p)
	c.store, c.broker, c.policy = store, broker, p
}

func (c *createRig) close() {
	_ = c.broker.Close()
	_ = c.store.Close()
}

// restart closes the owner as a crash would and opens it on the same state.
func (c *createRig) restart() {
	c.close()
	c.open()
}

func (c *createRig) request(op, name string) contract.Request {
	return contract.Request{AddonID: "fixture", OperationID: op, Generation: 1, Action: "create",
		Arguments: json.RawMessage(fmt.Sprintf(`{"tool":"databases-create","name":%q}`, name))}
}

func (c *createRig) propose(user, op, name string) Proposal {
	GinkgoHelper()
	p, err := c.broker.Propose(context.Background(), named(user), c.request(op, name))
	Expect(err).NotTo(HaveOccurred())
	return p
}

func (c *createRig) pendingIDs() []string {
	ids, err := c.policy.Pending()
	Expect(err).NotTo(HaveOccurred())
	return ids
}

func (c *createRig) accessible(user string) []ResourceKey {
	GinkgoHelper()
	keys, err := c.policy.Accessible(context.Background(), named(user), "fixture")
	Expect(err).NotTo(HaveOccurred())
	return keys
}

var _ = Describe("create proposals", Label("proposals", "create"), func() {
	ctx := context.Background()

	It("reserves a create proposal as a pending resource that nobody can reach", func() {
		c := newCreateRig(0)
		p := c.propose("owner", "create-1", "Sales")
		Expect(p.State).To(Equal(gateway.ProposalPending))
		Expect(p.Action).To(Equal("create"))
		Expect(p.ResourceID).NotTo(BeEmpty())
		// The confirming human sees what the host built from the requested
		// name, not what the addon wrote.
		Expect(p.Effects.Count).To(Equal(uint64(1)))
		Expect(string(p.Effects.Sample)).To(ContainSubstring(`"name":"Sales"`))
		Expect(string(p.Effects.Sample)).NotTo(ContainSubstring("addon says"))
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		key := ResourceKey{AddonID: "fixture", ResourceID: p.ResourceID}
		for _, who := range []string{"owner", "other"} {
			Expect(c.accessible(who)).To(BeEmpty())
			Expect(c.policy.Authorize(ctx, named(who), key, ActionRead)).To(MatchError(ErrForbidden))
		}
		Expect(c.rt.appliedCalls()).To(BeEmpty())
		Expect(c.rt.has(p.ResourceID)).To(BeFalse())
		// The same request again is the same proposal and one reservation.
		again := c.propose("owner", "create-1", "Sales")
		Expect(again.ResourceID).To(Equal(p.ResourceID))
		Expect(c.pendingIDs()).To(HaveLen(1))
		_, err := c.broker.Propose(ctx, named("owner"), c.request("create-1", "Other"))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.pendingIDs()).To(HaveLen(1))
	})

	It("commits the reservation on confirmation and makes the proposer the private owner", func() {
		c := newCreateRig(0)
		p := c.propose("owner", "create-1", "Sales")
		receipt, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(receipt.OperationID).To(Equal("create-" + p.ResourceID))
		Expect(c.rt.appliedCalls()).To(Equal([]string{"create-" + p.ResourceID + "@" + p.ResourceID + ":schema"}))
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(Equal([]ResourceKey{{AddonID: "fixture", ResourceID: p.ResourceID}}))
		Expect(c.accessible("other")).To(BeEmpty())
		key := ResourceKey{AddonID: "fixture", ResourceID: p.ResourceID}
		Expect(c.policy.Authorize(ctx, named("owner"), key, ActionDelete)).To(Succeed())
		Expect(c.policy.Authorize(ctx, named("other"), key, ActionRead)).To(MatchError(ErrForbidden))
		// A repeated confirmation is the stored answer and creates nothing.
		again, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(receipt))
		Expect(c.rt.appliedCalls()).To(HaveLen(1))
		got, err := c.broker.Get(ctx, named("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
	})

	It("abandons the reservation when a create proposal is cancelled or expires", func() {
		c := newCreateRig(0)
		cancelled := c.propose("owner", "create-1", "A")
		expiring := c.propose("owner", "create-2", "B")
		Expect(c.pendingIDs()).To(HaveLen(2))
		Expect(c.broker.Cancel(ctx, named("owner"), cancelled.ID)).To(Succeed())
		Expect(c.pendingIDs()).To(Equal([]string{expiring.ResourceID}))
		c.clock.advance(contract.DefaultLimits().ProposalExpiry + time.Second)
		Expect(c.broker.Sweep()).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
		_, err := c.broker.Confirm(ctx, session("owner"), expiring.ID)
		Expect(err).To(MatchError(ErrProposalExpired))
		_, err = c.broker.Confirm(ctx, session("owner"), cancelled.ID)
		Expect(err).To(MatchError(ErrProposalClosed))
		Expect(c.accessible("owner")).To(BeEmpty())
		Expect(c.rt.appliedCalls()).To(BeEmpty())
	})

	It("abandons the reservation when the addon rejects the creation", func() {
		c := newCreateRig(0)
		c.rt.rejectNext = true
		p := c.propose("owner", "create-1", "Sales")
		receipt, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptRejected))
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(BeEmpty())
	})

	It("keeps a lost create reply pending and unknown and settles it by receipt", func() {
		c := newCreateRig(0)
		c.rt.commitFirst = true
		p := c.propose("owner", "create-1", "Sales")
		_, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal(p.ID))
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		Expect(c.accessible("owner")).To(BeEmpty())
		got, err := c.broker.Get(ctx, named("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalOutcomeUnknown))
		// The lookup finds the receipt of the creation and settles both. It
		// never applies a second time.
		rec, err := c.broker.reconcileFor(ctx, named("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(gateway.ProposalApplied))
		Expect(c.rt.appliedCalls()).To(HaveLen(1))
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(HaveLen(1))
	})

	It("abandons an unknown create only when the addon confirms the backing is absent", func() {
		c := newCreateRig(0)
		c.rt.failFirst = true
		p := c.propose("owner", "create-1", "Sales")
		_, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		// Another user cannot resolve it, and an unbound digest is refused.
		_, err = c.broker.Resolve(ctx, session("other"), p.ID, p.Digest, "gone")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = c.broker.Resolve(ctx, session("owner"), p.ID, "", "gone")
		Expect(err).To(MatchError(ErrProposalChanged))
		// The addon cannot answer: the proposal is abandoned, the reservation
		// is not dropped.
		c.mu.Lock()
		c.existsErr = errors.New("addon down")
		c.mu.Unlock()
		res, err := c.broker.Resolve(ctx, session("owner"), p.ID, p.Digest, "addon was down")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.State).To(Equal(gateway.ProposalAbandoned))
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		Expect(c.accessible("owner")).To(BeEmpty())
		// The addon says it exists: kept pending for the late receipt.
		q := newCreateRig(0)
		q.rt.commitFirst = true
		kept := q.propose("owner", "create-1", "Sales")
		_, _ = q.broker.Confirm(ctx, session("owner"), kept.ID)
		q.rt.mu.Lock()
		delete(q.rt.receipts, "create-"+kept.ResourceID)
		q.rt.mu.Unlock()
		_, err = q.broker.Resolve(ctx, session("owner"), kept.ID, kept.Digest, "receipt lost")
		Expect(err).NotTo(HaveOccurred())
		Expect(q.pendingIDs()).To(Equal([]string{kept.ResourceID}))
		Expect(q.askedFor(kept.ResourceID)).To(BeTrue())
		// The addon confirms there is no backing: the reservation is dropped.
		d := newCreateRig(0)
		d.rt.failFirst = true
		gone := d.propose("owner", "create-1", "Sales")
		_, _ = d.broker.Confirm(ctx, session("owner"), gone.ID)
		_, err = d.broker.Resolve(ctx, session("owner"), gone.ID, gone.Digest, "addon never ran it")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.pendingIDs()).To(BeEmpty())
		Expect(d.askedFor(gone.ResourceID)).To(BeTrue())
	})

	It("settles reservations at restart by asking the addon and keeps those of live proposals", func() {
		c := newCreateRig(0)
		live := c.propose("owner", "create-live", "Live")
		c.rt.commitFirst = true
		created := c.propose("owner", "create-made", "Made")
		_, err := c.broker.Confirm(ctx, session("owner"), created.ID)
		Expect(err).To(HaveOccurred())
		c.rt.failFirst = true
		lost := c.propose("owner", "create-lost", "Lost")
		_, err = c.broker.Confirm(ctx, session("owner"), lost.ID)
		Expect(err).To(HaveOccurred())
		Expect(c.pendingIDs()).To(HaveLen(3))
		c.restart()
		c.mu.Lock()
		c.asked = nil
		c.mu.Unlock()
		Expect(c.policy.Reconcile(ctx, c.exists)).To(Succeed())
		// A pending proposal holds its reservation without any question.
		Expect(c.pendingIDs()).To(Equal([]string{live.ResourceID}))
		Expect(c.askedFor(live.ResourceID)).To(BeFalse())
		// An unknown outcome is settled only after the addon was asked.
		Expect(c.askedFor(created.ResourceID)).To(BeTrue())
		Expect(c.askedFor(lost.ResourceID)).To(BeTrue())
		Expect(c.accessible("owner")).To(Equal([]ResourceKey{{AddonID: "fixture", ResourceID: created.ResourceID}}))
		Expect(c.broker.Recover(ctx)).To(Succeed())
		// The background lookup then records the receipt of the creation.
		Expect(c.broker.RetryUnresolved(ctx)).To(BeNumerically(">=", 1))
		got, err := c.broker.Get(ctx, named("owner"), created.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		// The live proposal is still confirmable after the restart.
		receipt, err := c.broker.Confirm(ctx, session("owner"), live.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(c.accessible("owner")).To(HaveLen(2))
	})

	It("bounds concurrent create proposals by the pending quota", func() {
		c := newCreateRig(2)
		var wg sync.WaitGroup
		results := make([]error, 6)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, results[i] = c.broker.Propose(ctx, named("owner"), c.request(fmt.Sprintf("create-%d", i), fmt.Sprintf("db%d", i)))
			}()
		}
		wg.Wait()
		ok := 0
		for _, e := range results {
			if e == nil {
				ok++
			} else {
				Expect(e).To(MatchError(ErrQuota))
			}
		}
		Expect(ok).To(Equal(2))
		Expect(c.pendingIDs()).To(HaveLen(2))
		// Another user keeps their own share.
		other := c.propose("other", "create-other", "Mine")
		Expect(other.State).To(Equal(gateway.ProposalPending))
		Expect(c.pendingIDs()).To(HaveLen(3))
	})

	It("lets only the proposer confirm, cancel or read a create proposal", func() {
		c := newCreateRig(0)
		p := c.propose("owner", "create-1", "Sales")
		_, err := c.broker.Confirm(ctx, session("other"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.broker.Cancel(ctx, named("other"), p.ID)).To(MatchError(ErrForbidden))
		_, err = c.broker.Get(ctx, named("other"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		// An API key proposes and never approves.
		_, err = c.broker.Confirm(ctx, named("owner"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		Expect(c.rt.appliedCalls()).To(BeEmpty())
		Expect(c.accessible("other")).To(BeEmpty())
	})

	It("denies a create proposal when creation authority is revoked before confirmation", func() {
		c := newCreateRig(0)
		c.mu.Lock()
		c.deny = true
		c.mu.Unlock()
		_, err := c.broker.Propose(ctx, named("owner"), c.request("create-0", "Sales"))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.pendingIDs()).To(BeEmpty())
		c.mu.Lock()
		c.deny = false
		c.mu.Unlock()
		p := c.propose("owner", "create-1", "Sales")
		c.mu.Lock()
		c.deny = true
		c.mu.Unlock()
		_, err = c.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.rt.appliedCalls()).To(BeEmpty())
		Expect(c.accessible("owner")).To(BeEmpty())
		// Withdrawing is still allowed and drops the reservation.
		Expect(c.broker.Cancel(ctx, named("owner"), p.ID)).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
	})

	It("denies a create proposal for an undeclared tool or an existing resource without reserving", func() {
		c := newCreateRig(0)
		bad := c.request("create-1", "Sales")
		bad.Arguments = json.RawMessage(`{"tool":"wipe","name":"Sales"}`)
		_, err := c.broker.Propose(ctx, named("owner"), bad)
		Expect(err).To(MatchError(ErrForbidden))
		noName := c.request("create-2", "")
		noName.Arguments = json.RawMessage(`{"tool":"databases-create"}`)
		_, err = c.broker.Propose(ctx, named("owner"), noName)
		Expect(err).To(MatchError(ErrForbidden))
		// A create request never names a resource: the host issues the ID.
		made := c.propose("owner", "create-3", "Sales")
		_, err = c.broker.Confirm(ctx, session("owner"), made.ID)
		Expect(err).NotTo(HaveOccurred())
		existing := c.request("create-4", "Again")
		existing.ResourceID = made.ResourceID
		_, err = c.broker.Propose(ctx, named("owner"), existing)
		Expect(err).To(MatchError(ErrForbidden))
		// A write proposal cannot be aimed at a reservation.
		held := c.propose("owner", "create-5", "Held")
		write := contract.Request{AddonID: "fixture", ResourceID: held.ResourceID, OperationID: "write-1", Generation: 1, Action: "write",
			Arguments: json.RawMessage(`{"tool":"databases-create","name":"x"}`)}
		_, err = c.broker.Propose(ctx, named("owner"), write)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.pendingIDs()).To(Equal([]string{held.ResourceID}))
		// The tool rule holds again at confirmation.
		c.tools.set([]string{"databases-rename"})
		_, err = c.broker.Confirm(ctx, session("owner"), held.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(c.rt.appliedCalls()).To(HaveLen(1))
	})

	It("does not let reconcile drop the reservation of a creation in flight", func() {
		c := newCreateRig(0)
		// The direct path of the policy.
		entered, release := make(chan struct{}), make(chan struct{})
		var key ResourceKey
		done := make(chan error, 1)
		go func() {
			var err error
			key, err = c.policy.Create(ctx, named("owner"), "fixture", func(context.Context, ResourceKey) error {
				close(entered)
				<-release
				return nil
			})
			done <- err
		}()
		<-entered
		Expect(c.policy.Reconcile(ctx, func(context.Context, ResourceKey) (bool, error) { return false, nil })).To(Succeed())
		Expect(c.pendingIDs()).To(HaveLen(1))
		close(release)
		Expect(<-done).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(Equal([]ResourceKey{key}))

		// The confirmation path of the broker.
		p := c.propose("owner", "create-1", "Sales")
		in, out := make(chan struct{}), make(chan struct{})
		c.rt.mu.Lock()
		c.rt.hold = func() { close(in); <-out }
		c.rt.mu.Unlock()
		finished := make(chan error, 1)
		go func() {
			_, err := c.broker.Confirm(ctx, session("owner"), p.ID)
			finished <- err
		}()
		<-in
		Expect(c.policy.Reconcile(ctx, func(context.Context, ResourceKey) (bool, error) { return false, nil })).To(Succeed())
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		close(out)
		Expect(<-finished).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(HaveLen(2))
	})

	It("commits the reservation at recovery when the crash came after the receipt was stored", func() {
		c := newCreateRig(0)
		c.broker.hooks.afterSettle = func(string) {
			// The reservation cannot be written from here on, as after a crash.
			c.store.mu.Lock()
			c.store.failed = true
			c.store.mu.Unlock()
		}
		p := c.propose("owner", "create-1", "Sales")
		_, err := c.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.pendingIDs()).To(Equal([]string{p.ResourceID}))
		c.restart()
		Expect(c.accessible("owner")).To(BeEmpty())
		Expect(c.broker.Recover(ctx)).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
		Expect(c.accessible("owner")).To(Equal([]ResourceKey{{AddonID: "fixture", ResourceID: p.ResourceID}}))
		// A rejected create left behind is dropped the same way.
		c.rt.rejectNext = true
		q := c.propose("owner", "create-2", "Other")
		c.broker.hooks.afterSettle = func(string) {
			c.store.mu.Lock()
			c.store.failed = true
			c.store.mu.Unlock()
		}
		_, err = c.broker.Confirm(ctx, session("owner"), q.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.pendingIDs()).To(Equal([]string{q.ResourceID}))
		c.restart()
		Expect(c.broker.Recover(ctx)).To(Succeed())
		Expect(c.pendingIDs()).To(BeEmpty())
	})
})
