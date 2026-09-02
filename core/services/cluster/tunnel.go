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

// ConnectPath is the route a worker dials to open its tunnel, and the route the
// HTTP layer registers the handler on. It lives here, beside the registry that
// holds what the dial produces, for the reason PeerPath does: the HTTP
// endpoints package imports this one, never the other way round.
//
// The literal is spelled out rather than derived from auth.ClusterPathPrefix
// because importing core/http/auth is exactly the dependency this package must
// not have. A spec in the endpoints package, which can see both, holds the two
// from drifting apart.
const ConnectPath = "/api/cluster/connect"

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
	// claiming holds one gate per node that a claim is in flight for. It is
	// what makes "claim, then record the epoch" indivisible per node; see
	// enterClaim.
	claiming map[string]chan struct{}
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
		reg:      reg,
		selfID:   selfID,
		tunnels:  map[string]*heldTunnel{},
		claiming: map[string]chan struct{}{},
	}
}

// enterClaim takes the gate for nodeID, so that no two claims for one node are
// ever in flight at the same time. leaveClaim releases it.
//
// It exists because a claim and the record of that claim are two steps, and
// between them the database has already moved. Two Attach calls for one node
// both claim, and PostgreSQL serialises the two upserts, but nothing orders the
// two map writes against the two commits: the entry that ends up installed can
// carry the epoch of the claim that did NOT win the row. Its Detach then
// releases an epoch the row does not hold, the release matches nothing, and the
// row survives the socket. Nothing sweeps that, because the replica named on it
// is alive and heartbeating, so Owner keeps naming this replica as the owner of
// a tunnel it no longer holds and every dialer routed here gets ErrNotOwner.
//
// The gate is per node rather than one lock over the whole registry so that a
// slow claim for one worker does not hold up Open for any other, the same
// reason PeerPool locks per peer. Detach is deliberately NOT gated: it takes no
// context and must never park behind an in-flight database call. It does not
// need to be, because it changes no epoch; what it can interleave with is
// covered where that matters, in Reclaim.
//
// The entry is deleted rather than kept, so the map holds only the claims
// actually in flight and cannot grow with the number of workers ever seen.
func (t *TunnelRegistry) enterClaim(ctx context.Context, nodeID string) error {
	for {
		t.mu.Lock()
		gate, busy := t.claiming[nodeID]
		if !busy {
			t.claiming[nodeID] = make(chan struct{})
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()

		// Re-checked in the loop rather than taken on waking: several waiters
		// are released by one close, and only one of them may proceed.
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// leaveClaim releases the gate enterClaim took. The channel is closed rather
// than sent on, so every waiter wakes rather than one.
func (t *TunnelRegistry) leaveClaim(nodeID string) {
	t.mu.Lock()
	gate := t.claiming[nodeID]
	delete(t.claiming, nodeID)
	t.mu.Unlock()
	close(gate)
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
//
// Two Attach calls for one node are serialised, claim and record together, so
// the entry that survives is always the one whose claim the row carries. See
// enterClaim for what an unserialised pair leaves behind.
func (t *TunnelRegistry) Attach(ctx context.Context, nodeID string, sess *yamux.Session) (int64, error) {
	if sess == nil {
		// Claiming would publish a tunnel that cannot carry anything, and the
		// fence would then have to be unwound by a Detach nobody will call.
		return 0, fmt.Errorf("attaching tunnel for node %q: no session", nodeID)
	}

	if err := t.enterClaim(ctx, nodeID); err != nil {
		return 0, fmt.Errorf("attaching tunnel for node %q: %w", nodeID, err)
	}

	// The gated part is a closure so its release can be DEFERRED while the
	// session close below still happens outside the gate. Releasing on each
	// return path instead leaves one way out uncovered: a panic. Claim does
	// database work, and a panic anywhere under it would leave this node's gate
	// closed for the life of the process, so every later Attach or Reclaim for
	// that worker would block in enterClaim until its own context expired. The
	// caller's recover would report the panic and the worker would look
	// permanently unable to reconnect, with nothing linking the two.
	var previous *heldTunnel
	epoch, err := func() (int64, error) {
		defer t.leaveClaim(nodeID)

		epoch, err := t.reg.Claim(ctx, nodeID, t.selfID)
		if err != nil {
			return 0, err
		}

		t.mu.Lock()
		previous = t.tunnels[nodeID]
		t.tunnels[nodeID] = &heldTunnel{sess: sess, token: epoch, claim: epoch}
		t.mu.Unlock()
		return epoch, nil
	}()
	if err != nil {
		return 0, err
	}

	// Closed after the gate is released, not under it. The gate is justified by
	// being held for one claim round trip, and closing a session is not that:
	// yamux closes the underlying conn and then waits for both its send and
	// recv loops to exit (go-yamux/v5@v5.1.0/session.go:330-332), and the send
	// loop can be inside a write bounded only by ConnectionWriteTimeout. That
	// is a wait on other goroutines, and it must not stand between a worker
	// re-dialling this node and its claim.
	//
	// Releasing first is safe because the superseded session is no longer
	// reachable from the map: whoever re-dials next replaces an entry that
	// already names the new session, and this close can only ever affect the
	// one it just displaced.
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
		// The same rule peerlink.go applies to a peer, applied here to a
		// tunnel, because the confusion is the same one: a caller whose own
		// budget ran out gets the socket's error back before the context's
		// cancel func has necessarily run, so ctx.Err() can still read nil
		// while the failure is entirely the caller's. Reporting it plainly
		// would put "the tunnel this replica holds would not carry a stream"
		// in an operator's log for a worker that is fine and a client that was
		// impatient. callerRanOut settles it on the wall clock; see its
		// comment for why ctx.Err() alone is not the question.
		//
		// The caller's error is wrapped rather than returned bare, so
		// WorkerDialer's contract still holds (every failure to resolve or open
		// carries ErrNoRoute) and context.DeadlineExceeded stays matchable
		// underneath for anyone that wants to tell the two apart.
		if ctxErr := callerRanOut(ctx); ctxErr != nil {
			return nil, fmt.Errorf("opening a stream to node %q over the tunnel held here: the caller's own budget ran out: %w", nodeID, ctxErr)
		}
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
// having noticed, and how long that lasts is decided by the keepalive on the
// session whoever accepted the tunnel built. The worker's next reconnect
// supersedes the claim in any case.
//
// The entry of a skipped tunnel is left alone. Whoever attached it owns its
// lifetime and will detach it; Reclaim is not an eviction path, and evicting
// here would race that goroutine.
//
// A single node's failure does not abort the rest: the tunnels are independent,
// and a claim that failed is retried on the next sweep this replica survives.
func (t *TunnelRegistry) Reclaim(ctx context.Context) (int, error) {
	t.mu.Lock()
	held := make([]string, 0, len(t.tunnels))
	for nodeID := range t.tunnels {
		held = append(held, nodeID)
	}
	t.mu.Unlock()

	var reclaimed int
	var errs []error
	for _, nodeID := range held {
		if err := t.reclaimOne(ctx, nodeID); err != nil {
			if errors.Is(err, errTunnelNotReclaimed) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		reclaimed++
	}
	if len(errs) > 0 {
		return reclaimed, fmt.Errorf("re-claiming worker tunnels: %w", errors.Join(errs...))
	}
	return reclaimed, nil
}

// errTunnelNotReclaimed reports that a node was passed over rather than failed:
// its session is closed, or the attachment went away while the claim was in
// flight. It never leaves this file. It exists so Reclaim's count stays honest
// without "skipped" having to look like an error to its caller.
var errTunnelNotReclaimed = errors.New("cluster: tunnel not re-claimed")

// reclaimOne writes a fresh claim for one node and records it on whatever
// attachment is installed for that node.
//
// Whatever is installed when the gate is taken, not whatever Reclaim listed a
// moment earlier. A worker that re-dialled in between has an entry carrying the
// epoch of ITS claim, and the gate makes that claim strictly older than this
// one, so the row now holds this epoch and only this entry can release it.
// Refusing to record onto an attachment because it is not the one listed would
// leave that row with no attachment able to release it, which is the leak this
// whole function exists to prevent.
//
// If nothing is installed, the attachment detached while the claim was in
// flight. Detach is not gated, so this is reachable, and it is the one case
// where a claim is drawn that no attachment will ever release: the row would
// name this replica for a tunnel it does not hold, and Owner would send every
// dialer here to be told ErrNotOwner. The claim is therefore released again.
// Releasing it cannot take anyone else's row, because Release matches the epoch
// exactly and no epoch is ever reissued.
func (t *TunnelRegistry) reclaimOne(ctx context.Context, nodeID string) error {
	// The gate is taken before the entry is even read, so that everything this
	// function decides is decided about the attachment its claim will land on.
	// Reading first and gating after would leave a window in which a re-dial
	// replaces the entry, and the liveness this checked would be a property of
	// a session it is no longer claiming for.
	if err := t.enterClaim(ctx, nodeID); err != nil {
		return fmt.Errorf("re-claiming node %q: %w", nodeID, err)
	}

	// Gated part in a closure so the release is DEFERRED, for the reason Attach
	// gives: a panic under Claim would otherwise wedge this node's gate for the
	// life of the process. The trailing Release still runs outside the gate.
	var epoch int64
	var installed bool
	if err := func() error {
		defer t.leaveClaim(nodeID)

		t.mu.Lock()
		tunnel, ok := t.tunnels[nodeID]
		t.mu.Unlock()
		if !ok {
			// Detached between Reclaim listing the nodes and this gate. Nothing
			// was claimed, so there is nothing to undo.
			return errTunnelNotReclaimed
		}
		if tunnel.sess.IsClosed() {
			xlog.Debug("skipping re-claim of a worker tunnel whose session is closed", "node", nodeID)
			return errTunnelNotReclaimed
		}

		var err error
		epoch, err = t.reg.Claim(ctx, nodeID, t.selfID)
		if err != nil {
			return err
		}

		t.mu.Lock()
		current, present := t.tunnels[nodeID]
		installed = present
		if installed {
			// current is necessarily the entry read above: the gate is still
			// held, and Attach and reclaimOne are the only writers that install
			// one. The identity is therefore not re-checked; the case that IS
			// reachable is the entry being gone, because Detach is not gated.
			current.claim = epoch
		}
		t.mu.Unlock()
		return nil
	}(); err != nil {
		return err
	}

	if installed {
		return nil
	}

	// Released outside the gate: it is a second round trip, and holding the
	// gate across it would park a worker re-dialling this node behind a
	// cleanup. A re-dial that claims first simply makes this release match
	// nothing, which is the same no-op it would have been.
	if err := t.reg.Release(ctx, nodeID, t.selfID, epoch); err != nil && !errors.Is(err, ErrNoConnection) {
		return fmt.Errorf("releasing a re-claim for detached node %q: %w", nodeID, err)
	}
	return errTunnelNotReclaimed
}
