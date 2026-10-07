package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// ConnectPath is the route that a worker dials to open its tunnel. The HTTP
// layer registers the handler on it and the worker builds its URL from it.
const ConnectPath = "/api/cluster/connect"

// ErrNotOwner means that this replica does not hold the tunnel of a node.
//
// It is a routing fact and nothing else: another replica can hold that worker,
// and a caller that gets this error asks the owner that the database names. It
// must never come from anything that only failed. A database error, a broken
// socket and a session that ended under a held entry are all reported as
// themselves, because "not held here" tells a dialer to look elsewhere for a
// worker that is right here, and absence must never stand in for a failure.
var ErrNotOwner = errors.New("tunnel: this replica does not hold the tunnel for that node")

// releaseTimeout bounds the release in Detach. Detach runs in the goroutine that
// has just seen a session end, and that goroutine must not wait for a database
// that went away with the session.
const releaseTimeout = 5 * time.Second

// Registry holds the tunnels that this replica has accepted, and keeps the
// node_connections table in agreement with what it holds.
//
// The table says which replica owns a worker. This registry says which socket
// that ownership means. Attach writes the two in one order, always.
//
// A node has up to two sessions: the inference lane and the bulk lane. They
// share one claim and one epoch. The claim belongs to the inference lane, which
// is the session that the worker must have. The bulk lane is an addition to it
// and is held only on the replica that holds the inference lane.
type Registry struct {
	reg    *cluster.Registry
	selfID string

	mu      sync.Mutex
	tunnels map[string]*heldTunnel
	// claiming holds one gate for each node that has a claim in progress. It
	// makes "claim and then record the claim" one step for each node.
	claiming map[string]chan struct{}
	// bulkTokens numbers the attachments of the bulk lane. A bulk lane has no
	// claim of its own, so its attachment needs its own identity for Detach.
	bulkTokens int64
}

// heldTunnel is the tunnel of one node.
//
// token identifies one attachment of the inference lane for its whole life, and
// it is the only value that Detach compares. claim is the epoch of the row that
// this replica holds for the node, and Release needs it because the fence
// matches the row exactly. The two are the same number until this replica is
// swept and claims again, and they stay separate because they answer different
// questions. Neither is compared for order: Claim promises uniqueness, not
// growth.
type heldTunnel struct {
	inference *Session
	token     int64
	claim     int64

	bulk      *Session
	bulkToken int64
}

// NewRegistry returns a registry that claims tunnels as selfID. The ID must be
// the one that this replica registers in the instances table, because Owner
// joins a claim against that table to decide if the owner is alive.
func NewRegistry(reg *cluster.Registry, selfID string) *Registry {
	return &Registry{
		reg:      reg,
		selfID:   selfID,
		tunnels:  map[string]*heldTunnel{},
		claiming: map[string]chan struct{}{},
	}
}

// enterClaim takes the gate of a node, so that two claims of one node are never
// in progress together. leaveClaim gives it back.
//
// A claim and the record of that claim are two steps, and the database moves
// between them. Two Attach calls for one node both claim, and PostgreSQL puts
// the two writes in order, but nothing orders the two map writes against the
// two commits. The entry that ends up in the map could carry the epoch of the
// claim that did not win the row. Its Detach would then release an epoch that
// the row does not hold, the release would match nothing, and the row would
// outlive the socket. Nothing sweeps that row, because the replica named on it
// is alive. Owner would go on naming this replica as the owner of a tunnel that
// it does not hold.
//
// The gate is per node and not for the whole registry, so a slow claim of one
// worker does not hold up Open for the others. Detach does not use the gate. It
// takes no context and must not wait for a database call. It does not need the
// gate, because it changes no epoch.
func (t *Registry) enterClaim(ctx context.Context, nodeID string) error {
	for {
		t.mu.Lock()
		gate, busy := t.claiming[nodeID]
		if !busy {
			t.claiming[nodeID] = make(chan struct{})
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()

		// The loop checks again after a wake-up. One close wakes several
		// waiters and only one of them may go on.
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// leaveClaim gives back the gate. The channel is closed, so every waiter wakes.
func (t *Registry) leaveClaim(nodeID string) {
	t.mu.Lock()
	gate := t.claiming[nodeID]
	delete(t.claiming, nodeID)
	t.mu.Unlock()
	close(gate)
}

// Attach records this replica as the owner of the tunnel of nodeID and stores
// the session. The result is the token that the caller must give to Detach.
//
// For the inference lane the claim is written before the session is stored.
// A claimant that installs itself first and finds later that it cannot claim
// has published, for a while, a tunnel that no row records: Held would name it,
// and another replica that asks Owner would be told that the worker is not
// connected anywhere. If the claim comes first, a claim that fails leaves this
// replica as it was.
//
// A worker that dials again to the same replica replaces its earlier
// attachment, and the registry closes the session that it replaced. Nothing
// else can close it: the goroutine that accepted it is parked on a session that
// is not broken, only replaced. A bulk session of the replaced attachment is
// closed too. The caller keeps the session that it passed in.
//
// The bulk lane has no claim. It is stored under the tunnel that this replica
// holds, and without one the result is ErrNotOwner. The worker is then told to
// try another replica, and its next dial can land on the owner.
func (t *Registry) Attach(ctx context.Context, nodeID string, lane Lane, sess *Session) (int64, error) {
	if sess == nil {
		return 0, fmt.Errorf("attaching tunnel for node %q: no session", nodeID)
	}
	switch lane {
	case LaneInference:
		return t.attachInference(ctx, nodeID, sess)
	case LaneBulk:
		return t.attachBulk(nodeID, sess)
	default:
		return 0, fmt.Errorf("attaching tunnel for node %q: unknown lane %q", nodeID, lane)
	}
}

func (t *Registry) attachInference(ctx context.Context, nodeID string, sess *Session) (int64, error) {
	if err := t.enterClaim(ctx, nodeID); err != nil {
		return 0, fmt.Errorf("attaching tunnel for node %q: %w", nodeID, err)
	}

	// The part under the gate is a closure, so that the gate is released with a
	// defer and the close of the old session below still happens outside the
	// gate. A panic under Claim, which does database work, would otherwise leave
	// the gate of this node closed for the life of the process.
	var previous *heldTunnel
	epoch, err := func() (int64, error) {
		defer t.leaveClaim(nodeID)

		epoch, err := t.reg.Claim(ctx, nodeID, t.selfID)
		if err != nil {
			return 0, err
		}

		t.mu.Lock()
		previous = t.tunnels[nodeID]
		t.tunnels[nodeID] = &heldTunnel{inference: sess, token: epoch, claim: epoch}
		t.mu.Unlock()
		return epoch, nil
	}()
	if err != nil {
		return 0, err
	}

	// Closed after the gate is released. Yamux closes the connection and waits
	// for its send and receive loops, and the send loop can be in a write that
	// only the write timeout bounds. That wait is on other goroutines and must
	// not stand between a worker that dials again and its claim. This is safe
	// because the old session can no longer be reached from the map.
	if previous != nil {
		if previous.inference != sess {
			xlog.Debug("worker dialled this replica again, dropping its previous tunnel", "node", nodeID)
			_ = previous.inference.Close()
		}
		if previous.bulk != nil {
			_ = previous.bulk.Close()
		}
	}
	return epoch, nil
}

func (t *Registry) attachBulk(nodeID string, sess *Session) (int64, error) {
	t.mu.Lock()
	held, ok := t.tunnels[nodeID]
	if !ok || held.inference.IsClosed() {
		t.mu.Unlock()
		return 0, fmt.Errorf("attaching the bulk lane for node %q: %w", nodeID, ErrNotOwner)
	}
	previous := held.bulk
	t.bulkTokens++
	held.bulk = sess
	held.bulkToken = t.bulkTokens
	token := held.bulkToken
	t.mu.Unlock()

	if previous != nil && previous != sess {
		xlog.Debug("worker dialled the bulk lane again, dropping its previous session", "node", nodeID)
		_ = previous.Close()
	}
	return token, nil
}

// Detach drops the attachment that token identifies. For the inference lane it
// also releases the claim and closes the bulk session. A token that is not the
// one that Attach gave the current holder does nothing. That is how a holder
// that was replaced is stopped from removing the attachment that replaced it.
//
// The token is matched by equality and never by order. A claim made after a
// release can draw a lower number than one that was already issued, so a stale
// token can compare either way against a live one.
//
// To release a claim that this replica no longer holds is normal. It is what a
// worker that moved to another replica looks like from here. Detach is the last
// thing that a dying tunnel does and has nobody to return an error to, so it
// logs.
func (t *Registry) Detach(nodeID string, lane Lane, token int64) {
	if lane == LaneBulk {
		t.mu.Lock()
		held, ok := t.tunnels[nodeID]
		if ok && held.bulk != nil && held.bulkToken == token {
			held.bulk = nil
			held.bulkToken = 0
		}
		t.mu.Unlock()
		return
	}

	t.mu.Lock()
	held, ok := t.tunnels[nodeID]
	if !ok || held.token != token {
		t.mu.Unlock()
		return
	}
	delete(t.tunnels, nodeID)
	claim := held.claim
	bulk := held.bulk
	t.mu.Unlock()

	// The bulk lane lives and ends with the tunnel that it belongs to. Its
	// worker dials it again after the inference lane is back.
	if bulk != nil {
		_ = bulk.Close()
	}

	// Not the context of the caller, and not the one that Attach got. Both
	// belong to a request or a process that set the tunnel up, and either can be
	// cancelled by now, which would leave the row behind on every ordinary
	// disconnect.
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	// The claim, not the token. The row carries the last epoch that was claimed
	// for this attachment, and the release must match the row exactly.
	if err := t.reg.Release(ctx, nodeID, t.selfID, claim); err != nil {
		if errors.Is(err, cluster.ErrNoConnection) {
			xlog.Debug("worker tunnel claim was already replaced", "node", nodeID, "epoch", claim)
			return
		}
		xlog.Warn("Releasing a worker tunnel claim failed; peers drop it when the heartbeat of this replica ages out",
			"node", nodeID, "epoch", claim, "error", err)
	}
}

// Open returns a stream to the worker over the tunnel that this replica holds.
//
// The bulk lane falls back to the inference lane when the node has no bulk
// session. A worker that predates the bulk lane never has one, and a worker
// that is dialling it again has none for a moment. A transfer that works on the
// session that is shared is better than a transfer that fails.
//
// ErrNotOwner means only that no tunnel of the node is held here. Any other
// failure is returned as itself, wrapped: a session that ended under a held
// entry is a condition of the transport, and ErrNotOwner for it would send a
// dialer to look elsewhere for a worker that this replica holds.
//
// A failed open does not remove the entry. Attach and Detach decide what is
// held here, and an open that removed it would race with the goroutine that
// owns the session and is about to detach it.
func (t *Registry) Open(ctx context.Context, nodeID string, lane Lane) (net.Conn, error) {
	t.mu.Lock()
	held, ok := t.tunnels[nodeID]
	var sess *Session
	if ok {
		sess = held.inference
		if lane == LaneBulk && held.bulk != nil && !held.bulk.IsClosed() {
			sess = held.bulk
		}
	}
	t.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("opening a stream to node %q: %w", nodeID, ErrNotOwner)
	}

	stream, err := sess.OpenStream(ctx)
	if err != nil {
		// A caller whose own budget ran out gets the error of the socket before
		// the cancel function of its context has necessarily run. The error
		// would then be reported as a tunnel that will not carry a stream, for a
		// worker that is well and a client that was impatient. callerRanOut
		// decides on the wall clock. The error of the caller is wrapped and not
		// returned bare, so that context.DeadlineExceeded can still be matched.
		if ctxErr := callerRanOut(ctx); ctxErr != nil {
			return nil, fmt.Errorf("opening a stream to node %q over the tunnel held here: the budget of the caller ran out: %w", nodeID, ctxErr)
		}
		return nil, fmt.Errorf("opening a stream to node %q over the tunnel held here: %w", nodeID, err)
	}
	return stream, nil
}

// callerRanOut reports whether the budget of the caller is what ended an
// attempt.
//
// ctx.Err() alone is not that question. A dial passes the deadline of the caller
// down to the socket, so when the budget runs out the timer of the socket fires
// and its error travels up through the multiplexer. The cancellation of the
// context is another timer, and its function has to be run by the scheduler
// before ctx.Err() stops returning nil. Nothing orders the two, so a worker that
// is well could be reported as unreachable to a caller that only ran out of
// time. The wall clock settles it without waiting for a goroutine.
func callerRanOut(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A failure at exactly the deadline is the caller's too.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// Holds reports whether this replica holds the inference session of a node that
// has not ended. The connect endpoint asks it before it upgrades a bulk
// session, because after the upgrade only a closed socket can say no.
func (t *Registry) Holds(nodeID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	held, ok := t.tunnels[nodeID]
	return ok && !held.inference.IsClosed()
}

// Held returns the nodes whose tunnels this replica holds, sorted. It answers
// what this process holds, which is another question than who the table says
// owns a node. That question is Owner.
func (t *Registry) Held() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.tunnels))
	for nodeID := range t.tunnels {
		out = append(out, nodeID)
	}
	sort.Strings(out)
	return out
}

// Reclaim writes a new claim for every tunnel that is still held here, and
// returns how many it wrote.
//
// It is for one case. This replica stalled long enough for a peer to sweep it,
// which deleted its instance row and every connection row it owned, and it has
// registered again. Registering again rebuilds the instance row and nothing
// else. Without Reclaim the sockets are held here while the table says that
// nobody holds them, and the other replicas answer "not connected" for workers
// that are connected.
//
// A session that is closed is skipped. A claim is an upsert and takes the row
// from whoever holds it now, and a worker with a closed socket here has already
// connected somewhere else. The entry of a skipped tunnel stays: whoever
// attached it owns its life, and Reclaim is not a way to remove it.
//
// A failure for one node does not stop the others. The next sweep that this
// replica survives tries again.
func (t *Registry) Reclaim(ctx context.Context) (int, error) {
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
			if errors.Is(err, errNotReclaimed) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		reclaimed++
	}
	if len(errs) > 0 {
		return reclaimed, fmt.Errorf("claiming worker tunnels again: %w", errors.Join(errs...))
	}
	return reclaimed, nil
}

// errNotReclaimed means that a node was passed over and did not fail: its
// session is closed, or the attachment went away during the claim.
var errNotReclaimed = errors.New("tunnel: not claimed again")

// reclaimOne writes a new claim for one node and records it on the attachment
// that is installed for the node.
//
// The attachment that is installed when the gate is taken is the one that gets
// the claim, and not the one that Reclaim listed a moment before. A worker that
// dialled again in between has an entry with its own claim, which the gate makes
// older than this one. The row now holds the new epoch and only this entry can
// release it.
//
// If nothing is installed, the attachment was detached during the claim. Detach
// does not use the gate, so this can happen, and it is the one case where a
// claim is drawn that no attachment will release. The claim is released again.
// That cannot take the row of someone else, because the release matches the
// epoch exactly and no epoch is issued twice.
func (t *Registry) reclaimOne(ctx context.Context, nodeID string) error {
	// The gate is taken before the entry is read, so that all that this function
	// decides is decided about the attachment that the claim lands on.
	if err := t.enterClaim(ctx, nodeID); err != nil {
		return fmt.Errorf("claiming node %q again: %w", nodeID, err)
	}

	var epoch int64
	var installed bool
	if err := func() error {
		defer t.leaveClaim(nodeID)

		t.mu.Lock()
		tunnel, ok := t.tunnels[nodeID]
		t.mu.Unlock()
		if !ok {
			return errNotReclaimed
		}
		if tunnel.inference.IsClosed() {
			xlog.Debug("not claiming a worker tunnel again: its session is closed", "node", nodeID)
			return errNotReclaimed
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
			// current is the entry that was read above: the gate is held, and
			// only Attach and this function install an entry. The entry can
			// be gone, because Detach does not use the gate.
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

	// Released outside the gate: it is a second round trip, and a worker that
	// dials this node again must not wait behind a clean-up.
	if err := t.reg.Release(ctx, nodeID, t.selfID, epoch); err != nil && !errors.Is(err, cluster.ErrNoConnection) {
		return fmt.Errorf("releasing a new claim of detached node %q: %w", nodeID, err)
	}
	return errNotReclaimed
}
