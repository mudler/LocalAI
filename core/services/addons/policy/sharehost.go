// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Bounds of the leases a ShareHost keeps.
const (
	// DefaultShareWatch is how long one watch of a lease waits.
	DefaultShareWatch = 15 * time.Second
	// DefaultShareGrace is how long a lease lives with no watch in flight.
	// A frontend that died silently loses its admission at most
	// DefaultShareWatch + DefaultShareGrace after its last watch began.
	DefaultShareGrace = 10 * time.Second
	// MaxShareLeases bounds the leases held at once. The policy's own limit
	// on resources keeps one lease per running piece of work well below it.
	MaxShareLeases = 8192
	// MaxShareLeasesPerUser bounds the leases of one person, so that one
	// person who holds many admissions cannot fill MaxShareLeases and make
	// every other admission fail. 16 people at their cap fill the total.
	MaxShareLeasesPerUser = 512
)

// shareLeasesPerUser is MaxShareLeasesPerUser; a test lowers it.
var shareLeasesPerUser = MaxShareLeasesPerUser

// ErrShareLeaseQuota means the person holds as many admissions on other
// frontends as one person may. It is a denial; the person's running work
// must end first.
var ErrShareLeaseQuota = fmt.Errorf("too many running admissions of one person: %w", ErrQuota)

// ShareHost is the owner's half of the lookup, creation and admission of the
// resources of the kinds that are not add-ons, for the services that run
// their work on another process (gateway.ShareAuthority). An admission is
// AdmitRef held open under a lease: a revocation or a lowering cancels it and
// waits, as for work in this process, until the frontend released the lease
// or the lease expired.
//
// The leases are per-instance state of the owner, which is the only process
// that holds the policy. An owner restart drops them; every frontend's next
// watch then answers lost and the work stops.
type ShareHost struct {
	p *Policy
	// Wait is how long one watch waits. Zero selects DefaultShareWatch.
	Wait time.Duration
	// Grace is how long a lease lives with no watch in flight. Zero selects
	// DefaultShareGrace.
	Grace  time.Duration
	ctx    context.Context
	mu     sync.Mutex
	leases map[string]*shareLease
	// perUser counts the leases of each person in leases.
	perUser map[string]int
}

var _ gateway.ShareAuthority = (*ShareHost)(nil)

// NewShareHost serves the leases of p. The admissions end with ctx, the life
// of the owner.
func NewShareHost(ctx context.Context, p *Policy) *ShareHost {
	return &ShareHost{p: p, ctx: ctx, leases: map[string]*shareLease{}, perUser: map[string]int{}}
}

type shareLease struct {
	user string
	// lost is closed when the policy cancelled the admission.
	lost     chan struct{}
	lostOnce sync.Once
	// end is closed on release and on expiry; the admission then ends.
	end      chan struct{}
	endOnce  sync.Once
	watchers int
	expiry   *time.Timer
}

func (l *shareLease) close() { l.endOnce.Do(func() { close(l.end) }) }

func (h *ShareHost) wait() time.Duration {
	if h.Wait > 0 {
		return h.Wait
	}
	return DefaultShareWatch
}

func (h *ShareHost) grace() time.Duration {
	if h.Grace > 0 {
		return h.Grace
	}
	return DefaultShareGrace
}

// kind checks that sharing runs and that the kind is one the host serves:
// a registered kind that is not an add-on.
func (h *ShareHost) kind(kind string) error {
	if h.p.Mode() != ModeNamedUser {
		return gateway.ErrSharingDisabled
	}
	if !ValidKind(kind) {
		return ErrForbidden
	}
	if _, ok := h.p.Shareable(kind); !ok {
		return ErrForbidden
	}
	return nil
}

// ShareViews is ListingKind.
func (h *ShareHost) ShareViews(ctx context.Context, actor contract.Actor, kind string) (gateway.ShareViews, error) {
	if err := h.kind(kind); err != nil {
		return gateway.ShareViews{}, err
	}
	views, err := h.p.ListingKind(ctx, actor, kind)
	if err != nil {
		return gateway.ShareViews{}, err
	}
	out := gateway.ShareViews{Kind: kind, Views: make([]gateway.ShareView, 0, len(views))}
	for _, v := range views {
		out.Views = append(out.Views, gateway.ShareView{ID: v.Ref.ResourceID, Role: string(v.Role), Everyone: v.Everyone, OwnerID: v.OwnerID, Shared: v.Shared, Local: v.Local})
	}
	return out, nil
}

// CreateShare is CreateRef.
func (h *ShareHost) CreateShare(ctx context.Context, actor contract.Actor, kind string) (gateway.ShareRef, error) {
	if err := h.kind(kind); err != nil {
		return gateway.ShareRef{}, err
	}
	ref, err := h.p.CreateRef(ctx, actor, kind)
	if err != nil {
		return gateway.ShareRef{}, err
	}
	return gateway.ShareRef{ResourceID: ref.ResourceID}, nil
}

// EnsureShare is EnsureRef.
func (h *ShareHost) EnsureShare(ctx context.Context, actor contract.Actor, kind, local string) (gateway.ShareRef, error) {
	if err := h.kind(kind); err != nil {
		return gateway.ShareRef{}, err
	}
	ref, err := h.p.EnsureRef(ctx, actor, kind, local)
	if err != nil {
		return gateway.ShareRef{}, err
	}
	return gateway.ShareRef{ResourceID: ref.ResourceID}, nil
}

// ForgetShare is ForgetRef.
func (h *ShareHost) ForgetShare(ctx context.Context, actor contract.Actor, kind, resourceID string) error {
	if err := h.kind(kind); err != nil {
		return err
	}
	if resourceID == "" || len(resourceID) > 256 {
		return ErrForbidden
	}
	return h.p.ForgetRef(ctx, actor, Ref{Kind: kind, ResourceID: resourceID})
}

// AdmitShare admits the actor and holds the admission under a new lease. The
// first watch must begin within the grace, or the lease expires.
func (h *ShareHost) AdmitShare(ctx context.Context, actor contract.Actor, kind, resourceID, action string) (gateway.ShareLease, error) {
	if err := h.kind(kind); err != nil {
		return gateway.ShareLease{}, err
	}
	if resourceID == "" || len(resourceID) > 256 {
		return gateway.ShareLease{}, ErrForbidden
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return gateway.ShareLease{}, ErrUnavailable
	}
	id := hex.EncodeToString(raw)
	l := &shareLease{user: actor.UserID, lost: make(chan struct{}), end: make(chan struct{})}
	started := make(chan error, 1)
	result := make(chan error, 1)
	go func() {
		err := h.p.AdmitRef(h.ctx, actor, Ref{Kind: kind, ResourceID: resourceID}, ParseAction(action), func(run context.Context) error {
			if err := h.add(id, l); err != nil {
				started <- err
				return nil
			}
			started <- nil
			select {
			case <-l.end:
			case <-run.Done():
				l.lostOnce.Do(func() { close(l.lost) })
				// The frontend stops the work and then releases the lease;
				// the admission ends only then, or when the lease expires.
				<-l.end
			}
			return nil
		})
		h.remove(id)
		result <- err
	}()
	var err error
	select {
	case err = <-started:
	case err = <-result:
	case <-ctx.Done():
		// The admission may still begin; nobody watches it, so its lease
		// expires.
		return gateway.ShareLease{}, ErrUnavailable
	}
	if err != nil {
		return gateway.ShareLease{}, err
	}
	return gateway.ShareLease{Lease: id, WatchMillis: h.wait().Milliseconds()}, nil
}

func (h *ShareHost) add(id string, l *shareLease) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// The cap of one person comes first: a person at it is refused even
	// while the host has room, and so cannot take the room of the others.
	if h.perUser[l.user] >= shareLeasesPerUser {
		return ErrShareLeaseQuota
	}
	if len(h.leases) >= MaxShareLeases {
		return ErrUnavailable
	}
	l.expiry = time.AfterFunc(h.grace(), l.close)
	h.leases[id] = l
	h.perUser[l.user]++
	return nil
}

func (h *ShareHost) remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.leases[id]; l != nil {
		l.expiry.Stop()
		delete(h.leases, id)
		if h.perUser[l.user]--; h.perUser[l.user] <= 0 {
			delete(h.perUser, l.user)
		}
	}
}

// lookup returns the lease of the actor. Another person's lease is the same
// as none.
func (h *ShareHost) lookup(actor contract.Actor, id string) *shareLease {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.leases[id]
	if l == nil || l.user != actor.UserID {
		return nil
	}
	return l
}

// WatchShare renews the lease while it waits. It answers lost at once when
// the policy cancelled the admission, unless stopping says the frontend knows
// already; and whenever the lease is unknown, released or expired.
func (h *ShareHost) WatchShare(ctx context.Context, actor contract.Actor, id string, stopping bool) (gateway.ShareLeaseState, error) {
	h.mu.Lock()
	l := h.leases[id]
	if l == nil || l.user != actor.UserID {
		h.mu.Unlock()
		return gateway.ShareLeaseState{Lost: true}, nil
	}
	l.watchers++
	l.expiry.Stop()
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		l.watchers--
		if l.watchers == 0 {
			l.expiry.Reset(h.grace())
		}
		h.mu.Unlock()
	}()
	lost := l.lost
	if stopping {
		lost = nil
	}
	t := time.NewTimer(h.wait())
	defer t.Stop()
	select {
	case <-l.end:
		return gateway.ShareLeaseState{Lost: true}, nil
	case <-lost:
		return gateway.ShareLeaseState{Lost: true}, nil
	case <-t.C:
		return gateway.ShareLeaseState{}, nil
	case <-ctx.Done():
		return gateway.ShareLeaseState{}, ErrUnavailable
	}
}

// ReleaseShare ends the admission of the lease. An unknown lease, or one of
// another person, is a no-op.
func (h *ShareHost) ReleaseShare(_ context.Context, actor contract.Actor, id string) error {
	if l := h.lookup(actor, id); l != nil {
		l.close()
	}
	return nil
}

// leaseCount is the number of leases held, for tests.
func (h *ShareHost) leaseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.leases)
}
