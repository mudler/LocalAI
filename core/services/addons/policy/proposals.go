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
	// MaxProposalsPerUser bounds the live proposals one user holds: pending
	// ones that have not expired and unresolved ones.
	MaxProposalsPerUser = 32
	// MaxProposals bounds all stored proposals. Settled ones are dropped
	// oldest first, the inserting user's own first. A live proposal of
	// another user is never dropped to make room.
	MaxProposals = 1024
	// MaxProposalArgumentBytes bounds the canonical arguments of all stored
	// proposals together, and MaxProposalArgumentBytesPerUser those of the
	// live proposals of one user.
	MaxProposalArgumentBytes        = 64 << 20
	MaxProposalArgumentBytesPerUser = 16 << 20
	// DefaultProposalRetention is how long a settled proposal is kept, so a
	// repeated request or confirmation still gets its stored answer.
	DefaultProposalRetention = 24 * time.Hour
	// AbandonedRetention is how long an abandoned outcome is kept, so a late
	// receipt can still be recorded. It is never dropped to make room.
	AbandonedRetention = 30 * 24 * time.Hour
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
	// ErrProposalUserQuota means the user already holds the most live
	// proposals allowed. It is checked before any store-wide limit, so one
	// user can only exhaust their own share.
	ErrProposalUserQuota = fmt.Errorf("addon proposal limit of the user reached: %w", ErrQuota)
	// ErrProposalStoreFull means the store holds as many live proposals of
	// other users as it may. Nothing live is dropped to make room.
	ErrProposalStoreFull = fmt.Errorf("addon proposal store is full of live proposals: %w", ErrQuota)
	// ErrProposalBytesQuota means the stored canonical arguments would exceed
	// the byte bound of the user or of the store.
	ErrProposalBytesQuota = fmt.Errorf("addon proposal storage bound reached: %w", ErrQuota)
	// ErrOutcomeBlocked means another operation on the resource has an
	// unresolved outcome. Resolve it by receipt lookup first.
	ErrOutcomeBlocked = fmt.Errorf("addon operation outcome unresolved: %w", gateway.ErrForbidden)
)

// proposalRecord is the durable form. The proposer's credential stays here and
// never reaches the public view.
type proposalRecord struct {
	ID         string                  `json:"id"`
	Actor      contract.Actor          `json:"actor"`
	AddonID    string                  `json:"addonId"`
	ResourceID string                  `json:"resourceId"`
	Generation uint64                  `json:"generation"`
	Action     string                  `json:"action"`
	Arguments  json.RawMessage         `json:"arguments"`
	Revision   contract.Revision       `json:"revision"`
	Digest     string                  `json:"digest"`
	Effects    gateway.ProposalEffects `json:"effects"`
	State      ProposalState           `json:"state"`
	Reason     string                  `json:"reason,omitempty"`
	CreatedAt  time.Time               `json:"createdAt"`
	// UpdatedAt is when the state last changed. Retention counts from it.
	UpdatedAt time.Time         `json:"updatedAt,omitempty"`
	ExpiresAt time.Time         `json:"expiresAt"`
	Receipt   *contract.Receipt `json:"receipt,omitempty"`
	// UnknownSince is when the apply was handed to the addon. The automatic
	// receipt lookups run for a finite window counted from it.
	UnknownSince time.Time `json:"unknownSince,omitempty"`
	// Lookups and NextLookup drive the backoff of the automatic lookups.
	Lookups    int       `json:"lookups,omitempty"`
	NextLookup time.Time `json:"nextLookup,omitempty"`
	// Resolution is set once the resource owner stopped waiting.
	Resolution  *gateway.ProposalResolution `json:"resolution,omitempty"`
	Reconciling bool                        `json:"-"`
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
	if r.Resolution != nil {
		c := *r.Resolution
		n.Resolution = &c
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
		gateway.ProposalApplied, gateway.ProposalRejected, gateway.ProposalOutcomeUnknown, gateway.ProposalAbandoned:
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
			ParseAction(r.Action) == ActionUnknown || (r.State == gateway.ProposalApplied && (r.Receipt == nil || r.Receipt.State != contract.ReceiptCommitted)) ||
			(r.State == gateway.ProposalAbandoned && (r.Resolution == nil || r.Resolution.ActorID == "" || r.Resolution.Reason == "")) {
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
	now           func() time.Time
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
	return &proposalStore{dir: dir, lock: lock, doc: d, invalidations: map[string]uint64{}, now: time.Now}, nil
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
		r.UpdatedAt = s.now()
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
	// Retention is how long a settled proposal is kept. The zero value
	// selects DefaultProposalRetention; retention can never be disabled.
	Retention time.Duration
	// MaxProposals, MaxProposalsPerUser, MaxArgumentBytes and
	// MaxArgumentBytesPerUser lower the store bounds. Zero or a value above
	// the package default selects the default; a bound is never disabled.
	MaxProposals            int
	MaxProposalsPerUser     int
	MaxArgumentBytes        int64
	MaxArgumentBytesPerUser int64
	// LookupInterval is how often Run asks for the receipts of unresolved
	// proposals that are due. The zero value is five seconds.
	LookupInterval time.Duration
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
	// done ends Run when the broker is closed.
	done      chan struct{}
	closeOnce sync.Once
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
	if opts.Retention <= 0 {
		opts.Retention = DefaultProposalRetention
	}
	if opts.LookupInterval <= 0 {
		opts.LookupInterval = 5 * time.Second
	}
	if opts.MaxProposals <= 0 || opts.MaxProposals > MaxProposals {
		opts.MaxProposals = MaxProposals
	}
	if opts.MaxProposalsPerUser <= 0 || opts.MaxProposalsPerUser > MaxProposalsPerUser {
		opts.MaxProposalsPerUser = MaxProposalsPerUser
	}
	if opts.MaxArgumentBytes <= 0 || opts.MaxArgumentBytes > MaxProposalArgumentBytes {
		opts.MaxArgumentBytes = MaxProposalArgumentBytes
	}
	if opts.MaxArgumentBytesPerUser <= 0 || opts.MaxArgumentBytesPerUser > MaxProposalArgumentBytesPerUser {
		opts.MaxArgumentBytesPerUser = MaxProposalArgumentBytesPerUser
	}
	s, err := openProposalStore(dir)
	if err != nil {
		return nil, err
	}
	s.now = opts.Now
	b := &Broker{store: s, opts: opts, live: map[string]struct{}{}, done: make(chan struct{})}
	// Expiry is recorded when the owner starts, not only when a proposal is
	// read. A failed write is not fatal: expiry also holds by the clock.
	_ = b.Sweep()
	return b, nil
}

// Attach binds the policy the broker admits through. It must be called once,
// before the policy serves requests.
func (b *Broker) Attach(p *Policy) {
	b.mu.Lock()
	b.policy = p
	b.mu.Unlock()
}

// Close releases the durable state.
func (b *Broker) Close() error {
	b.closeOnce.Do(func() { close(b.done) })
	return b.store.Close()
}

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
				r.UpdatedAt = s.now()
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
		CreatedAt: r.CreatedAt, Receipt: r.Receipt, Reason: r.Reason, Resolution: r.Resolution}
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

// live reports a proposal that holds a share of its user's quota: a pending
// one that has not expired, and any unresolved one.
func (r *proposalRecord) live(now time.Time) bool {
	return (r.State == gateway.ProposalPending && now.Before(r.ExpiresAt)) || r.unresolved()
}

// settledState reports a state that ends the proposal's life.
func settledState(s ProposalState) bool {
	switch s {
	case gateway.ProposalCancelled, gateway.ProposalExpired, gateway.ProposalApplied, gateway.ProposalRejected:
		return true
	}
	return false
}

// sweep records expiry and drops what is past retention. It reports whether
// the document changed. A pending proposal is expired by the clock whether or
// not anyone read it, so an unread proposal cannot hold quota or space.
func (b *Broker) sweep(d *proposalDocument, now time.Time) bool {
	changed := false
	for id, r := range d.Proposals {
		if r.State == gateway.ProposalPending && !now.Before(r.ExpiresAt) {
			r.State = gateway.ProposalExpired
			r.Reason = "expired"
			r.UpdatedAt = now
			changed = true
		}
		if r.State == gateway.ProposalAbandoned && r.Resolution != nil && now.Sub(r.Resolution.At) >= AbandonedRetention {
			delete(d.Proposals, id)
			changed = true
		}
		if settledState(r.State) {
			since := r.UpdatedAt
			if since.IsZero() {
				since = r.CreatedAt
			}
			if now.Sub(since) >= b.opts.Retention {
				delete(d.Proposals, id)
				changed = true
			}
		}
	}
	return changed
}

var errNothingToSweep = errors.New("nothing to sweep")

// Sweep makes expiry and retention durable now. The owner calls it on start;
// every insertion does the same, so quota never depends on reads.
func (b *Broker) Sweep() error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.mutateLocked(func(d *proposalDocument) error {
		if !b.sweep(d, b.opts.Now()) {
			return errNothingToSweep
		}
		return nil
	})
	if errors.Is(err, errNothingToSweep) {
		return nil
	}
	return err
}

// insert stores a new pending proposal unless a revocation of the proposer's
// authority ran meanwhile or a quota is reached. The user's own share is
// checked first; the store-wide bounds then make room only by dropping
// settled proposals, the inserting user's own first, never a live one.
func (b *Broker) insert(rec *proposalRecord, inv uint64, user string, now time.Time) error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.invalidations[invalidationKey(rec.ResourceID, user)] != inv {
		return ErrForbidden
	}
	// Record expiry and retention first so they hold even when the insertion
	// is refused below.
	_ = s.mutateLocked(func(d *proposalDocument) error {
		if !b.sweep(d, now) {
			return errNothingToSweep
		}
		return nil
	})
	return s.mutateLocked(func(d *proposalDocument) error {
		b.sweep(d, now)
		if _, dup := d.Proposals[rec.ID]; dup {
			return ErrForbidden
		}
		size := int64(len(rec.Arguments))
		liveCount, liveBytes := 0, int64(0)
		for _, r := range d.Proposals {
			if r.Actor.UserID == user && r.live(now) {
				liveCount++
				liveBytes += int64(len(r.Arguments))
			}
		}
		if liveCount >= b.opts.MaxProposalsPerUser {
			return ErrProposalUserQuota
		}
		if liveBytes+size > b.opts.MaxArgumentBytesPerUser {
			return ErrProposalBytesQuota
		}
		if err := b.makeRoom(d, user, size); err != nil {
			return err
		}
		d.Proposals[rec.ID] = rec.copy()
		return nil
	})
}

// makeRoom drops settled proposals until one more record of size bytes fits.
// It fails with a typed quota error when only live proposals remain.
func (b *Broker) makeRoom(d *proposalDocument, user string, size int64) error {
	total := int64(0)
	for _, r := range d.Proposals {
		total += int64(len(r.Arguments))
	}
	var settled []*proposalRecord
	for _, r := range d.Proposals {
		if settledState(r.State) {
			settled = append(settled, r)
		}
	}
	// The inserting user's own records go first, then the oldest.
	sort.Slice(settled, func(i, j int) bool {
		a, c := settled[i], settled[j]
		if (a.Actor.UserID == user) != (c.Actor.UserID == user) {
			return a.Actor.UserID == user
		}
		return a.CreatedAt.Before(c.CreatedAt)
	})
	for len(d.Proposals) >= b.opts.MaxProposals || total+size > b.opts.MaxArgumentBytes {
		if len(settled) == 0 {
			if total+size > b.opts.MaxArgumentBytes {
				return ErrProposalBytesQuota
			}
			return ErrProposalStoreFull
		}
		total -= int64(len(settled[0].Arguments))
		delete(d.Proposals, settled[0].ID)
		settled = settled[1:]
	}
	return nil
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
	case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown, gateway.ProposalAbandoned:
		return b.resolveForConfirm(ctx, r)
	case gateway.ProposalExpired:
		return contract.Receipt{}, ErrProposalExpired
	case gateway.ProposalPending:
	default:
		// Cancelled, and any state added later, never runs.
		return contract.Receipt{}, ErrProposalClosed
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
		x.UnknownSince = b.opts.Now()
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
	_, _ = b.store.transition(id, []ProposalState{gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown, gateway.ProposalAbandoned}, func(x *proposalRecord) {
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

// byState decides what an outage of a state-changing call means by the
// owner's own record of the proposal: one that reached the addon (executing
// or outcome-unknown) is an unknown outcome with the original operation ID,
// whatever error the call ended with. Anything else is left to the caller as
// a plain outage. Only the proposer's own record is consulted.
func (g gatewayBroker) byState(actor contract.Actor, id string, err error) error {
	var unknown *gateway.OutcomeUnknown
	if err == nil || errors.As(err, &unknown) || gateway.StatusFor(err) != 503 {
		return err
	}
	if r, ok := g.b.store.get(id); ok && r.Actor.UserID == actor.UserID && r.Actor.Mode == actor.Mode && r.unresolved() {
		return &gateway.OutcomeUnknown{OperationID: id}
	}
	return err
}

func (g gatewayBroker) Propose(ctx context.Context, a contract.Actor, r contract.Request) (Proposal, error) {
	return g.b.Propose(ctx, a, r)
}

func (g gatewayBroker) Confirm(ctx context.Context, a contract.Actor, id, digest string) (Proposal, error) {
	if digest == "" {
		// A confirmation names what the human saw; an unbound one is refused.
		return Proposal{}, ErrProposalChanged
	}
	if _, err := g.b.ConfirmBound(ctx, a, id, digest); err != nil {
		return Proposal{}, g.byState(a, id, err)
	}
	p, err := g.b.Get(ctx, a, id)
	return p, g.byState(a, id, err)
}

func (g gatewayBroker) Cancel(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	r, err := g.b.cancel(ctx, a, id)
	if err != nil {
		return Proposal{}, g.byState(a, id, err)
	}
	return g.b.view(r), nil
}

func (g gatewayBroker) Get(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.b.Get(ctx, a, id)
}

func (g gatewayBroker) Resolve(ctx context.Context, a contract.Actor, id, digest, reason string) (Proposal, error) {
	p, err := g.b.Resolve(ctx, a, id, digest, reason)
	return p, g.byState(a, id, err)
}

func (g gatewayBroker) Reconcile(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.b.reconcileFor(ctx, a, id)
}
