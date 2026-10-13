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
	holdReply   func(ctx context.Context)
	receiptMode string // "", "pending", "unavailable"
}

func newFixtureRuntime() *fixtureRuntime {
	return &fixtureRuntime{data: 1, receipts: map[string]contract.Receipt{}, seenArgs: map[string]string{}}
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
		ExpiresAt:   time.Now().Add(time.Hour),
		Summary:     json.RawMessage(`{"count":3,"sample":[{"row":1},{"row":2}]}`),
	}, nil
}

func (r *fixtureRuntime) Apply(ctx context.Context, e contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	r.mu.Lock()
	id := e.Request.OperationID
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
	return receipt, nil
}

func (r *fixtureRuntime) Receipt(_ context.Context, id string) (contract.Receipt, error) {
	r.mu.Lock()
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
}

// openBrokerFixture opens the policy store and the broker in dir, as a
// restarted owner does.
func openBrokerFixture(mode Mode, dir string, rt *fixtureRuntime, instances *fixtureInstances, c *clock, accounts *account) *brokerFixture {
	store, err := OpenStore(filepath.Join(dir, "policy"), mode)
	Expect(err).NotTo(HaveOccurred())
	broker, err := OpenBroker(filepath.Join(dir, "proposals"), BrokerOptions{Runtime: instances, Now: c.now})
	Expect(err).NotTo(HaveOccurred())
	p := New(store, Options{
		Revalidate:      accounts.check,
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		Proposals:       broker,
		DrainTimeout:    2 * time.Second,
	})
	broker.Attach(p)
	f := &brokerFixture{dir: dir, mode: mode, store: store, broker: broker, policy: p, accounts: accounts, rt: rt, instances: instances, clock: c}
	return f
}

func newBrokerFixture(mode Mode) *brokerFixture {
	rt := newFixtureRuntime()
	f := openBrokerFixture(mode, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: rt},
		&clock{t: time.Now()}, &account{blocked: map[string]bool{}})
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
	g := openBrokerFixture(f.mode, f.dir, f.rt, f.instances, f.clock, f.accounts)
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
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(HaveOccurred())
		Expect(gateway.StatusFor(err)).To(Equal(503))
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
		Expect(blocked.BlockedBy).To(Equal("dropped"))
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
