// SPDX-License-Identifier: MIT
package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Proposal is the public view of a stored proposal.
type Proposal = gateway.Proposal

// ProposalState is the lifecycle state of a stored proposal.
type ProposalState = gateway.ProposalState

const (
	proposalFileName = "proposals.json"
	// MaxProposalsPerUser bounds the pending proposals one user holds.
	MaxProposalsPerUser = 32
	// MaxProposals bounds all stored proposals. Settled ones are dropped
	// oldest first; unresolved ones are never dropped to make room.
	MaxProposals = 1024
	// maxEffectSample bounds the sample shown to the confirming human.
	maxEffectSample = 16 << 10
)

var (
	// ErrProposalClosed means the proposal is no longer pending. It is the
	// single-use guard: a settled, cancelled or expired proposal never runs.
	ErrProposalClosed = fmt.Errorf("addon proposal is not pending: %w", gateway.ErrForbidden)
	// ErrProposalExpired means the proposal reached its expiry unconfirmed.
	ErrProposalExpired = fmt.Errorf("addon proposal expired: %w", gateway.ErrForbidden)
	// ErrProposalStale means the addon generation or revisions moved since the
	// preview, so what the human saw is no longer what would run.
	ErrProposalStale = fmt.Errorf("addon proposal is stale: %w", gateway.ErrForbidden)
	// ErrProposalChanged means the confirmation does not name the stored
	// operation exactly.
	ErrProposalChanged = fmt.Errorf("addon proposal digest does not match: %w", gateway.ErrForbidden)
	// ErrOutcomeBlocked means another operation on the resource has an
	// unresolved outcome. Resolve it by receipt lookup first.
	ErrOutcomeBlocked = fmt.Errorf("addon operation outcome unresolved: %w", gateway.ErrForbidden)
)

// proposalRecord is the durable form. The proposer's credential stays here and
// never reaches the public view.
type proposalRecord struct {
	ID          string                  `json:"id"`
	Actor       contract.Actor          `json:"actor"`
	AddonID     string                  `json:"addonId"`
	ResourceID  string                  `json:"resourceId"`
	Generation  uint64                  `json:"generation"`
	Action      string                  `json:"action"`
	Arguments   json.RawMessage         `json:"arguments"`
	Revision    contract.Revision       `json:"revision"`
	Digest      string                  `json:"digest"`
	Effects     gateway.ProposalEffects `json:"effects"`
	State       ProposalState           `json:"state"`
	Reason      string                  `json:"reason,omitempty"`
	CreatedAt   time.Time               `json:"createdAt"`
	ExpiresAt   time.Time               `json:"expiresAt"`
	Receipt     *contract.Receipt       `json:"receipt,omitempty"`
	Reconciling bool                    `json:"-"`
}

func (r *proposalRecord) copy() *proposalRecord {
	n := *r
	n.Arguments = append(json.RawMessage(nil), r.Arguments...)
	n.Effects.Sample = append(json.RawMessage(nil), r.Effects.Sample...)
	if r.Receipt != nil {
		c := *r.Receipt
		c.Result.Payload = append(json.RawMessage(nil), r.Receipt.Result.Payload...)
		n.Receipt = &c
	}
	return &n
}

// unresolved reports a state that blocks conflicting writes on the resource.
func (r *proposalRecord) unresolved() bool {
	return r.State == gateway.ProposalExecuting || r.State == gateway.ProposalOutcomeUnknown
}

type proposalDocument struct {
	Version   int                        `json:"version"`
	Proposals map[string]*proposalRecord `json:"proposals"`
}

func (d *proposalDocument) clone() *proposalDocument {
	c := &proposalDocument{Version: d.Version, Proposals: make(map[string]*proposalRecord, len(d.Proposals))}
	for id, r := range d.Proposals {
		c.Proposals[id] = r.copy()
	}
	return c
}

func validState(s ProposalState) bool {
	switch s {
	case gateway.ProposalPending, gateway.ProposalCancelled, gateway.ProposalExpired, gateway.ProposalExecuting,
		gateway.ProposalApplied, gateway.ProposalRejected, gateway.ProposalOutcomeUnknown:
		return true
	}
	return false
}

func loadProposals(dir string) (*proposalDocument, error) {
	corrupt := errors.New("addon proposal store is corrupt")
	b, err := os.ReadFile(filepath.Join(dir, proposalFileName))
	if errors.Is(err, os.ErrNotExist) {
		return &proposalDocument{Version: 1, Proposals: map[string]*proposalRecord{}}, nil
	}
	if err != nil {
		return nil, err
	}
	d := &proposalDocument{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(d); err != nil || d.Version != 1 {
		return nil, corrupt
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, corrupt
	}
	if d.Proposals == nil {
		d.Proposals = map[string]*proposalRecord{}
	}
	for id, r := range d.Proposals {
		var args map[string]json.RawMessage
		if r == nil || r.ID != id || id == "" || r.Actor.UserID == "" || r.Actor.CredentialID == "" || r.AddonID == "" || r.ResourceID == "" ||
			r.Generation == 0 || !validState(r.State) || r.Digest == "" || json.Unmarshal(r.Arguments, &args) != nil || args == nil ||
			ParseAction(r.Action) == ActionUnknown || (r.State == gateway.ProposalApplied && (r.Receipt == nil || r.Receipt.State != contract.ReceiptCommitted)) {
			return nil, corrupt
		}
		r.Reconciling = false
	}
	return d, nil
}

// proposalStore is the durable proposal state, one process at a time. It uses
// the policy store's patterns: copy, atomic rename, fsync, validation on load,
// and no reset of a corrupt file.
type proposalStore struct {
	dir  string
	lock *os.File
	mu   sync.Mutex
	doc  *proposalDocument
	// failed is set when a change was renamed into place but could not be made
	// durable. Memory then holds it and no further write is accepted.
	failed bool
	// invalidations counts revocation sweeps per resource and user. A proposal
	// made under admission that was invalidated meanwhile is not stored.
	invalidations map[string]uint64
}

func openProposalStore(dir string) (*proposalStore, error) {
	if dir == "" {
		return nil, ErrMigrationRequired
	}
	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	cleanTemps(dir)
	d, err := loadProposals(dir)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return &proposalStore{dir: dir, lock: lock, doc: d, invalidations: map[string]uint64{}}, nil
}

func (s *proposalStore) get(id string) (proposalRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.doc.Proposals[id]
	if !ok {
		return proposalRecord{}, false
	}
	return *r.copy(), true
}

func (s *proposalStore) all() []proposalRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proposalRecord, 0, len(s.doc.Proposals))
	for _, r := range s.doc.Proposals {
		out = append(out, *r.copy())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// mutate applies fn to a copy and publishes it only when durable.
func (s *proposalStore) mutate(fn func(*proposalDocument) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutateLocked(fn)
}

func (s *proposalStore) mutateLocked(fn func(*proposalDocument) error) error {
	if s.failed {
		return ErrUnavailable
	}
	next := s.doc.clone()
	if err := fn(next); err != nil {
		return err
	}
	b, err := json.Marshal(next)
	if err != nil {
		return ErrUnavailable
	}
	renamed, err := durableWrite(filepath.Join(s.dir, proposalFileName), b)
	if err != nil {
		if renamed {
			s.doc = next
			s.failed = true
		}
		return ErrUnavailable
	}
	s.doc = next
	return nil
}

func invalidationKey(resource, user string) string { return resource + "\x00" + user }

func (s *proposalStore) invalidationCount(resource, user string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invalidations[invalidationKey(resource, user)]
}

// transition moves a proposal from one of the allowed states, atomically with
// the durable write. It reports the stored record after the change.
func (s *proposalStore) transition(id string, from []ProposalState, fn func(*proposalRecord)) (proposalRecord, error) {
	var out proposalRecord
	err := s.mutate(func(d *proposalDocument) error {
		r := d.Proposals[id]
		if r == nil {
			return ErrForbidden
		}
		ok := false
		for _, f := range from {
			ok = ok || r.State == f
		}
		if !ok {
			return ErrProposalClosed
		}
		fn(r)
		out = *r.copy()
		return nil
	})
	return out, err
}

// Close releases the store.
func (s *proposalStore) Close() error { return s.lock.Close() }

// BrokerOptions are the host-supplied dependencies of a Broker.
type BrokerOptions struct {
	// Runtime resolves the current ready runtime of an addon. Without it the
	// broker is unavailable.
	Runtime gateway.CurrentRuntime
	// Limits bound sizes and timeouts. The zero value selects the defaults.
	Limits contract.Limits
	// Now is the clock. The zero value is time.Now.
	Now func() time.Time
	// ConfirmCredential says which credentials stand for a human at a
	// host-owned confirmation surface. The default accepts a browser session
	// in named-user mode and the local owner in local-owner mode, never an API
	// key.
	ConfirmCredential func(contract.Actor) bool
}

// Broker is the owner-held proposal state machine. A data or schema mutation
// is stored first, shown to a human with its exact effects, and executed only
// by an explicit confirmation of that stored operation.
type Broker struct {
	store  *proposalStore
	opts   BrokerOptions
	mu     sync.Mutex
	policy *Policy
	// live holds proposals whose Apply this process is running. A receipt
	// lookup must not call such a proposal unknown.
	live map[string]struct{}
	// hooks let tests stop the broker at a named point, as a crash would.
	hooks brokerHooks
}

type brokerHooks struct {
	// afterExecuting runs once the executing state is durable, before dispatch.
	afterExecuting func(id string)
	// afterApply runs once the runtime answered, before the answer is stored.
	afterApply func(id string)
}

var _ ProposalInvalidator = (*Broker)(nil)

// OpenBroker opens the durable proposal state in dir. Attach it to the policy
// before serving; it is unavailable until then.
func OpenBroker(dir string, opts BrokerOptions) (*Broker, error) {
	if opts.Limits == (contract.Limits{}) {
		opts.Limits = contract.DefaultLimits()
	}
	if err := opts.Limits.Validate(); err != nil {
		return nil, ErrMigrationRequired
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s, err := openProposalStore(dir)
	if err != nil {
		return nil, err
	}
	return &Broker{store: s, opts: opts, live: map[string]struct{}{}}, nil
}

// Attach binds the policy the broker admits through. It must be called once,
// before the policy serves requests.
func (b *Broker) Attach(p *Policy) {
	b.mu.Lock()
	b.policy = p
	b.mu.Unlock()
}

// Close releases the durable state.
func (b *Broker) Close() error { return b.store.Close() }

// Gateway adapts the broker to the gateway's ProposalBroker.
func (b *Broker) Gateway() gateway.ProposalBroker { return gatewayBroker{b} }

func (b *Broker) attached() (*Policy, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.policy == nil || b.opts.Runtime == nil {
		return nil, ErrUnavailable
	}
	return b.policy, nil
}

func (b *Broker) confirmCredential(p *Policy, a contract.Actor) bool {
	if b.opts.ConfirmCredential != nil {
		return b.opts.ConfirmCredential(a)
	}
	if p.store.Mode() == ModeLocalOwner {
		return a.CredentialID == LocalOwnerID
	}
	return strings.HasPrefix(a.CredentialID, "session:") && len(a.CredentialID) > len("session:")
}

// InvalidateProposals cancels the pending proposals a user made on a resource.
// It returns only once that is durable; a failure leaves the revocation
// unacknowledged. A proposal that is already executing is drained by policy.
func (b *Broker) InvalidateProposals(_ context.Context, resource ResourceKey, userID string) error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidations[invalidationKey(resource.ResourceID, userID)]++
	return s.mutateLocked(func(d *proposalDocument) error {
		for _, r := range d.Proposals {
			if r.State == gateway.ProposalPending && r.ResourceID == resource.ResourceID && r.AddonID == resource.AddonID && r.Actor.UserID == userID {
				r.State = gateway.ProposalCancelled
				r.Reason = "authority lowered"
			}
		}
		return nil
	})
}

// canonicalArguments compacts the arguments so the stored and the previewed
// bytes are one value, and requires an object within the payload bound.
func (b *Broker) canonicalArguments(raw json.RawMessage) (json.RawMessage, error) {
	var args map[string]json.RawMessage
	var out bytes.Buffer
	if json.Unmarshal(raw, &args) != nil || args == nil || json.Compact(&out, raw) != nil || int64(out.Len()) > b.opts.Limits.PayloadBytes {
		return nil, ErrForbidden
	}
	return out.Bytes(), nil
}

// digestOf binds a confirmation to everything the human saw and to the
// operation that would run: its identifier, the proposing user, the resource,
// the addon generation, the action, the canonical arguments and the revision.
// The proposer's credential is not part of it, since it is not displayed.
func digestOf(r *proposalRecord) string {
	b, _ := json.Marshal(struct {
		Operation  string            `json:"operation"`
		UserID     string            `json:"user"`
		Mode       string            `json:"mode"`
		AddonID    string            `json:"addon"`
		ResourceID string            `json:"resource"`
		Generation uint64            `json:"generation"`
		Action     string            `json:"action"`
		Arguments  json.RawMessage   `json:"arguments"`
		Revision   contract.Revision `json:"revision"`
	}{r.ID, r.Actor.UserID, r.Actor.Mode, r.AddonID, r.ResourceID, r.Generation, r.Action, r.Arguments, r.Revision})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// effects extracts the exact count and a bounded sample from the preview
// summary. An addon that has no separate sample gets the whole summary.
func effects(summary json.RawMessage) (gateway.ProposalEffects, error) {
	var shape struct {
		Count  *uint64         `json:"count"`
		Sample json.RawMessage `json:"sample"`
	}
	out := gateway.ProposalEffects{Sample: summary}
	if json.Unmarshal(summary, &shape) == nil {
		if shape.Count != nil {
			out.Count = *shape.Count
		}
		if len(shape.Sample) > 0 {
			out.Sample = shape.Sample
		}
	}
	if len(out.Sample) > maxEffectSample {
		return out, ErrUnavailable
	}
	return out, nil
}

func (b *Broker) view(r proposalRecord) Proposal {
	p := Proposal{ID: r.ID, OperationID: r.ID, AddonID: r.AddonID, ResourceID: r.ResourceID, Generation: r.Generation, Action: r.Action,
		Arguments: r.Arguments, Revision: r.Revision, Digest: r.Digest, Effects: r.Effects, State: r.State, ExpiresAt: r.ExpiresAt,
		CreatedAt: r.CreatedAt, Receipt: r.Receipt, Reason: r.Reason}
	if r.State == gateway.ProposalPending {
		p.BlockedBy = b.blockedBy(r.ResourceID, r.ID)
	}
	return p
}

// blockedBy names an operation on the resource whose outcome is unresolved.
func (b *Broker) blockedBy(resourceID, except string) string {
	for _, r := range b.store.all() {
		if r.ResourceID == resourceID && r.ID != except && r.unresolved() {
			return r.ID
		}
	}
	return ""
}

// runtime resolves the addon's current instance, which must have a client.
func (b *Broker) runtime(ctx context.Context, addonID string) (addonruntime.Instance, error) {
	instance, err := b.opts.Runtime.Runtime(ctx, addonID)
	if err != nil {
		if ctx.Err() != nil {
			return instance, ctx.Err()
		}
		return instance, ErrUnavailable
	}
	if instance.Client == nil {
		return instance, ErrUnavailable
	}
	return instance, nil
}

// Propose stores a data or schema mutation as a pending proposal after a
// preview. It never executes. The request's operation ID becomes the proposal
// ID: it is unique, and repeating the same request returns the same proposal.
// Sharing and deletion are never proposals.
func (b *Broker) Propose(ctx context.Context, actor contract.Actor, request contract.Request) (Proposal, error) {
	p, err := b.attached()
	if err != nil {
		return Proposal{}, err
	}
	action := ParseAction(request.Action)
	if action != ActionWrite && action != ActionSchema {
		return Proposal{}, ErrForbidden
	}
	args, err := b.canonicalArguments(request.Arguments)
	if err != nil {
		return Proposal{}, err
	}
	request.Arguments = args
	now := b.opts.Now()
	probe := contract.Envelope{Request: request, Authority: contract.Authority{Actor: actor, Scope: "preview", ExpiresAt: now.Add(b.opts.Limits.PreviewTimeout)}}
	if probe.Validate(now, b.opts.Limits) != nil || request.ResourceID == "" {
		return Proposal{}, ErrForbidden
	}
	key, err := p.Resolve(ctx, actor, request)
	if err != nil {
		return Proposal{}, err
	}
	request.ResourceID = key.ResourceID
	var out Proposal
	err = p.Admit(ctx, actor, key, action, func(rctx context.Context) error {
		inv := b.store.invalidationCount(key.ResourceID, actor.UserID)
		if existing, ok := b.store.get(request.OperationID); ok {
			// The same operation again returns the stored proposal, so a lost
			// reply is recoverable. Anything else under that ID is a denial.
			if sameRequest(existing, actor, key, request, action) {
				out = b.view(existing)
				return nil
			}
			return ErrForbidden
		}
		if blocker := b.blockedBy(key.ResourceID, ""); blocker != "" {
			return ErrOutcomeBlocked
		}
		instance, e := b.runtime(rctx, key.AddonID)
		if e != nil {
			return e
		}
		if instance.Generation != request.Generation {
			return ErrProposalStale
		}
		env := contract.Envelope{Request: request, Authority: contract.Authority{Actor: actor, Scope: "preview", ExpiresAt: b.opts.Now().Add(b.opts.Limits.PreviewTimeout)}}
		preview, e := instance.Client.Preview(rctx, env)
		if e != nil {
			if rctx.Err() != nil {
				return rctx.Err()
			}
			return ErrUnavailable
		}
		eff, e := effects(preview.Summary)
		if e != nil {
			return e
		}
		created := b.opts.Now()
		expires := created.Add(b.opts.Limits.ProposalExpiry)
		if preview.ExpiresAt.Before(expires) {
			expires = preview.ExpiresAt
		}
		if !expires.After(created) {
			return ErrUnavailable
		}
		rec := &proposalRecord{ID: request.OperationID, Actor: actor, AddonID: key.AddonID, ResourceID: key.ResourceID, Generation: request.Generation,
			Action: string(action), Arguments: args, Revision: preview.Revision, Effects: eff, State: gateway.ProposalPending, CreatedAt: created, ExpiresAt: expires}
		rec.Digest = digestOf(rec)
		if rctx.Err() != nil {
			return ErrForbidden
		}
		e = b.insert(rec, inv, actor.UserID, created)
		if e == nil {
			out = b.view(*rec)
		}
		return e
	})
	return out, err
}

func sameRequest(r proposalRecord, actor contract.Actor, key ResourceKey, request contract.Request, action Action) bool {
	return r.Actor.UserID == actor.UserID && r.Actor.Mode == actor.Mode && r.AddonID == key.AddonID && r.ResourceID == key.ResourceID &&
		r.Generation == request.Generation && r.Action == string(action) && bytes.Equal(r.Arguments, request.Arguments)
}

// insert stores a new pending proposal unless a revocation of the proposer's
// authority ran meanwhile or a quota is reached.
func (b *Broker) insert(rec *proposalRecord, inv uint64, user string, now time.Time) error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidations[invalidationKey(rec.ResourceID, user)] != inv {
		return ErrForbidden
	}
	return s.mutateLocked(func(d *proposalDocument) error {
		if _, dup := d.Proposals[rec.ID]; dup {
			return ErrForbidden
		}
		pending := 0
		for _, r := range d.Proposals {
			if r.State == gateway.ProposalPending && r.Actor.UserID == user && r.ExpiresAt.After(now) {
				pending++
			}
		}
		if pending >= MaxProposalsPerUser {
			return ErrQuota
		}
		if len(d.Proposals) >= MaxProposals && !pruneSettled(d, len(d.Proposals)-MaxProposals+1) {
			return ErrQuota
		}
		d.Proposals[rec.ID] = rec.copy()
		return nil
	})
}

// pruneSettled drops the n oldest settled proposals. It reports false when
// fewer than n are settled: an unresolved or pending one is never dropped.
func pruneSettled(d *proposalDocument, n int) bool {
	var settled []*proposalRecord
	for _, r := range d.Proposals {
		switch r.State {
		case gateway.ProposalCancelled, gateway.ProposalExpired, gateway.ProposalApplied, gateway.ProposalRejected:
			settled = append(settled, r)
		}
	}
	if len(settled) < n {
		return false
	}
	sort.Slice(settled, func(i, j int) bool { return settled[i].CreatedAt.Before(settled[j].CreatedAt) })
	for _, r := range settled[:n] {
		delete(d.Proposals, r.ID)
	}
	return true
}

// own returns the actor's stored proposal. A missing proposal and another
// user's proposal are the same denial.
func (b *Broker) own(actor contract.Actor, id string) (proposalRecord, error) {
	r, ok := b.store.get(id)
	if !ok || r.Actor.UserID != actor.UserID || r.Actor.Mode != actor.Mode || id == "" {
		return proposalRecord{}, ErrForbidden
	}
	return r, nil
}

// expire records the expiry of a pending proposal that ran out of time.
func (b *Broker) expire(r proposalRecord) (proposalRecord, bool) {
	if r.State != gateway.ProposalPending || b.opts.Now().Before(r.ExpiresAt) {
		return r, false
	}
	n, err := b.store.transition(r.ID, []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
		x.State = gateway.ProposalExpired
		x.Reason = "expired"
	})
	if err != nil {
		// The expiry still holds even if it could not be written down.
		r.State = gateway.ProposalExpired
		return r, true
	}
	return n, true
}

// Get returns the actor's proposal.
func (b *Broker) Get(ctx context.Context, actor contract.Actor, id string) (Proposal, error) {
	p, err := b.attached()
	if err != nil {
		return Proposal{}, err
	}
	if err = p.current(ctx, actor); err != nil {
		return Proposal{}, err
	}
	r, err := b.own(actor, id)
	if err != nil {
		return Proposal{}, err
	}
	r, _ = b.expire(r)
	return b.view(r), nil
}

// Cancel withdraws a pending proposal. It needs only a current identity: a
// user whose write authority was lowered can still withdraw.
func (b *Broker) Cancel(ctx context.Context, actor contract.Actor, id string) error {
	_, err := b.cancel(ctx, actor, id)
	return err
}

func (b *Broker) cancel(ctx context.Context, actor contract.Actor, id string) (proposalRecord, error) {
	p, err := b.attached()
	if err != nil {
		return proposalRecord{}, err
	}
	if err = p.current(ctx, actor); err != nil {
		return proposalRecord{}, err
	}
	r, err := b.own(actor, id)
	if err != nil {
		return proposalRecord{}, err
	}
	if r.State == gateway.ProposalCancelled {
		return r, nil
	}
	return b.store.transition(id, []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
		x.State = gateway.ProposalCancelled
		x.Reason = "cancelled"
	})
}

// Confirm applies the stored proposal once. It is the only way a stored
// mutation reaches the addon, and it uses exactly the stored parameters.
func (b *Broker) Confirm(ctx context.Context, actor contract.Actor, proposalID string) (contract.Receipt, error) {
	return b.ConfirmBound(ctx, actor, proposalID, "")
}

// ConfirmBound is Confirm for a confirmation that names the digest the human
// saw. An empty digest skips the comparison and is for internal callers.
func (b *Broker) ConfirmBound(ctx context.Context, actor contract.Actor, proposalID, digest string) (contract.Receipt, error) {
	p, err := b.attached()
	if err != nil {
		return contract.Receipt{}, err
	}
	if err = p.current(ctx, actor); err != nil {
		return contract.Receipt{}, err
	}
	// A tool, an agent or an API key is a valid account but not a human at a
	// confirmation surface: it can propose and never approve.
	if !b.confirmCredential(p, actor) {
		return contract.Receipt{}, ErrForbidden
	}
	r, err := b.own(actor, proposalID)
	if err != nil {
		return contract.Receipt{}, err
	}
	// What is stored must still be what was shown: a record changed at rest
	// never runs, whatever digest the caller names.
	if digestOf(&r) != r.Digest || (digest != "" && digest != r.Digest) {
		return contract.Receipt{}, ErrProposalChanged
	}
	switch r.State {
	case gateway.ProposalApplied, gateway.ProposalRejected:
		// Single use: a repeat gets the stored answer and executes nothing.
		if r.Receipt != nil {
			return *r.Receipt, nil
		}
		return contract.Receipt{}, ErrProposalClosed
	case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown:
		return b.resolveForConfirm(ctx, r)
	case gateway.ProposalCancelled:
		return contract.Receipt{}, ErrProposalClosed
	case gateway.ProposalExpired:
		return contract.Receipt{}, ErrProposalExpired
	}
	if _, expired := b.expire(r); expired {
		return contract.Receipt{}, ErrProposalExpired
	}
	key := ResourceKey{AddonID: r.AddonID, ResourceID: r.ResourceID}
	var receipt contract.Receipt
	var outcome error
	err = p.Admit(ctx, actor, key, ParseAction(r.Action), func(rctx context.Context) error {
		receipt, outcome = b.execute(rctx, actor, r.ID)
		return outcome
	})
	if err != nil {
		return contract.Receipt{}, err
	}
	return receipt, nil
}

// resolveForConfirm answers a repeated confirmation of an unresolved proposal
// by receipt lookup. It never applies again.
func (b *Broker) resolveForConfirm(ctx context.Context, r proposalRecord) (contract.Receipt, error) {
	receipt, err := b.Reconcile(ctx, r.ID)
	if err == nil && (receipt.State == contract.ReceiptCommitted || receipt.State == contract.ReceiptRejected) {
		return receipt, nil
	}
	return contract.Receipt{}, &gateway.OutcomeUnknown{OperationID: r.ID}
}

// execute runs under admission. The executing state is durable before the
// runtime sees the operation, so a crash always leaves a proposal that
// reconciliation can find.
func (b *Broker) execute(ctx context.Context, actor contract.Actor, id string) (contract.Receipt, error) {
	r, ok := b.store.get(id)
	if !ok || r.State != gateway.ProposalPending {
		return contract.Receipt{}, ErrProposalClosed
	}
	if digestOf(&r) != r.Digest {
		return contract.Receipt{}, ErrProposalChanged
	}
	if _, expired := b.expire(r); expired {
		return contract.Receipt{}, ErrProposalExpired
	}
	if blocker := b.blockedBy(r.ResourceID, r.ID); blocker != "" {
		return contract.Receipt{}, ErrOutcomeBlocked
	}
	instance, err := b.runtime(ctx, r.AddonID)
	if err != nil {
		return contract.Receipt{}, err
	}
	if instance.Generation != r.Generation {
		_, _ = b.store.transition(id, []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
			x.State = gateway.ProposalRejected
			x.Reason = "addon generation changed"
		})
		return contract.Receipt{}, ErrProposalStale
	}
	if ctx.Err() != nil {
		return contract.Receipt{}, ErrForbidden
	}
	if _, err = b.store.transition(id, []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
		x.State = gateway.ProposalExecuting
		x.Reason = ""
	}); err != nil {
		return contract.Receipt{}, err
	}
	b.mu.Lock()
	b.live[id] = struct{}{}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.live, id); b.mu.Unlock() }()
	if b.hooks.afterExecuting != nil {
		b.hooks.afterExecuting(id)
	}
	if ctx.Err() != nil {
		// Nothing was sent, so this is a definite cancellation.
		_, _ = b.store.transition(id, []ProposalState{gateway.ProposalExecuting}, func(x *proposalRecord) {
			x.State = gateway.ProposalCancelled
			x.Reason = "authority lowered before dispatch"
		})
		return contract.Receipt{}, ErrForbidden
	}
	env := contract.Envelope{
		Request:   contract.Request{AddonID: r.AddonID, ResourceID: r.ResourceID, OperationID: r.ID, Generation: r.Generation, Action: r.Action, Arguments: r.Arguments},
		Authority: contract.Authority{Actor: actor, Scope: "apply", ExpiresAt: b.opts.Now().Add(b.opts.Limits.ApplyTimeout)},
	}
	receipt, err := instance.Client.Apply(ctx, env, r.Revision)
	if b.hooks.afterApply != nil {
		b.hooks.afterApply(id)
	}
	unknown := &gateway.OutcomeUnknown{OperationID: id}
	if err != nil {
		b.settle(id, gateway.ProposalOutcomeUnknown, nil, "apply did not report")
		return contract.Receipt{}, unknown
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		b.settle(id, gateway.ProposalApplied, &receipt, "")
		return receipt, nil
	case contract.ReceiptRejected:
		b.settle(id, gateway.ProposalRejected, &receipt, "rejected by the addon")
		return receipt, nil
	}
	b.settle(id, gateway.ProposalOutcomeUnknown, nil, "apply is not settled")
	return contract.Receipt{}, unknown
}

// settle records the end state of an executed proposal. A failed write leaves
// the durable executing state, which reconciliation resolves later.
func (b *Broker) settle(id string, state ProposalState, receipt *contract.Receipt, reason string) {
	_, _ = b.store.transition(id, []ProposalState{gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown}, func(x *proposalRecord) {
		x.State = state
		x.Reason = reason
		if receipt != nil {
			c := *receipt
			x.Receipt = &c
		}
	})
}

// gatewayBroker adapts Broker to gateway.ProposalBroker.
type gatewayBroker struct{ b *Broker }

func (g gatewayBroker) Propose(ctx context.Context, a contract.Actor, r contract.Request) (Proposal, error) {
	return g.b.Propose(ctx, a, r)
}

func (g gatewayBroker) Confirm(ctx context.Context, a contract.Actor, id, digest string) (Proposal, error) {
	if digest == "" {
		// A confirmation names what the human saw; an unbound one is refused.
		return Proposal{}, ErrProposalChanged
	}
	if _, err := g.b.ConfirmBound(ctx, a, id, digest); err != nil {
		return Proposal{}, err
	}
	return g.b.Get(ctx, a, id)
}

func (g gatewayBroker) Cancel(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	r, err := g.b.cancel(ctx, a, id)
	if err != nil {
		return Proposal{}, err
	}
	return g.b.view(r), nil
}

func (g gatewayBroker) Get(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.b.Get(ctx, a, id)
}

func (g gatewayBroker) Reconcile(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.b.reconcileFor(ctx, a, id)
}
