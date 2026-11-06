// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A service whose resources are shared through the owner's policy (a kind
// that is not an add-on, for example a saved agent) runs its work in the
// process that serves the person, which is often not the owner. These calls
// let it look its resources up, create one and run work under the owner's
// admission from any frontend. They are sharing calls (ResourceCall.sharing):
// the owner checks the account, and its policy the feature the kind names.
//
// The owner cannot reach a frontend, so an admission is a lease that the
// frontend keeps alive with a long poll (share-watch). The poll answers at
// once when the owner's policy cancels the admission (a revocation or a
// lowering); the frontend then cancels the work, keeps the lease alive until
// the work returned, and releases it. Only then does the owner's admission
// end, so the owner's grant change answers after the work stopped, as it does
// for work in its own process. A lease nobody watches expires on the owner.
//
// A service that already names its resources (a connector binding has an ID,
// a collection a name) keeps one record for each name: share-ensure makes or
// finds the record of a name of the caller, share-forget drops it after every
// grant was revoked, and share-views gives the name of each record.

const (
	shareViews   = "share-views"
	shareCreate  = "share-create"
	shareAdmit   = "share-admit"
	shareWatch   = "share-watch"
	shareRelease = "share-release"
	shareEnsure  = "share-ensure"
	shareForget  = "share-forget"
)

// maxShareLocal bounds the service's name of a resource in a call.
const maxShareLocal = 256

// ShareView is one resource of a kind as the caller sees it. OwnerID is the
// owner's account ID: the frontend uses it to match the resource to its own
// record and shows a display name, never the ID, to anyone else.
type ShareView struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Everyone bool   `json:"everyone,omitempty"`
	OwnerID  string `json:"ownerId"`
	// Shared is the number of grants, for the owner only.
	Shared int `json:"shared,omitempty"`
	// Local is the service's own name of the resource (see share-ensure),
	// empty for a resource made by share-create.
	Local string `json:"local,omitempty"`
}

// ShareViews is the answer of share-views: every resource of the kind the
// caller owns or holds a grant on.
type ShareViews struct {
	Kind  string      `json:"kind"`
	Views []ShareView `json:"views"`
}

// ShareRef is the answer of share-create.
type ShareRef struct {
	ResourceID string `json:"resourceId"`
}

// ShareLease is the answer of share-admit: the lease that holds the
// admission on the owner and how long one watch of it waits.
type ShareLease struct {
	Lease       string `json:"lease"`
	WatchMillis int64  `json:"watchMillis"`
}

// ShareLeaseState is the answer of share-watch. Lost says the admission
// ended on the owner: it was cancelled, it expired, or the owner does not
// know the lease (it restarted).
type ShareLeaseState struct {
	Lost bool `json:"lost"`
}

// ShareAuthority is the owner's half of these calls. Only the owner holds
// one; a frontend leaves it nil and forwards.
type ShareAuthority interface {
	ShareViews(ctx context.Context, actor contract.Actor, kind string) (ShareViews, error)
	CreateShare(ctx context.Context, actor contract.Actor, kind string) (ShareRef, error)
	// AdmitShare admits the actor for the action on the resource and holds
	// the admission under a new lease, or answers the policy's denial.
	AdmitShare(ctx context.Context, actor contract.Actor, kind, resourceID, action string) (ShareLease, error)
	// WatchShare renews the lease of the actor and waits for up to one watch
	// period. It answers Lost at once when the admission is cancelled or
	// ended, unless stopping says the frontend already knows and is stopping
	// the work: then it only keeps the lease alive.
	WatchShare(ctx context.Context, actor contract.Actor, lease string, stopping bool) (ShareLeaseState, error)
	// ReleaseShare ends the admission of the lease. It is safe to repeat.
	ReleaseShare(ctx context.Context, actor contract.Actor, lease string) error
	// EnsureShare returns the record the actor keeps under the name local in
	// the kind, and makes it when there is none.
	EnsureShare(ctx context.Context, actor contract.Actor, kind, local string) (ShareRef, error)
	// ForgetShare revokes every grant of a record of the actor and drops it.
	ForgetShare(ctx context.Context, actor contract.Actor, kind, resourceID string) error
}

// leased says whether the call is one of the lease calls above.
func (c ResourceCall) leased() bool {
	switch c.Op {
	case shareViews, shareCreate, shareAdmit, shareWatch, shareRelease, shareEnsure, shareForget:
		return true
	}
	return false
}

// shareLocal serves a lease call on the owner.
func (g *Gateway) shareLocal(ctx context.Context, a contract.Actor, call ResourceCall) (any, error) {
	if g.ShareAuthority == nil {
		return nil, ErrUnavailable
	}
	if len(call.Lease) > 64 || len(call.ResourceID) > 256 || len(call.Action) > 32 || len(call.Local) > maxShareLocal {
		return nil, ErrInvalidRequest
	}
	switch call.Op {
	case shareViews:
		return g.ShareAuthority.ShareViews(ctx, a, call.ShareKind)
	case shareCreate:
		return g.ShareAuthority.CreateShare(ctx, a, call.ShareKind)
	case shareAdmit:
		return g.ShareAuthority.AdmitShare(ctx, a, call.ShareKind, call.ResourceID, call.Action)
	case shareWatch:
		return g.ShareAuthority.WatchShare(ctx, a, call.Lease, call.Stopping)
	case shareRelease:
		return struct{}{}, g.ShareAuthority.ReleaseShare(ctx, a, call.Lease)
	case shareEnsure:
		return g.ShareAuthority.EnsureShare(ctx, a, call.ShareKind, call.Local)
	case shareForget:
		return struct{}{}, g.ShareAuthority.ForgetShare(ctx, a, call.ShareKind, call.ResourceID)
	}
	return nil, ErrForbidden
}

// ShareViews lists the resources of a kind the caller owns or holds a grant
// on, with the owner of each.
func (g *Gateway) ShareViews(ctx context.Context, a contract.Actor, kind string) (ShareViews, error) {
	return decodeResource[ShareViews](g.resourceCall(ctx, a, "", ResourceCall{Op: shareViews, ShareKind: kind}))
}

// CreateShare makes a resource of a kind owned by the caller. A lost answer
// is an unknown outcome: the owner may have made a record nobody uses.
func (g *Gateway) CreateShare(ctx context.Context, a contract.Actor, kind string) (ShareRef, error) {
	return decodeResource[ShareRef](g.resourceCall(ctx, a, "", ResourceCall{Op: shareCreate, ShareKind: kind}))
}

// EnsureShare returns the record the caller keeps under the name local in the
// kind, and makes it when there is none. Repeating it is safe: a lost answer
// is an unknown outcome that the same call settles.
func (g *Gateway) EnsureShare(ctx context.Context, a contract.Actor, kind, local string) (ShareRef, error) {
	return decodeResource[ShareRef](g.resourceCall(ctx, a, "", ResourceCall{Op: shareEnsure, ShareKind: kind, Local: local}))
}

// ForgetShare revokes every grant of a record of the caller and drops it.
// The owner answers after the work of every grantee stopped.
func (g *Gateway) ForgetShare(ctx context.Context, a contract.Actor, kind, resourceID string) error {
	_, err := g.resourceCall(ctx, a, "", ResourceCall{Op: shareForget, ShareKind: kind, ResourceID: resourceID})
	return err
}

// Bounds of the frontend half of a lease.
const (
	// DefaultShareWatch is the watch period an owner gives when its answer
	// names none.
	DefaultShareWatch = 15 * time.Second
	// maxShareWatch caps the period an owner may ask for, so a watch stays
	// within the carrier's 65 s client and server bounds.
	maxShareWatch = 30 * time.Second
	// shareWatchMargin is how much longer than the period the frontend waits
	// for one watch before it reads the owner as lost.
	shareWatchMargin = 5 * time.Second
)

// RunShared runs run in this process while the owner admits the actor for
// the action on the resource. The context given to run is cancelled when the
// owner's policy takes the admission away, when the owner can no longer be
// reached (the owner could then not take it away), and when ctx ends.
// RunShared returns after run returned and the lease was released. When the
// owner refuses the admission, run is not called.
func (g *Gateway) RunShared(ctx context.Context, a contract.Actor, kind, resourceID, action string, run func(context.Context) error) error {
	if run == nil {
		return ErrForbidden
	}
	lease, err := decodeResource[ShareLease](g.resourceCall(ctx, a, "", ResourceCall{Op: shareAdmit, ShareKind: kind, ResourceID: resourceID, Action: action}))
	if err != nil {
		return err
	}
	if lease.Lease == "" {
		return ErrUnavailable
	}
	wait := time.Duration(lease.WatchMillis) * time.Millisecond
	if wait <= 0 || wait > maxShareWatch {
		wait = DefaultShareWatch
	}
	runCtx, cancel := context.WithCancel(ctx)
	// finished ends when run returned; a watch that only keeps the lease
	// alive while the work stops lasts until then.
	finished, finish := context.WithCancel(context.WithoutCancel(ctx))
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		stopping := false
		for {
			parent := runCtx
			if stopping {
				parent = finished
			}
			wctx, wcancel := context.WithTimeout(parent, wait+shareWatchMargin)
			st, err := decodeResource[ShareLeaseState](g.resourceCall(wctx, a, "", ResourceCall{Op: shareWatch, Lease: lease.Lease, Stopping: stopping}))
			wcancel()
			if finished.Err() != nil {
				return
			}
			if stopping {
				// A lost owner or an ended lease leaves nothing to keep alive.
				if err != nil || st.Lost {
					return
				}
				continue
			}
			if err != nil || st.Lost || runCtx.Err() != nil {
				cancel()
				stopping = true
			}
		}
	}()
	err = run(runCtx)
	finish()
	cancel()
	<-watched
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultReadTimeout)
	defer rcancel()
	// A release that does not arrive is settled by the lease's expiry.
	_, _ = g.resourceCall(rctx, a, "", ResourceCall{Op: shareRelease, Lease: lease.Lease})
	return err
}
