// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// session is the credential of a human at the host's confirmation surface.
func session(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "session:" + id, Mode: "named-user"}
}

// fixtureRuntime is a stand-in addon with a revision, an idempotent receipt
// journal and injectable failures. It proves the broker's state machine before
// any real addon exists.
type fixtureRuntime struct {
	mu          sync.Mutex
	data        uint64
	receipts    map[string]contract.Receipt
	applied     []string
	previews    int
	seenArgs    map[string]string
	commitFirst bool // commit, then fail the reply
	failFirst   bool // fail without committing
	// applyErr, when set, is returned by the next Apply before the request is
	// recorded, as a client that answers without the add-on seeing it.
	applyErr    error
	holdReply   func(ctx context.Context)
	receiptMode string // "", "pending", "unavailable", "block"
	lookups     int
	// afterCommit runs once an apply committed and its reply is about to be
	// returned.
	afterCommit func()
	// entered is signalled when a lookup in "block" mode starts. The lookup
	// then waits for its context or for release.
	entered chan struct{}
	release chan struct{}
	// returned is closed when a blocked lookup came back.
	returned     chan struct{}
	returnedOnce sync.Once
}

func newFixtureRuntime() *fixtureRuntime {
	return &fixtureRuntime{data: 1, receipts: map[string]contract.Receipt{}, seenArgs: map[string]string{}, entered: make(chan struct{}, 16), release: make(chan struct{}), returned: make(chan struct{})}
}

func (r *fixtureRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, nil
}

func (r *fixtureRuntime) Preview(_ context.Context, e contract.Envelope) (contract.Preview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.previews++
	return contract.Preview{
		OperationID: e.Request.OperationID,
		Revision:    contract.Revision{Schema: 1, Data: r.data},
		ExpiresAt:   time.Now().Add(100 * 24 * time.Hour),
		Summary:     json.RawMessage(`{"count":3,"sample":[{"row":1},{"row":2}]}`),
	}, nil
}

func (r *fixtureRuntime) Apply(ctx context.Context, e contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	r.mu.Lock()
	id := e.Request.OperationID
	if err := r.applyErr; err != nil {
		r.applyErr = nil
		r.mu.Unlock()
		return contract.Receipt{}, err
	}
	r.applied = append(r.applied, id)
	r.seenArgs[id] = string(e.Request.Arguments)
	if prior, ok := r.receipts[id]; ok {
		r.mu.Unlock()
		return prior, nil
	}
	if r.failFirst {
		r.failFirst = false
		r.mu.Unlock()
		return contract.Receipt{}, errors.New("connection reset before the runtime took it")
	}
	var receipt contract.Receipt
	if rev.Data != r.data {
		receipt = contract.Receipt{OperationID: id, State: contract.ReceiptRejected}
	} else {
		r.data++
		receipt = contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: rev.Schema, Data: r.data}}
	}
	r.receipts[id] = receipt
	commitFirst, hold := r.commitFirst, r.holdReply
	r.commitFirst = false
	r.mu.Unlock()
	if hold != nil {
		hold(ctx)
		return contract.Receipt{}, errors.New("reply lost")
	}
	if commitFirst {
		return contract.Receipt{}, errors.New("reply lost")
	}
	r.mu.Lock()
	after := r.afterCommit
	r.mu.Unlock()
	if after != nil {
		after()
	}
	return receipt, nil
}

func (r *fixtureRuntime) Receipt(ctx context.Context, id string) (contract.Receipt, error) {
	r.mu.Lock()
	r.lookups++
	if r.receiptMode == "block" {
		r.mu.Unlock()
		r.entered <- struct{}{}
		select {
		case <-ctx.Done():
		case <-r.release:
		}
		r.returnedOnce.Do(func() { close(r.returned) })
		return contract.Receipt{}, errors.New("lookup interrupted")
	}
	defer r.mu.Unlock()
	switch r.receiptMode {
	case "unavailable":
		return contract.Receipt{}, errors.New("runtime unavailable")
	case "pending":
		return contract.Receipt{OperationID: id, State: contract.ReceiptPending}, nil
	}
	if rc, ok := r.receipts[id]; ok {
		return rc, nil
	}
	return contract.Receipt{OperationID: id, State: contract.ReceiptUnknown}, nil
}

func (r *fixtureRuntime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errors.New("no outputs")
}

func (r *fixtureRuntime) applyCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.applied...)
}

func (r *fixtureRuntime) lookupCount() int { r.mu.Lock(); defer r.mu.Unlock(); return r.lookups }

func (r *fixtureRuntime) setReceiptMode(m string) { r.mu.Lock(); r.receiptMode = m; r.mu.Unlock() }

func (r *fixtureRuntime) bumpData() { r.mu.Lock(); r.data++; r.mu.Unlock() }

type fixtureInstances struct {
	mu  sync.Mutex
	gen uint64
	rt  contract.Runtime
}

func (f *fixtureInstances) Runtime(context.Context, string) (addonruntime.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return addonruntime.Instance{Generation: f.gen, Incarnation: "current", Client: f.rt}, nil
}

func (f *fixtureInstances) setGeneration(g uint64) { f.mu.Lock(); f.gen = g; f.mu.Unlock() }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type brokerFixture struct {
	dir       string
	mode      Mode
	store     *Store
	broker    *Broker
	policy    *Policy
	accounts  *account
	rt        *fixtureRuntime
	instances *fixtureInstances
	clock     *clock
	tune      []func(*BrokerOptions)
}

// openBrokerFixture opens the policy store and the broker in dir, as a
// restarted owner does.
func openBrokerFixture(mode Mode, dir string, rt *fixtureRuntime, instances *fixtureInstances, c *clock, accounts *account, tune ...func(*BrokerOptions)) *brokerFixture {
	store, err := OpenStore(filepath.Join(dir, "policy"), mode)
	Expect(err).NotTo(HaveOccurred())
	opts := BrokerOptions{Runtime: instances, Now: c.now}
	for _, t := range tune {
		t(&opts)
	}
	broker, err := OpenBroker(filepath.Join(dir, "proposals"), opts)
	Expect(err).NotTo(HaveOccurred())
	p := New(store, Options{
		Revalidate:      accounts.check,
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		Proposals:       broker,
		DrainTimeout:    2 * time.Second,
	})
	broker.Attach(p)
	f := &brokerFixture{dir: dir, mode: mode, store: store, broker: broker, policy: p, accounts: accounts, rt: rt, instances: instances, clock: c, tune: tune}
	return f
}

func newBrokerFixture(mode Mode, tune ...func(*BrokerOptions)) *brokerFixture {
	rt := newFixtureRuntime()
	f := openBrokerFixture(mode, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: rt},
		&clock{t: time.Now()}, &account{blocked: map[string]bool{}}, tune...)
	DeferCleanup(func() { f.close() })
	return f
}

func (f *brokerFixture) close() {
	_ = f.broker.Close()
	_ = f.store.Close()
}

// restart closes the owner as a crash would and opens it again on the same
// durable state and the same addon runtime.
func (f *brokerFixture) restart() *brokerFixture {
	f.close()
	g := openBrokerFixture(f.mode, f.dir, f.rt, f.instances, f.clock, f.accounts, f.tune...)
	DeferCleanup(func() { g.close() })
	return g
}

func (f *brokerFixture) create(owner contract.Actor) ResourceKey {
	key, err := f.policy.Register(context.Background(), owner, "fixture")
	Expect(err).NotTo(HaveOccurred())
	Expect(f.policy.Commit(context.Background(), owner, key)).To(Succeed())
	return key
}

func (f *brokerFixture) request(key ResourceKey, op, action, args string) contract.Request {
	return contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: op, Generation: 1, Action: action, Arguments: json.RawMessage(args)}
}

// stored reads the proposal state from disk, not from memory.
func (f *brokerFixture) stored(id string) ProposalState {
	b, err := os.ReadFile(filepath.Join(f.dir, "proposals", proposalFileName))
	Expect(err).NotTo(HaveOccurred())
	var d proposalDocument
	Expect(json.Unmarshal(b, &d)).To(Succeed())
	r, ok := d.Proposals[id]
	if !ok {
		return ""
	}
	return r.State
}

var _ = Describe("proposal broker", Label("proposals"), func() {
	ctx := context.Background()

	It("stores a pending proposal with its exact effects and never executes it", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{ "rows": [ {"a": 1}, {"a": 2}, {"a": 3} ] }`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.ID).To(Equal("op-1"))
		Expect(p.OperationID).To(Equal("op-1"))
		Expect(p.State).To(Equal(gateway.ProposalPending))
		Expect(p.Effects.Count).To(Equal(uint64(3)))
		Expect(string(p.Effects.Sample)).To(Equal(`[{"row":1},{"row":2}]`))
		Expect(string(p.Arguments)).To(Equal(`{"rows":[{"a":1},{"a":2},{"a":3}]}`))
		Expect(p.Revision).To(Equal(contract.Revision{Schema: 1, Data: 1}))
		Expect(p.Digest).NotTo(BeEmpty())
		Expect(p.ExpiresAt).To(BeTemporally("~", f.clock.now().Add(contract.DefaultLimits().ProposalExpiry), time.Second))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalPending))
		got, err := f.broker.Get(ctx, owner, "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Digest).To(Equal(p.Digest))
	})

	It("applies exactly the stored arguments once and answers a repeat from the stored receipt", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		receipt, err := f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(receipt.OperationID).To(Equal("op-1"))
		Expect(f.rt.applyCalls()).To(Equal([]string{"op-1"}))
		Expect(f.rt.seenArgs["op-1"]).To(Equal(`{"rows":[{"a":1}]}`))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalApplied))
		again, err := f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(receipt))
		// The same request again is the same settled proposal, not a new one.
		same, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(same.State).To(Equal(gateway.ProposalApplied))
		Expect(f.rt.applyCalls()).To(HaveLen(1))
		Expect(f.rt.previews).To(Equal(1))
	})

	It("refuses changed arguments, other actors, unknown identifiers and an unbound digest", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, other := named("owner"), named("other")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "other", RoleEditor)).To(Succeed())
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		// The operation ID cannot be reused with other arguments, by anyone.
		_, err = f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[{"a":999}]}`))
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.Propose(ctx, other, f.request(key, "op-1", "write", `{"rows":[{"a":1}]}`))
		Expect(err).To(MatchError(ErrForbidden))
		// Another account cannot read, cancel or confirm it, even with a session.
		_, err = f.broker.Confirm(ctx, session("other"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.Get(ctx, other, p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.broker.Cancel(ctx, other, p.ID)).To(MatchError(ErrForbidden))
		_, err = f.broker.Confirm(ctx, session("owner"), "unknown")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.ConfirmBound(ctx, session("owner"), p.ID, "not-the-digest")
		Expect(err).To(MatchError(ErrProposalChanged))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		got, err := f.broker.Get(ctx, owner, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalPending))
		Expect(string(got.Arguments)).To(Equal(`{"rows":[{"a":1}]}`))
		_, err = f.broker.ConfirmBound(ctx, session("owner"), p.ID, p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.seenArgs["op-1"]).To(Equal(`{"rows":[{"a":1}]}`))
	})

	It("never lets an API key, plain text or a boolean approve", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"confirmed":true,"approved":"yes","note":"user said yes"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.State).To(Equal(gateway.ProposalPending))
		// The same user, but the unattended key credential that proposed it.
		_, err = f.broker.Confirm(ctx, owner, p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.ConfirmBound(ctx, owner, p.ID, p.Digest)
		Expect(err).To(MatchError(ErrForbidden))
		// A session credential that is not named at all.
		bare := contract.Actor{UserID: "owner", CredentialID: "session:", Mode: "named-user"}
		_, err = f.broker.Confirm(ctx, bare, p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		// A proposal that carries approval words in its arguments is only data.
		got, err := f.broker.Get(ctx, owner, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalPending))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		// The gateway adapter refuses a confirmation that names no digest.
		_, err = f.broker.Gateway().Confirm(ctx, session("owner"), p.ID, "")
		Expect(err).To(MatchError(ErrProposalChanged))
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("accepts the local owner as the confirming principal in local-owner mode only", func() {
		f := newBrokerFixture(ModeLocalOwner)
		local := contract.Actor{UserID: LocalOwnerID, CredentialID: LocalOwnerID, Mode: "local-owner"}
		key := f.create(local)
		p, err := f.broker.Propose(ctx, local, f.request(key, "op-1", "write", `{"rows":[]}`))
		Expect(err).NotTo(HaveOccurred())
		viaKey := contract.Actor{UserID: LocalOwnerID, CredentialID: "key:agent", Mode: "local-owner"}
		_, err = f.broker.Confirm(ctx, viaKey, p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		receipt, err := f.broker.Confirm(ctx, local, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
	})

	It("expires a proposal and refuses a late or repeated confirmation", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[]}`))
		Expect(err).NotTo(HaveOccurred())
		f.clock.advance(contract.DefaultLimits().ProposalExpiry + time.Second)
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(MatchError(ErrProposalExpired))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalExpired))
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(MatchError(ErrProposalExpired))
		got, err := f.broker.Get(ctx, owner, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalExpired))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		// The operation ID is spent: a replay of the request returns the same
		// expired proposal and neither previews nor stores anything new.
		again, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(gateway.ProposalExpired))
		Expect(f.rt.previews).To(Equal(1))
	})

	It("refuses a proposal made stale by a new generation or a moved revision", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "revision", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		// The data changed after the human was shown the preview: the runtime
		// validates the revision transactionally and refuses.
		f.rt.bumpData()
		receipt, err := f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptRejected))
		Expect(f.stored("revision")).To(Equal(gateway.ProposalRejected))
		Expect(f.rt.receipts["revision"].State).To(Equal(contract.ReceiptRejected))
		g, err := f.broker.Propose(ctx, owner, f.request(key, "generation", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		f.instances.setGeneration(2)
		before := len(f.rt.applyCalls())
		_, err = f.broker.Confirm(ctx, session("owner"), g.ID)
		Expect(err).To(MatchError(ErrProposalStale))
		Expect(f.stored("generation")).To(Equal(gateway.ProposalRejected))
		Expect(f.rt.applyCalls()).To(HaveLen(before))
		// A request that names the old generation is refused at proposal time.
		_, err = f.broker.Propose(ctx, owner, f.request(key, "late", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrProposalStale))
	})

	It("cancels a proposal for good and restricts who may propose which action", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor, viewer := named("owner"), named("editor"), named("viewer")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		Expect(f.policy.SetGrant(ctx, owner, key, "viewer", RoleViewer)).To(Succeed())
		p, err := f.broker.Propose(ctx, editor, f.request(key, "op-1", "write", `{"rows":[]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.broker.Cancel(ctx, editor, p.ID)).To(Succeed())
		Expect(f.broker.Cancel(ctx, editor, p.ID)).To(Succeed())
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalCancelled))
		_, err = f.broker.Confirm(ctx, session("editor"), p.ID)
		Expect(err).To(MatchError(ErrProposalClosed))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		_, err = f.broker.Propose(ctx, viewer, f.request(key, "op-2", "write", `{"rows":[]}`))
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.Propose(ctx, editor, f.request(key, "op-3", "schema", `{"add":"t"}`))
		Expect(err).To(MatchError(ErrForbidden))
		s, err := f.broker.Propose(ctx, owner, f.request(key, "op-4", "schema", `{"add":"t"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Action).To(Equal("schema"))
		stranger := named("stranger")
		_, err = f.broker.Propose(ctx, stranger, f.request(key, "op-5", "write", `{"rows":[]}`))
		Expect(err).To(MatchError(ErrForbidden))
	})

	It("never accepts sharing, deletion, reads or unclassified actions as proposals", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		for i, action := range []string{"share", "delete", "read", "output", "install", "anything"} {
			_, err := f.broker.Propose(ctx, owner, f.request(key, fmt.Sprintf("op-%d", i), action, `{}`))
			Expect(err).To(MatchError(ErrForbidden), action)
		}
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op-args", "write", `[1,2]`))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.previews).To(BeZero())
	})

	It("cancels pending proposals when authority is lowered and lets a revocation win the confirmation race", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		pending, err := f.broker.Propose(ctx, editor, f.request(key, "pending", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(Succeed())
		Expect(f.stored("pending")).To(Equal(gateway.ProposalCancelled))
		_, err = f.broker.Confirm(ctx, session("editor"), pending.ID)
		Expect(err).To(HaveOccurred())
		Expect(f.rt.applyCalls()).To(BeEmpty())

		// A confirmation already inside the addon is stopped by the revocation,
		// which is acknowledged only after it ended and left an unknown outcome.
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		racing, err := f.broker.Propose(ctx, editor, f.request(key, "racing", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		entered := make(chan struct{})
		f.rt.mu.Lock()
		f.rt.holdReply = func(c context.Context) { close(entered); <-c.Done() }
		f.rt.mu.Unlock()
		result := make(chan error, 1)
		go func() {
			_, e := f.broker.Confirm(ctx, session("editor"), racing.ID)
			result <- e
		}()
		<-entered
		Expect(f.policy.Revoke(ctx, owner, key, "editor")).To(Succeed())
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(<-result, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("racing"))
		Expect(f.stored("racing")).To(Equal(gateway.ProposalOutcomeUnknown))
		f.rt.mu.Lock()
		f.rt.holdReply = nil
		f.rt.mu.Unlock()
		// The runtime did commit it; a lookup says so and nothing is replayed.
		receipt, err := f.broker.Reconcile(ctx, "racing")
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(f.stored("racing")).To(Equal(gateway.ProposalApplied))
		Expect(f.rt.applyCalls()).To(Equal([]string{"racing"}))
	})

	It("persists every transition atomically before it is reported", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		var during ProposalState
		f.broker.hooks.afterExecuting = func(id string) { during = f.stored(id) }
		var after ProposalState
		f.broker.hooks.afterApply = func(id string) { after = f.stored(id) }
		_, err := f.broker.Propose(ctx, owner, f.request(key, "ok", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("ok")).To(Equal(gateway.ProposalPending))
		_, err = f.broker.Confirm(ctx, session("owner"), "ok")
		Expect(err).NotTo(HaveOccurred())
		// The durable state was executing before the runtime saw the call, and
		// still executing when the answer came back, until it was stored.
		Expect(during).To(Equal(gateway.ProposalExecuting))
		Expect(after).To(Equal(gateway.ProposalExecuting))
		Expect(f.stored("ok")).To(Equal(gateway.ProposalApplied))
		// Every state reached through the public path is on disk and valid.
		_, err = f.broker.Propose(ctx, owner, f.request(key, "gone", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.broker.Cancel(ctx, owner, "gone")).To(Succeed())
		Expect(f.stored("gone")).To(Equal(gateway.ProposalCancelled))
		g := f.restart()
		got, err := g.broker.Get(ctx, owner, "ok")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Receipt).NotTo(BeNil())
		got, err = g.broker.Get(ctx, owner, "gone")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalCancelled))
	})

	It("does not dispatch when the executing state cannot be made durable", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		old := syncDirectory
		syncDirectory = func(*os.File) error { return errors.New("sync failed") }
		DeferCleanup(func() { syncDirectory = old })
		_, err = f.broker.Gateway().Confirm(ctx, session("owner"), p.ID, p.Digest)
		Expect(err).To(HaveOccurred())
		Expect(gateway.StatusFor(err)).To(Equal(503))
		// The owner's record says it reached executing, so the answer is an
		// unknown outcome with the original operation, not a plain outage.
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("op-1"))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		// The store accepts no further writes until it is reopened.
		_, err = f.broker.Propose(ctx, owner, f.request(key, "op-2", "write", `{"rows":[2]}`))
		Expect(err).To(HaveOccurred())
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("keeps a lost reply unknown, blocks conflicting writes and resolves it by receipt without a replay", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		other, err := f.broker.Propose(ctx, owner, f.request(key, "waiting", "write", `{"rows":[0]}`))
		Expect(err).NotTo(HaveOccurred())
		p, err := f.broker.Propose(ctx, owner, f.request(key, "lost", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.mu.Lock()
		f.rt.commitFirst = true
		f.rt.mu.Unlock()
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("lost"))
		Expect(gateway.StatusFor(err)).To(Equal(503))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalOutcomeUnknown))
		// Repeating the confirmation looks the receipt up; it is not an apply.
		receipt, err := f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(f.rt.applyCalls()).To(Equal([]string{"lost"}))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalApplied))
		_ = other

		// An apply that never reached the runtime stays unknown: a lookup alone
		// cannot prove it did not run, so nothing is sent again.
		f.rt.mu.Lock()
		f.rt.failFirst = true
		f.rt.mu.Unlock()
		q, err := f.broker.Propose(ctx, owner, f.request(key, "dropped", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.broker.Confirm(ctx, session("owner"), q.ID)
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("dropped"))
		// Writes on the resource are blocked and say which operation to resolve.
		_, err = f.broker.Propose(ctx, owner, f.request(key, "next", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		_, err = f.broker.Confirm(ctx, session("owner"), other.ID)
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		blocked, err := f.broker.Get(ctx, owner, other.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked.Blocked).To(BeTrue())
		// Reads are not blocked.
		Expect(f.policy.Authorize(ctx, owner, key, ActionRead)).To(Succeed())
		callsBefore := f.rt.applyCalls()
		for i := 0; i < 2; i++ {
			rc, err := f.broker.Reconcile(ctx, "dropped")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			Expect(f.stored("dropped")).To(Equal(gateway.ProposalOutcomeUnknown))
		}
		f.rt.mu.Lock()
		f.rt.receiptMode = "pending"
		f.rt.mu.Unlock()
		rc, err := f.broker.Reconcile(ctx, "dropped")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptPending))
		Expect(f.stored("dropped")).To(Equal(gateway.ProposalOutcomeUnknown))
		f.rt.mu.Lock()
		f.rt.receiptMode = "unavailable"
		f.rt.mu.Unlock()
		_, err = f.broker.Reconcile(ctx, "dropped")
		Expect(gateway.StatusFor(err)).To(Equal(503))
		Expect(f.rt.applyCalls()).To(Equal(callsBefore))
		// The lookup is the recovery: once it is final the block lifts.
		f.rt.mu.Lock()
		f.rt.receiptMode = ""
		f.rt.receipts["dropped"] = contract.Receipt{OperationID: "dropped", State: contract.ReceiptRejected}
		f.rt.mu.Unlock()
		rc, err = f.broker.Reconcile(ctx, "dropped")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(f.stored("dropped")).To(Equal(gateway.ProposalRejected))
		_, err = f.broker.Confirm(ctx, session("owner"), other.ID)
		Expect(err).NotTo(HaveOccurred())
	})

	It("reports a lost reply from the owner's own gateway with the original operation", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		g := &gateway.Gateway{Proposals: f.broker.Gateway(), Revalidate: f.accounts.check}
		p, err := g.Propose(ctx, owner, f.request(key, "lost", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.mu.Lock()
		f.rt.commitFirst = true
		f.rt.mu.Unlock()
		_, err = g.ConfirmProposal(ctx, session("owner"), p.ID, p.Digest)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("lost"))
		got, err := g.ReconcileProposal(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Receipt).NotTo(BeNil())
		// Without a broker the gateway is unavailable, never a grant.
		none := &gateway.Gateway{Revalidate: f.accounts.check}
		_, err = none.Propose(ctx, owner, f.request(key, "x", "write", `{}`))
		Expect(gateway.StatusFor(err)).To(Equal(503))
	})

	It("recovers a crash after dispatch by receipt and a crash before dispatch as unknown without replay", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		after, err := f.broker.Propose(ctx, owner, f.request(key, "after", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.broker.hooks.afterApply = func(string) { panic("crash after dispatch") }
		Expect(func() { _, _ = f.broker.Confirm(ctx, session("owner"), after.ID) }).To(PanicWith("crash after dispatch"))
		f.broker.hooks.afterApply = nil
		Expect(f.stored("after")).To(Equal(gateway.ProposalExecuting))
		before, err := f.broker.Propose(ctx, owner, f.request(key, "before", "write", `{"rows":[2]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		_ = before

		g := f.restart()
		Expect(g.broker.Recover(ctx)).To(Succeed())
		got, err := g.broker.Get(ctx, owner, "after")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(g.rt.applyCalls()).To(Equal([]string{"after"}))

		pb, err := g.broker.Propose(ctx, owner, g.request(key, "before", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		g.broker.hooks.afterExecuting = func(string) { panic("crash before dispatch") }
		Expect(func() { _, _ = g.broker.Confirm(ctx, session("owner"), pb.ID) }).To(PanicWith("crash before dispatch"))
		g.broker.hooks.afterExecuting = nil
		Expect(g.stored("before")).To(Equal(gateway.ProposalExecuting))

		h := g.restart()
		Expect(h.broker.Recover(ctx)).To(Succeed())
		got, err = h.broker.Get(ctx, owner, "before")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalOutcomeUnknown))
		Expect(h.stored("before")).To(Equal(gateway.ProposalOutcomeUnknown))
		_, err = h.broker.Confirm(ctx, session("owner"), "before")
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(h.rt.applyCalls()).To(Equal([]string{"after"}))
		_, err = h.broker.Propose(ctx, owner, h.request(key, "next", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
	})

	It("keeps pending proposals across a restart and never resets a corrupt store", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		g := f.restart()
		got, err := g.broker.Get(ctx, owner, "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Digest).To(Equal(p.Digest))
		_, err = g.broker.ConfirmBound(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())

		g.close()
		file := filepath.Join(g.dir, "proposals", proposalFileName)
		good, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		for name, content := range map[string][]byte{
			"garbage":  []byte("{not json"),
			"trailing": append(append([]byte(nil), good...), []byte(`{"version":1}`)...),
			"unknown":  []byte(`{"version":1,"proposals":{},"extra":1}`),
			"badstate": []byte(`{"version":1,"proposals":{"a":{"id":"a","actor":{"userId":"u","credentialId":"c","mode":"named-user"},"addonId":"x","resourceId":"r","generation":1,"action":"write","arguments":{},"digest":"d","state":"bogus"}}}`),
		} {
			Expect(os.WriteFile(file, content, 0o600)).To(Succeed(), name)
			_, err = OpenBroker(filepath.Join(g.dir, "proposals"), BrokerOptions{Runtime: g.instances})
			Expect(err).To(HaveOccurred(), name)
			after, rerr := os.ReadFile(file)
			Expect(rerr).NotTo(HaveOccurred())
			Expect(after).To(Equal(content), name)
		}
		// A second opener is refused while the owner holds the store.
		Expect(os.WriteFile(file, good, 0o600)).To(Succeed())
		h := openBrokerFixture(ModeNamedUser, g.dir, g.rt, g.instances, g.clock, g.accounts)
		DeferCleanup(func() { h.close() })
		_, err = OpenBroker(filepath.Join(g.dir, "proposals"), BrokerOptions{Runtime: g.instances})
		Expect(err).To(MatchError(ErrStoreBusy))
	})

	It("bounds the pending proposals of one user and the store as a whole", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		for i := 0; i < MaxProposalsPerUser; i++ {
			_, err := f.broker.Propose(ctx, owner, f.request(key, fmt.Sprintf("op-%d", i), "write", `{"rows":[1]}`))
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := f.broker.Propose(ctx, owner, f.request(key, "one-too-many", "write", `{"rows":[1]}`))
		Expect(err).To(MatchError(ErrQuota))
		Expect(f.broker.Cancel(ctx, owner, "op-0")).To(Succeed())
		_, err = f.broker.Propose(ctx, owner, f.request(key, "fits-again", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
	})

	It("binds the digest to the operation, actor, resource, generation, action, arguments and revision", func() {
		base := proposalRecord{ID: "op", Actor: contract.Actor{UserID: "u", CredentialID: "session:u", Mode: "named-user"}, AddonID: "a", ResourceID: "r",
			Generation: 1, Action: "write", Arguments: json.RawMessage(`{"rows":[1]}`), Revision: contract.Revision{Schema: 1, Data: 1}}
		want := digestOf(&base)
		Expect(want).NotTo(BeEmpty())
		for name, change := range map[string]func(*proposalRecord){
			"operation id":  func(r *proposalRecord) { r.ID = "op2" },
			"actor":         func(r *proposalRecord) { r.Actor.UserID = "v" },
			"mode":          func(r *proposalRecord) { r.Actor.Mode = "local-owner" },
			"addon":         func(r *proposalRecord) { r.AddonID = "b" },
			"resource":      func(r *proposalRecord) { r.ResourceID = "s" },
			"generation":    func(r *proposalRecord) { r.Generation = 2 },
			"action":        func(r *proposalRecord) { r.Action = "schema" },
			"arguments":     func(r *proposalRecord) { r.Arguments = json.RawMessage(`{"rows":[2]}`) },
			"nil arguments": func(r *proposalRecord) { r.Arguments = nil },
			"schema":        func(r *proposalRecord) { r.Revision.Schema = 2 },
			"data":          func(r *proposalRecord) { r.Revision.Data = 2 },
		} {
			changed := base
			change(&changed)
			Expect(digestOf(&changed)).NotTo(Equal(want), name)
		}
		// The proposer's credential is not part of what the human saw.
		same := base
		same.Actor.CredentialID = "key:other"
		Expect(digestOf(&same)).To(Equal(want))
	})

	It("refuses a stored proposal whose operation, actor, resource, generation or arguments changed at rest", func() {
		for name, tamper := range map[string]func(r map[string]any){
			"arguments":  func(r map[string]any) { r["arguments"] = map[string]any{"rows": []any{999}} },
			"actor":      func(r map[string]any) { r["actor"].(map[string]any)["userId"] = "mallory" },
			"resource":   func(r map[string]any) { r["resourceId"] = "second" },
			"generation": func(r map[string]any) { r["generation"] = 2 },
			"operation":  func(r map[string]any) { r["id"] = "renamed" },
		} {
			f := newBrokerFixture(ModeNamedUser)
			owner := named("owner")
			key := f.create(owner)
			second := f.create(owner)
			Expect(f.policy.SetGrant(ctx, owner, key, "mallory", RoleEditor)).To(Succeed())
			Expect(f.policy.SetGrant(ctx, owner, second, "mallory", RoleEditor)).To(Succeed())
			p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"rows":[1]}`))
			Expect(err).NotTo(HaveOccurred(), name)
			f.close()
			file := filepath.Join(f.dir, "proposals", proposalFileName)
			b, err := os.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			var doc map[string]any
			Expect(json.Unmarshal(b, &doc)).To(Succeed())
			recs := doc["proposals"].(map[string]any)
			rec := recs["op-1"].(map[string]any)
			tamper(rec)
			if name == "resource" {
				rec["resourceId"] = second.ResourceID
			}
			target := "op-1"
			if name == "operation" {
				delete(recs, "op-1")
				recs["renamed"] = rec
				target = "renamed"
			}
			b, err = json.Marshal(doc)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(file, b, 0o600)).To(Succeed())
			if name == "generation" {
				f.instances.setGeneration(2)
			}
			g := openBrokerFixture(ModeNamedUser, f.dir, f.rt, f.instances, f.clock, f.accounts)
			DeferCleanup(func() { g.close() })
			who := session("owner")
			if name == "actor" {
				who = session("mallory")
			}
			_, err = g.broker.ConfirmBound(ctx, who, target, p.Digest)
			Expect(err).To(MatchError(ErrProposalChanged), name)
			// An internal confirmation without a digest is checked the same way.
			_, err = g.broker.Confirm(ctx, who, target)
			Expect(err).To(MatchError(ErrProposalChanged), name)
			Expect(g.rt.applyCalls()).To(BeEmpty(), name)
		}
	})

	It("records expiry and collects settled proposals without any read so a quiet clock cannot fill the store", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		// Past the store bound, with every proposal expiring unread.
		rounds := MaxProposals + 100
		for i := 0; i < rounds; i++ {
			_, err := f.broker.Propose(ctx, owner, f.request(key, fmt.Sprintf("op-%d", i), "write", `{"rows":[1]}`))
			Expect(err).NotTo(HaveOccurred(), "round %d", i)
			f.clock.advance(6 * time.Minute)
		}
		Expect(f.stored(fmt.Sprintf("op-%d", rounds-2))).To(Equal(gateway.ProposalExpired))
		Expect(f.stored("op-0")).To(Equal(ProposalState("")))
		Expect(len(f.broker.store.all())).To(BeNumerically("<=", 2*int(DefaultProposalRetention/(6*time.Minute))))
		// The record outlives a restart as expired, and the user can go on.
		g := f.restart()
		Expect(g.stored(fmt.Sprintf("op-%d", rounds-1))).To(Equal(gateway.ProposalExpired))
		_, err := g.broker.Propose(ctx, owner, g.request(key, "after", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = g.broker.Confirm(ctx, session("owner"), fmt.Sprintf("op-%d", rounds-1))
		Expect(err).To(MatchError(ErrProposalExpired))
	})

	It("records expiry durably when the owner starts and when it recovers", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "unread", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("unread")).To(Equal(gateway.ProposalPending))
		f.clock.advance(contract.DefaultLimits().ProposalExpiry + time.Second)
		g := f.restart()
		Expect(g.stored("unread")).To(Equal(gateway.ProposalExpired))

		_, err = g.broker.Propose(ctx, owner, g.request(key, "second", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		g.clock.advance(contract.DefaultLimits().ProposalExpiry + time.Second)
		Expect(g.stored("second")).To(Equal(gateway.ProposalPending))
		Expect(g.broker.Recover(ctx)).To(Succeed())
		Expect(g.stored("second")).To(Equal(gateway.ProposalExpired))
	})

	It("keeps settled proposals for the retention window only", func() {
		f := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.Retention = time.Hour })
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "done", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.broker.Cancel(ctx, owner, "done")).To(Succeed())
		f.clock.advance(30 * time.Minute)
		_, err = f.broker.Propose(ctx, owner, f.request(key, "a", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("done")).To(Equal(gateway.ProposalCancelled))
		f.clock.advance(31 * time.Minute)
		_, err = f.broker.Propose(ctx, owner, f.request(key, "b", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("done")).To(Equal(ProposalState("")))
		// A live proposal is never collected, whatever its age.
		Expect(f.stored("b")).To(Equal(gateway.ProposalPending))
	})

	It("keeps one user from starving others and never drops another user's live proposal", func() {
		f := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.MaxProposals = 8; o.MaxProposalsPerUser = 4 })
		owner := named("owner")
		key := f.create(owner)
		for _, u := range []string{"b", "c"} {
			Expect(f.policy.SetGrant(ctx, owner, key, u, RoleEditor)).To(Succeed())
		}
		propose := func(who, id string) error {
			_, err := f.broker.Propose(ctx, named(who), f.request(key, id, "write", `{"rows":[1]}`))
			return err
		}
		for i := 0; i < 4; i++ {
			Expect(propose("owner", fmt.Sprintf("a-%d", i))).To(Succeed())
		}
		// The user's own share is exhausted first, with the user's own error.
		Expect(propose("owner", "a-4")).To(MatchError(ErrProposalUserQuota))
		for i := 0; i < 4; i++ {
			Expect(propose("b", fmt.Sprintf("b-%d", i))).To(Succeed())
		}
		// Full of other users' live proposals: refused, and nothing is dropped.
		Expect(propose("c", "c-0")).To(MatchError(ErrProposalStoreFull))
		Expect(propose("owner", "a-5")).To(MatchError(ErrProposalUserQuota))
		Expect(errors.Is(ErrProposalStoreFull, ErrQuota)).To(BeTrue())
		for i := 0; i < 4; i++ {
			Expect(f.stored(fmt.Sprintf("a-%d", i))).To(Equal(gateway.ProposalPending))
			Expect(f.stored(fmt.Sprintf("b-%d", i))).To(Equal(gateway.ProposalPending))
		}
		// A settled proposal makes room, and the proposer's own go first.
		Expect(f.broker.Cancel(ctx, named("b"), "b-0")).To(Succeed())
		Expect(propose("c", "c-0")).To(Succeed())
		Expect(f.stored("b-0")).To(Equal(ProposalState("")))
		// Expired ones make room too.
		f.clock.advance(contract.DefaultLimits().ProposalExpiry + time.Second)
		Expect(propose("c", "c-1")).To(Succeed())
		Expect(len(f.broker.store.all())).To(BeNumerically("<=", 8))
		Expect(f.stored("c-1")).To(Equal(gateway.ProposalPending))
	})

	It("bounds the stored canonical arguments per user and in total with a typed quota error", func() {
		const args = `{"rows":[1]}`
		n := int64(len(args))
		f := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) {
			o.MaxArgumentBytesPerUser = 2*n + 1
			o.MaxArgumentBytes = 3*n + 1
		})
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "b", RoleEditor)).To(Succeed())
		propose := func(who, id string) error {
			_, err := f.broker.Propose(ctx, named(who), f.request(key, id, "write", args))
			return err
		}
		Expect(propose("owner", "a-0")).To(Succeed())
		Expect(propose("owner", "a-1")).To(Succeed())
		Expect(propose("owner", "a-2")).To(MatchError(ErrProposalBytesQuota))
		Expect(propose("b", "b-0")).To(Succeed())
		// The store as a whole is at its bound and everything in it is live.
		err := propose("b", "b-1")
		Expect(err).To(MatchError(ErrProposalBytesQuota))
		Expect(errors.Is(err, ErrQuota)).To(BeTrue())
		Expect(gateway.StatusFor(err)).To(Equal(403))
		// A settled proposal frees its bytes.
		Expect(f.broker.Cancel(ctx, owner, "a-0")).To(Succeed())
		Expect(propose("b", "b-1")).To(Succeed())
		Expect(f.stored("a-0")).To(Equal(ProposalState("")))
		// One proposal larger than the whole bound can never be stored.
		tiny := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.MaxArgumentBytes = n - 1; o.MaxArgumentBytesPerUser = n - 1 })
		k := tiny.create(owner)
		_, err = tiny.broker.Propose(ctx, owner, tiny.request(k, "big", "write", args))
		Expect(err).To(MatchError(ErrProposalBytesQuota))
	})

	// lose leaves a confirmed proposal whose apply reported nothing.
	lose := func(f *brokerFixture, who contract.Actor, key ResourceKey, id string) Proposal {
		p, err := f.broker.Propose(ctx, who, f.request(key, id, "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.mu.Lock()
		f.rt.failFirst = true
		f.rt.mu.Unlock()
		_, err = f.broker.Confirm(ctx, session(who.UserID), id)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		return p
	}

	It("looks an unresolved receipt up again with backoff for a finite window and never applies again", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lose(f, owner, key, "lost")
		calls := f.rt.applyCalls()
		before := f.rt.lookupCount()
		f.rt.setReceiptMode("pending")
		Expect(f.broker.RetryUnresolved(ctx)).To(Equal(1))
		Expect(f.rt.lookupCount()).To(Equal(before + 1))
		// Not due yet: nothing is asked of the addon.
		Expect(f.broker.RetryUnresolved(ctx)).To(Equal(0))
		Expect(f.rt.lookupCount()).To(Equal(before + 1))
		// Each wait is longer than the last, up to the limit.
		limits := contract.DefaultLimits()
		last := f.rt.lookupCount()
		var waits []time.Duration
		for i := 0; i < 6; i++ {
			waited := time.Duration(0)
			for step := limits.BackoffInitial; f.broker.RetryUnresolved(ctx) == 0; {
				f.clock.advance(step)
				waited += step
				Expect(waited).To(BeNumerically("<=", 2*limits.BackoffMax))
			}
			waits = append(waits, waited)
			Expect(f.rt.lookupCount()).To(Equal(last + 1))
			last++
		}
		Expect(waits[len(waits)-1]).To(BeNumerically(">", waits[0]))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalOutcomeUnknown))
		// A final receipt settles it and lifts the block.
		f.rt.mu.Lock()
		f.rt.receiptMode = ""
		f.rt.receipts["lost"] = contract.Receipt{OperationID: "lost", State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 2}}
		f.rt.mu.Unlock()
		f.clock.advance(limits.BackoffMax)
		Expect(f.broker.RetryUnresolved(ctx)).To(Equal(1))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalApplied))
		Expect(f.rt.applyCalls()).To(Equal(calls))

		// Past the window the automatic lookups stop; the block stays.
		lose(f, owner, key, "stuck")
		f.clock.advance(limits.ApplyTimeout + 10*time.Minute)
		seen := f.rt.lookupCount()
		Expect(f.broker.RetryUnresolved(ctx)).To(Equal(0))
		Expect(f.rt.lookupCount()).To(Equal(seen))
		Expect(f.stored("stuck")).To(Equal(gateway.ProposalOutcomeUnknown))
		_, err := f.broker.Propose(ctx, owner, f.request(key, "next", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
	})

	It("lets only the resource owner's browser session abandon an unresolved outcome and then unblocks writes", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		waiting, err := f.broker.Propose(ctx, editor, f.request(key, "waiting", "write", `{"rows":[0]}`))
		Expect(err).NotTo(HaveOccurred())
		lost := lose(f, editor, key, "lost")
		calls := f.rt.applyCalls()
		f.rt.setReceiptMode("pending")
		_, err = f.broker.Propose(ctx, editor, f.request(key, "next", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))

		// The proposer with write authority, an installation administrator and
		// the owner's own API key cannot abandon it.
		for name, who := range map[string]contract.Actor{"editor session": session("editor"), "admin session": session("admin"), "owner key": owner} {
			_, err = f.broker.Resolve(ctx, who, "lost", lost.Digest, "not waiting any longer")
			Expect(err).To(MatchError(ErrForbidden), name)
		}
		Expect(f.stored("lost")).To(Equal(gateway.ProposalOutcomeUnknown))
		// A bound digest and a reason are required.
		_, err = f.broker.Resolve(ctx, session("owner"), "lost", "not-the-digest", "reason")
		Expect(err).To(MatchError(ErrProposalChanged))
		for _, reason := range []string{"", "   ", strings.Repeat("x", 513)} {
			_, err = f.broker.Resolve(ctx, session("owner"), "lost", lost.Digest, reason)
			Expect(err).To(MatchError(ErrForbidden))
		}
		_, err = f.broker.Resolve(ctx, session("owner"), "unknown-id", lost.Digest, "reason")
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalOutcomeUnknown))

		got, err := f.broker.Resolve(ctx, session("owner"), "lost", lost.Digest, "the addon was reinstalled")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalAbandoned))
		Expect(got.Resolution).NotTo(BeNil())
		Expect(got.Resolution.ActorID).To(Equal("owner"))
		Expect(got.Resolution.Reason).To(Equal("the addon was reinstalled"))
		Expect(got.Resolution.At).To(BeTemporally("~", f.clock.now(), time.Second))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalAbandoned))
		Expect(f.rt.applyCalls()).To(Equal(calls))

		// Writes are possible again, for the proposer and for others.
		_, err = f.broker.ConfirmBound(ctx, session("editor"), waiting.ID, waiting.Digest)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.broker.Propose(ctx, editor, f.request(key, "next", "write", `{"rows":[3]}`))
		Expect(err).NotTo(HaveOccurred())
		// Repeating the acknowledgement changes nothing and applies nothing.
		again, err := f.broker.Resolve(ctx, session("owner"), "lost", lost.Digest, "second try")
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(gateway.ProposalAbandoned))
		Expect(again.Resolution.Reason).To(Equal("the addon was reinstalled"))
		// Confirming it again is a lookup, not an apply.
		_, err = f.broker.Confirm(ctx, session("editor"), "lost")
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(f.rt.applyCalls()).To(Equal([]string{"lost", "waiting"}))

		// The acknowledgement survives a restart, still unblocking writes.
		g := f.restart()
		Expect(g.stored("lost")).To(Equal(gateway.ProposalAbandoned))
		Expect(g.broker.Recover(ctx)).To(Succeed())
		_, err = g.broker.Propose(ctx, editor, g.request(key, "after-restart", "write", `{"rows":[4]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(g.rt.applyCalls()).To(Equal([]string{"lost", "waiting"}))
	})

	It("records a late receipt of an abandoned outcome instead of hiding it", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lost := lose(f, owner, key, "lost")
		f.rt.setReceiptMode("pending")
		_, err := f.broker.Resolve(ctx, session("owner"), "lost", lost.Digest, "waited long enough")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("lost")).To(Equal(gateway.ProposalAbandoned))
		calls := f.rt.applyCalls()
		// Still no receipt: the proposal stays acknowledged and unresolved.
		rc, err := f.broker.Reconcile(ctx, "lost")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptPending))
		Expect(f.stored("lost")).To(Equal(gateway.ProposalAbandoned))
		// The addon reports that it did apply it after all.
		f.rt.mu.Lock()
		f.rt.receiptMode = ""
		f.rt.receipts["lost"] = contract.Receipt{OperationID: "lost", State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 2}}
		f.rt.mu.Unlock()
		rc, err = f.broker.Reconcile(ctx, "lost")
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		got, err := f.broker.Get(ctx, owner, "lost")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Receipt).NotTo(BeNil())
		Expect(got.Resolution).NotTo(BeNil())
		Expect(f.stored("lost")).To(Equal(gateway.ProposalApplied))
		Expect(f.rt.applyCalls()).To(Equal(calls))
		// A rejected receipt is recorded the same way.
		other := lose(f, owner, key, "other")
		_, err = f.broker.Resolve(ctx, session("owner"), "other", other.Digest, "gave up")
		Expect(err).NotTo(HaveOccurred())
		f.rt.mu.Lock()
		f.rt.receipts["other"] = contract.Receipt{OperationID: "other", State: contract.ReceiptRejected}
		f.rt.mu.Unlock()
		f.clock.advance(time.Minute)
		Expect(f.broker.RetryUnresolved(ctx)).To(BeNumerically(">=", 1))
		Expect(f.stored("other")).To(Equal(gateway.ProposalRejected))
	})

	It("settles by lookup instead of abandoning when the receipt is final", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lost := lose(f, owner, key, "lost")
		f.rt.mu.Lock()
		f.rt.receipts["lost"] = contract.Receipt{OperationID: "lost", State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 2}}
		f.rt.mu.Unlock()
		got, err := f.broker.Resolve(ctx, session("owner"), "lost", lost.Digest, "checking")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Resolution).To(BeNil())
		// Only an unresolved outcome can be abandoned.
		fresh, err := f.broker.Propose(ctx, owner, f.request(key, "fresh", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.broker.Resolve(ctx, session("owner"), "fresh", fresh.Digest, "reason")
		Expect(err).To(MatchError(ErrProposalClosed))
		Expect(f.stored("fresh")).To(Equal(gateway.ProposalPending))
	})

	It("refuses a stored abandonment that names nobody and keeps looking up in the background", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lose(f, owner, key, "lost")
		f.rt.setReceiptMode("pending")
		// The ticker is injected: a tick is accepted only after the previous
		// round finished, so no wait or sleep decides the outcome.
		tick := make(chan time.Time)
		f.broker.tickC = tick
		ictx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		before := f.rt.lookupCount()
		go func() { f.broker.Run(ictx); close(done) }()
		tick <- time.Now()
		tick <- time.Now()
		Expect(f.rt.lookupCount()).To(BeNumerically(">", before))
		cancel()
		<-done

		f.close()
		file := filepath.Join(f.dir, "proposals", proposalFileName)
		b, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(b, &doc)).To(Succeed())
		rec := doc["proposals"].(map[string]any)["lost"].(map[string]any)
		rec["state"] = "abandoned-unknown"
		b, _ = json.Marshal(doc)
		Expect(os.WriteFile(file, b, 0o600)).To(Succeed())
		_, err = OpenBroker(filepath.Join(f.dir, "proposals"), BrokerOptions{Runtime: f.instances})
		Expect(err).To(HaveOccurred())
	})

	It("keeps executing, outcome-unknown and abandoned records far past retention and still dedupes them", func() {
		f := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.Retention = time.Hour })
		owner := named("owner")
		spare := f.create(owner)
		keys := map[string]ResourceKey{}
		for _, id := range []string{"executing", "unknown", "abandoned"} {
			keys[id] = f.create(owner)
		}
		// executing: confirmed and handed over, with no process running it.
		_, err := f.broker.Propose(ctx, owner, f.request(keys["executing"], "executing", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.broker.store.transition("executing", []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
			x.State = gateway.ProposalExecuting
			x.UnknownSince = f.clock.now()
		})
		Expect(err).NotTo(HaveOccurred())
		lose(f, owner, keys["unknown"], "unknown")
		gone := lose(f, owner, keys["abandoned"], "abandoned")
		f.rt.setReceiptMode("pending")
		_, err = f.broker.Resolve(ctx, session("owner"), "abandoned", gone.Digest, "waited long enough")
		Expect(err).NotTo(HaveOccurred())

		want := map[string]ProposalState{"executing": gateway.ProposalExecuting, "unknown": gateway.ProposalOutcomeUnknown, "abandoned": gateway.ProposalAbandoned}
		// Past retention, but inside the longer window of an abandoned outcome.
		f.clock.advance(100 * time.Hour)
		Expect(f.broker.Sweep()).To(Succeed())
		for id, state := range want {
			Expect(f.stored(id)).To(Equal(state), id)
		}
		// An insertion sweeps and makes room as well; none of them may go.
		for i := 0; i < 3; i++ {
			_, err = f.broker.Propose(ctx, owner, f.request(spare, fmt.Sprintf("filler-%d", i), "write", `{"rows":[9]}`))
			Expect(err).NotTo(HaveOccurred())
		}
		for id, state := range want {
			Expect(f.stored(id)).To(Equal(state), id)
		}
		// The operation ID still names the stored operation: the same request
		// returns it, another one under that ID is a denial, and nothing runs.
		calls := f.rt.applyCalls()
		for id, state := range want {
			got, err := f.broker.Propose(ctx, owner, f.request(keys[id], id, "write", `{"rows":[1]}`))
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(got.State).To(Equal(state), id)
			_, err = f.broker.Propose(ctx, owner, f.request(keys[id], id, "write", `{"rows":[2]}`))
			Expect(err).To(MatchError(ErrForbidden), id)
		}
		Expect(f.rt.applyCalls()).To(Equal(calls))
		// Only the abandoned record has a finite life, and it is much longer.
		f.clock.advance(AbandonedRetention)
		Expect(f.broker.Sweep()).To(Succeed())
		Expect(f.stored("abandoned")).To(Equal(ProposalState("")))
		Expect(f.stored("executing")).To(Equal(gateway.ProposalExecuting))
		Expect(f.stored("unknown")).To(Equal(gateway.ProposalOutcomeUnknown))
	})

	It("waits for the background run and for lookups in flight before it closes, and writes nothing after", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lose(f, owner, key, "lost")
		f.rt.setReceiptMode("block")
		tick := make(chan time.Time)
		f.broker.tickC = tick
		done := make(chan struct{})
		go func() { f.broker.Run(ctx); close(done) }()
		tick <- time.Now()
		// The lookup is inside the runtime now.
		<-f.rt.entered
		file := filepath.Join(f.dir, "proposals", proposalFileName)
		Expect(f.broker.Close()).To(Succeed())
		// The lookup in flight was cut short and has come back by now, and
		// the run then ends: it is not left behind a closed store.
		select {
		case <-f.rt.returned:
		default:
			Fail("Close returned while a lookup was still in flight")
		}
		<-done
		// The round the close interrupted has finished its bookkeeping by now;
		// from here on the file must not change.
		before, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		// Nothing is served or written once it is closed.
		_, err = f.broker.Reconcile(ctx, "lost")
		Expect(gateway.StatusFor(err)).To(Equal(503))
		Expect(f.broker.Sweep()).NotTo(Succeed())
		_, err = f.broker.Propose(ctx, owner, f.request(key, "late", "write", `{"rows":[5]}`))
		Expect(err).To(HaveOccurred())
		again, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(again)).To(Equal(string(before)))
	})

	It("lets the owner role list the unresolved proposals of its resource and abandon one by the listed digest", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		// The editor's confirmation reply is lost; the owner cannot read it by ID.
		lost := lose(f, editor, key, "lost")
		_, err := f.broker.Get(ctx, owner, "lost")
		Expect(err).To(MatchError(ErrForbidden))
		f.rt.setReceiptMode("pending")
		f.clock.advance(90 * time.Second)

		list, err := f.broker.Unresolved(ctx, session("owner"), key.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		got := list[0]
		Expect(got.ID).To(Equal("lost"))
		Expect(got.State).To(Equal(gateway.ProposalOutcomeUnknown))
		Expect(got.ProposerID).To(Equal("editor"))
		Expect(got.Action).To(Equal("write"))
		Expect(got.ResourceID).To(Equal(key.ResourceID))
		Expect(got.Digest).To(Equal(lost.Digest))
		Expect(got.Effects.Count).To(BeEquivalentTo(3))
		Expect(got.ExpiresAt).To(BeTemporally("==", lost.ExpiresAt))
		Expect(got.AgeSeconds).To(BeNumerically(">=", 90))
		// The proposed rows are not part of the owner's view.
		raw, err := json.Marshal(list)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(`"rows":[1]`))
		Expect(string(raw)).NotTo(ContainSubstring("arguments"))
		Expect(string(raw)).NotTo(ContainSubstring("credential"))

		// Only the owner role of this resource, from a human surface, may list.
		other := f.create(editor)
		for name, who := range map[string]contract.Actor{"editor session": session("editor"), "admin session": session("admin"), "owner key": owner, "editor key": editor} {
			_, err = f.broker.Unresolved(ctx, who, key.ResourceID)
			Expect(err).To(MatchError(ErrForbidden), name)
		}
		_, err = f.broker.Unresolved(ctx, session("owner"), other.ResourceID)
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.Unresolved(ctx, session("owner"), "no-such-resource")
		Expect(err).To(MatchError(ErrForbidden))
		// A resource with nothing unresolved lists nothing.
		empty, err := f.broker.Unresolved(ctx, session("editor"), other.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(empty).To(BeEmpty())

		// The owner abandons it with the digest it was listed with.
		done, err := f.broker.Resolve(ctx, session("owner"), got.ID, got.Digest, "the addon was reset")
		Expect(err).NotTo(HaveOccurred())
		Expect(done.State).To(Equal(gateway.ProposalAbandoned))
		after, err := f.broker.Unresolved(ctx, session("owner"), key.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveLen(1))
		Expect(after[0].State).To(Equal(gateway.ProposalAbandoned))
		Expect(after[0].Resolution).NotTo(BeNil())
		Expect(after[0].Resolution.ActorID).To(Equal("owner"))
		// Settled proposals are not listed.
		f.rt.setReceiptMode("")
		f.rt.mu.Lock()
		f.rt.receipts["lost"] = contract.Receipt{OperationID: "lost", State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 2}}
		f.rt.mu.Unlock()
		_, err = f.broker.Reconcile(ctx, "lost")
		Expect(err).NotTo(HaveOccurred())
		settled, err := f.broker.Unresolved(ctx, session("owner"), key.ResourceID)
		Expect(err).NotTo(HaveOccurred())
		Expect(settled).To(BeEmpty())
	})

	It("names the blocking proposal in the denial only to the resource owner", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		waiting, err := f.broker.Propose(ctx, owner, f.request(key, "waiting", "write", `{"rows":[0]}`))
		Expect(err).NotTo(HaveOccurred())
		lose(f, editor, key, "lost")

		_, err = f.broker.Propose(ctx, editor, f.request(key, "by-editor", "write", `{"rows":[3]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		var blocked *gateway.OutcomeBlocked
		Expect(errors.As(err, &blocked)).To(BeFalse())
		Expect(gateway.StatusFor(err)).To(Equal(403))

		_, err = f.broker.Propose(ctx, owner, f.request(key, "by-owner", "write", `{"rows":[4]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		Expect(errors.As(err, &blocked)).To(BeTrue())
		Expect(blocked.BlockedBy).To(Equal("lost"))
		Expect(gateway.StatusFor(err)).To(Equal(403))

		// The same holds for a confirmation that finds the resource blocked.
		_, err = f.broker.Confirm(ctx, session("owner"), waiting.ID)
		Expect(err).To(MatchError(ErrOutcomeBlocked))
		blocked = nil
		Expect(errors.As(err, &blocked)).To(BeTrue())
		Expect(blocked.BlockedBy).To(Equal("lost"))
	})

	It("does not show another user's blocking operation in a proposal view", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		pre, err := f.broker.Propose(ctx, editor, f.request(key, "pre-editor", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		lose(f, owner, key, "lost")

		got, err := f.broker.Get(ctx, session("editor"), pre.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Blocked).To(BeTrue())
		raw, err := json.Marshal(got)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("lost"))
	})

	It("refuses an unbound digest when the owner abandons through the gateway or the broker", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		lost := lose(f, owner, key, "lost")
		f.rt.setReceiptMode("pending")
		calls := f.rt.applyCalls()
		for _, digest := range []string{"", "not-the-digest"} {
			_, err := f.broker.Gateway().Resolve(ctx, session("owner"), "lost", digest, "reason")
			Expect(err).To(MatchError(ErrProposalChanged), digest)
			_, err = f.broker.Resolve(ctx, session("owner"), "lost", digest, "reason")
			Expect(err).To(MatchError(ErrProposalChanged), digest)
			Expect(f.stored("lost")).To(Equal(gateway.ProposalOutcomeUnknown))
		}
		got, err := f.broker.Gateway().Resolve(ctx, session("owner"), "lost", lost.Digest, "reason")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalAbandoned))
		Expect(f.rt.applyCalls()).To(Equal(calls))
	})

	It("answers the same typed outage for a resolution whoever proposed the operation", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner, editor := named("owner"), named("editor")
		key := f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
		own := lose(f, owner, key, "own")
		f.rt.setReceiptMode("pending")
		// The frontend's own account check passes; the owner's store is down.
		g := &gateway.Gateway{Proposals: f.broker.Gateway(), Revalidate: func(context.Context, contract.Actor) error { return nil }}
		f.accounts.setDown(true)
		_, err := g.ResolveProposal(ctx, session("owner"), "own", own.Digest, "reason")
		var unavailable *gateway.ProposalUnavailable
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unavailable)).To(BeTrue())
		Expect(errors.As(err, &unknown)).To(BeFalse())
		Expect(unavailable.ProposalID).To(Equal("own"))
		f.accounts.setDown(false)
		Expect(f.stored("own")).To(Equal(gateway.ProposalOutcomeUnknown))
		done, err := f.broker.Gateway().Resolve(ctx, session("owner"), "own", own.Digest, "reason")
		Expect(err).NotTo(HaveOccurred())
		Expect(done.State).To(Equal(gateway.ProposalAbandoned))

		// Another proposer's operation gets the same type.
		theirs := lose(f, editor, key, "theirs")
		f.accounts.setDown(true)
		_, err = g.ResolveProposal(ctx, session("owner"), "theirs", theirs.Digest, "reason")
		unavailable, unknown = nil, nil
		Expect(errors.As(err, &unavailable)).To(BeTrue())
		Expect(errors.As(err, &unknown)).To(BeFalse())
		Expect(unavailable.ProposalID).To(Equal("theirs"))
	})

	It("returns the applied proposal when the owner cannot read it back after a successful apply", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "done", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		// The account store goes down right after the addon committed.
		f.rt.mu.Lock()
		f.rt.afterCommit = func() { f.accounts.setDown(true) }
		f.rt.mu.Unlock()
		got, err := f.broker.Gateway().Confirm(ctx, session("owner"), "done", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		Expect(got.Receipt).NotTo(BeNil())
		Expect(f.stored("done")).To(Equal(gateway.ProposalApplied))
		Expect(f.rt.applyCalls()).To(Equal([]string{"done"}))
	})

	It("issues resource identifiers the envelope contract accepts", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		for i := 0; i < 40; i++ {
			key := f.create(owner)
			env := contract.Envelope{
				Request:   contract.Request{AddonID: "fixture", ResourceID: key.ResourceID, OperationID: "op", Generation: 1, Action: "write", Arguments: json.RawMessage(`{}`)},
				Authority: contract.Authority{Actor: owner, Scope: "preview", ExpiresAt: time.Now().Add(time.Minute)},
			}
			Expect(env.Validate(time.Now(), contract.DefaultLimits())).To(Succeed(), key.ResourceID)
		}
	})

	It("is unavailable until it is attached to a policy and a runtime", func() {
		dir := GinkgoT().TempDir()
		b, err := OpenBroker(filepath.Join(dir, "proposals"), BrokerOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })
		_, err = b.Propose(ctx, named("owner"), contract.Request{})
		Expect(err).To(MatchError(ErrUnavailable))
		_, err = b.Confirm(ctx, session("owner"), "x")
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(b.InvalidateProposals(ctx, ResourceKey{AddonID: "a", ResourceID: "r"}, "u")).To(Succeed())
	})
})
