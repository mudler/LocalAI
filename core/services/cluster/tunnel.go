// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-yamux/v5"
	"github.com/mudler/xlog"
)

// ErrNotOwner reports that this replica does not hold the tunnel for a node.
//
// It is a ROUTING fact and nothing else: some other replica may hold that
// worker perfectly well, and a caller that sees it relays through the owner the
// database names. It must therefore never be produced by anything that merely
// failed. A database error, a broken socket, a session that shut down under a
// held entry: each of those is reported as itself, because reporting them as
// "not held here" tells a dialer to look elsewhere for a worker that is right
// here, and the design forbids absence standing in for unreachable. This is the
// same rule Claim and Owner follow when they refuse a dialect rather than
// answering ErrNoConnection.
var ErrNotOwner = errors.New("cluster: this replica does not hold the tunnel for that node")

// tunnelReleaseTimeout bounds the release Detach performs. Detach is called
// from the goroutine that has just watched a worker's session die, and that
// goroutine must not be parked on a database that went away with it.
const tunnelReleaseTimeout = 5 * time.Second

// TunnelRegistry holds the worker tunnels this replica has accepted, and keeps
// the node_connections table agreeing with what it holds.
//
// It is the local half of the connection fence: the table says which replica
// owns a worker, and this says which socket that ownership actually resolves
// to. The two are written in one order, always, by Attach.
type TunnelRegistry struct {
	reg    *Registry
	selfID string

	mu      sync.Mutex
	tunnels map[string]*heldTunnel
}

// heldTunnel is one accepted worker tunnel.
//
// The two epochs are the same number until this replica is swept and re-claims,
// and they are separate fields because they answer different questions.
//
// token is what Attach handed back, and it is the only value Detach matches.
// It identifies one local attachment for that attachment's whole life, which is
// what lets a superseded holder's Detach be recognised as stale: epochs are
// never reissued, so a token from an earlier attachment cannot collide with a
// later one's.
//
// claim is the epoch of the row this replica currently holds for the node, and
// it is what Release must be given, because that is the row the fence matches
// on. A re-claim draws a fresh epoch and moves this one; leaving Release to use
// the token instead would match nothing, and the row would outlive the socket
// with no caller able to tell.
//
// Neither is ever ordered against the other, or against anything else. Claim
// guarantees uniqueness, not monotonicity.
type heldTunnel struct {
	sess  *yamux.Session
	token int64
	claim int64
}

// NewTunnelRegistry returns a registry that claims tunnels as selfID. The ID
// must be the same one this replica registers in the instances table, since
// that is what Owner joins a claim against to decide the owner is alive.
func NewTunnelRegistry(reg *Registry, selfID string) *TunnelRegistry {
	return &TunnelRegistry{
		reg:     reg,
		selfID:  selfID,
		tunnels: map[string]*heldTunnel{},
	}
}

// Attach records this replica as the owner of nodeID's tunnel and stores the
// session, returning the epoch the caller must later hand to Detach.
//
// The claim is written BEFORE the session is stored, and the order is the
// point. A claimant that installs itself first and only then finds it cannot
// claim has, for that window, published a tunnel no row records: Held names it,
// and a peer asking Owner is told the worker is connected nowhere. Claiming
// first means a failed claim leaves this replica exactly as it was.
//
// A worker that re-dials onto this same replica supersedes its own earlier
// attachment, and the superseded session is closed here. Nothing else can close
// it: whoever accepted it is parked in AcceptStream on a session that is not
// broken, only replaced, and would wait there until the far side noticed. The
// caller keeps ownership of the session it passed in; only a session this
// registry evicted is closed by this registry.
func (t *TunnelRegistry) Attach(ctx context.Context, nodeID string, sess *yamux.Session) (int64, error) {
	if sess == nil {
		// Claiming would publish a tunnel that cannot carry anything, and the
		// fence would then have to be unwound by a Detach nobody will call.
		return 0, fmt.Errorf("attaching tunnel for node %q: no session", nodeID)
	}

	epoch, err := t.reg.Claim(ctx, nodeID, t.selfID)
	if err != nil {
		return 0, err
	}

	t.mu.Lock()
	previous := t.tunnels[nodeID]
	t.tunnels[nodeID] = &heldTunnel{sess: sess, token: epoch, claim: epoch}
	t.mu.Unlock()

	if previous != nil && previous.sess != sess {
		xlog.Debug("worker re-dialled this replica, dropping its previous tunnel", "node", nodeID)
		_ = previous.sess.Close()
	}
	return epoch, nil
}

// Detach drops the attachment epoch identifies and releases its claim. An epoch
// that is not the one Attach handed the current holder is a no-op, which is how
// a superseded holder noticing its dead socket is stopped from evicting the
// attachment that replaced it.
//
// Matched by EQUALITY, never by order. An epoch is unique and never reissued,
// but a claim inserted after a Release can draw a lower number than one already
// issued, so a stale token may compare either way against the live one.
//
// Releasing a claim this replica no longer holds is ordinary rather than
// exceptional: it is what a worker having re-homed to another replica looks
// like from here, so it is logged and not returned. Detach has no error to
// return to, being the last thing a dying tunnel's goroutine does.
func (t *TunnelRegistry) Detach(nodeID string, epoch int64) {
	t.mu.Lock()
	held, ok := t.tunnels[nodeID]
	if !ok || held.token != epoch {
		t.mu.Unlock()
		return
	}
	delete(t.tunnels, nodeID)
	claim := held.claim
	t.mu.Unlock()

	// Not the caller's context, and not the one Attach was given: both belong
	// to the request or the process that set the tunnel up, and by the time a
	// tunnel is being torn down either may already be cancelled, which would
	// leave the row behind on every ordinary disconnect.
	ctx, cancel := context.WithTimeout(context.Background(), tunnelReleaseTimeout)
	defer cancel()
	// The claim, not the token: the row carries whatever epoch was last claimed
	// for this attachment, and Release matches the row exactly.
	if err := t.reg.Release(ctx, nodeID, t.selfID, claim); err != nil {
		if errors.Is(err, ErrNoConnection) {
			xlog.Debug("worker tunnel claim was already superseded", "node", nodeID, "epoch", claim)
			return
		}
		xlog.Warn("Releasing a worker tunnel claim failed; peers will drop it when this replica's heartbeat ages out",
			"node", nodeID, "epoch", claim, "error", err)
	}
}

// Open returns a stream to the worker over the tunnel this replica holds.
//
// ErrNotOwner means only that no tunnel for nodeID is held here. Every other
// failure is returned as itself, wrapped: a session that died under a held
// entry is a transport condition, and answering ErrNotOwner for it would send a
// dialer looking elsewhere for a worker this replica is holding.
//
// A failed open does not evict the entry. Whether a tunnel is held here is
// decided by Attach and Detach, and letting one bad open unhold it would race
// the goroutine that owns the session and is about to detach it properly.
func (t *TunnelRegistry) Open(ctx context.Context, nodeID string) (net.Conn, error) {
	t.mu.Lock()
	held, ok := t.tunnels[nodeID]
	t.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("opening a stream to node %q: %w", nodeID, ErrNotOwner)
	}

	stream, err := held.sess.OpenStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening a stream to node %q over the tunnel held here: %w", nodeID, err)
	}
	return stream, nil
}

// Held returns the nodes whose tunnels this replica holds, sorted.
//
// It answers what this process holds, which is not the same question as who the
// table says owns a node; Owner answers that one. The membership loop uses this
// to know what to re-claim after its rows have been swept.
func (t *TunnelRegistry) Held() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.tunnels))
	for nodeID := range t.tunnels {
		out = append(out, nodeID)
	}
	sort.Strings(out)
	return out
}

// Reclaim writes a fresh claim for every tunnel still held here, and returns
// how many it wrote.
//
// It exists for one case: this replica stalled long enough for a peer to sweep
// it, which deleted its instance row AND every connection row it owned, and it
// has just re-registered. Re-registration rebuilds the instance row only, so
// without this the sockets are still held here while the table records nobody
// holding them, and every other replica answers "not connected" for workers
// that are connected.
//
// A closed session is skipped rather than claimed. Claiming is an upsert, so it
// takes the row from whoever holds it now, and a worker whose socket here is
// closed has already reconnected somewhere: claiming it back would point every
// dialer at a replica that cannot carry a byte to it. The check narrows that
// window rather than closing it, since a socket can be dead without this side
// having noticed; yamux's keepalive bounds how long that lasts, and the
// worker's next reconnect supersedes the claim in any case.
//
// The entry of a skipped tunnel is left alone. Whoever attached it owns its
// lifetime and will detach it; Reclaim is not an eviction path, and evicting
// here would race that goroutine.
//
// A single node's failure does not abort the rest: the tunnels are independent,
// and a claim that failed is retried on the next sweep this replica survives.
func (t *TunnelRegistry) Reclaim(ctx context.Context) (int, error) {
	t.mu.Lock()
	held := make(map[string]*heldTunnel, len(t.tunnels))
	for nodeID, tunnel := range t.tunnels {
		held[nodeID] = tunnel
	}
	t.mu.Unlock()

	var reclaimed int
	var errs []error
	for nodeID, tunnel := range held {
		if tunnel.sess.IsClosed() {
			xlog.Debug("skipping re-claim of a worker tunnel whose session is closed", "node", nodeID)
			continue
		}
		epoch, err := t.reg.Claim(ctx, nodeID, t.selfID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		t.mu.Lock()
		// Re-read under the lock: the tunnel may have been detached, or
		// superseded by a reconnect, while the claim was in flight. Writing the
		// new claim onto whatever is there now would give a different
		// attachment an epoch it never drew, and its Release would then match
		// nothing.
		//
		// The epoch just drawn is then held by no attachment. If it also
		// happened to be the last write to the row, the row outlives the
		// attachment that no longer matches it: it still names this replica,
		// truthfully, but no Detach will remove it. It is cleared by the
		// worker's next claim anywhere, since Claim upserts, and otherwise by
		// the sweep that eventually removes this replica. Serialising Attach
		// and Reclaim per node would close it; the window is one database
		// round trip during the tick that follows a sweep of this replica, and
		// the machinery costs more than it removes.
		if current, ok := t.tunnels[nodeID]; ok && current == tunnel {
			current.claim = epoch
			reclaimed++
		}
		t.mu.Unlock()
	}
	if len(errs) > 0 {
		return reclaimed, fmt.Errorf("re-claiming worker tunnels: %w", errors.Join(errs...))
	}
	return reclaimed, nil
}
