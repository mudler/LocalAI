// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"fmt"
	"time"
)

// Presence is what this package can say about a worker's tunnel.
//
// Four values and not a boolean, because the conditions underneath are four and
// only ONE of them is a verdict a caller may act on. Collapsing any pair is the
// defect this phase exists to prevent: absence is what makes the scheduler stop
// placing work, reap node rows and evict models, and one of those paths runs
// during inference, so a worker misreported as absent loses the models it is
// serving at that moment.
//
// What this type does NOT cover is the two conditions that live on the dialing
// path: a replica that would not answer, and the worker's own reply (including
// its "that backend is not there"). Those are IsWorkerAnswer and ErrNoRoute in
// dialer.go, and they are deliberately not spelled again here.
type Presence uint8

const (
	// PresenceUnknown reports that no connection row exists for this node at
	// all. This package cannot tell a worker that has never dialled from one
	// whose departure aged out of retention, and it must not guess: the first
	// is a worker still starting up and the second is one long gone.
	//
	// It is the zero value on purpose, so a Presence returned alongside an
	// error is the value nobody may act on rather than one that reads as a
	// verdict.
	PresenceUnknown Presence = iota
	// PresenceConnected reports that a LIVE replica holds this worker's tunnel.
	PresenceConnected
	// PresenceReconnecting reports that no live replica holds the tunnel and
	// the grace has not run out: either the departure is recent, or the owning
	// replica died and no departure has been stamped yet. A retry, never a
	// verdict; nobody may act on it.
	PresenceReconnecting
	// PresenceGone reports that no live replica holds the tunnel and the
	// departure is older than the grace. This is the ONLY value a caller may
	// read as absence.
	PresenceGone
)

// String names the value for a log line. The default case names the number
// rather than falling through to a real value: a caller reading "gone" out of a
// log for a value that does not exist would be reading the one answer it is
// allowed to reap on.
func (p Presence) String() string {
	switch p {
	case PresenceUnknown:
		return "unknown"
	case PresenceConnected:
		return "connected"
	case PresenceReconnecting:
		return "reconnecting"
	case PresenceGone:
		return "gone"
	default:
		return fmt.Sprintf("presence(%d)", uint8(p))
	}
}

// presenceQuery answers all three questions about one node in one statement:
// does a row exist, does a LIVE replica hold it, and is its departure older
// than the grace.
//
// One statement rather than a read followed by an instance lookup, for the
// reason Owner is one statement: between two reads the owning replica can die,
// and the answer would then be assembled from two different snapshots of the
// cluster. Phase 2 shipped exactly that as a dialer relaying into a corpse for
// a whole liveness window.
//
// HELD-NESS IS ASKED FIRST, and the departure only refines it. Every writer in
// this binary clears disconnected_at in the same statement that writes the
// owner, but that is a property of these writers rather than of the table: a
// replica running a binary from before the column existed re-claims WITHOUT
// clearing the stamp, so during a rolling upgrade a HELD row can carry a
// departure older than any grace. Reading the stamp first, or treating a
// non-null stamp as evidence of absence on its own, reports a worker that is
// connected right now as gone. That is why `departed` carries the negation of
// connectionIsHeld rather than standing on the timestamp alone.
//
// Both windows are computed by the DATABASE. They are compared across replicas,
// so a Go-side cutoff would make the effective window depend on each replica's
// clock skew, and replicas disagreeing about whether a worker is gone is the
// flapping this branch exists to remove. No behavioural spec can see the
// difference (the test container shares the host clock), which is why the
// statement shape is pinned instead.
//
// The predicates are the package's own, not copies. Two spellings of "live" or
// of "held" drift, and the drift here reads as a worker that one query calls
// connected and another calls gone.
//
// The bind order is the order the placeholders appear in the text: the grace,
// then the liveness window, then the node id.
const presenceQuery = `
SELECT
  (` + connectionIsHeld + ` AND instances.id IS NOT NULL) AS held,
  (NOT (` + connectionIsHeld + `)
     AND node_connections.disconnected_at IS NOT NULL
     AND node_connections.disconnected_at < now() - make_interval(secs => ?)) AS departed
FROM node_connections
LEFT JOIN instances
  ON instances.id = node_connections.owner_instance_id
 AND ` + instanceIsLive + `
WHERE node_connections.node_id = ?`

// Presence reports what this deployment can say about nodeID's tunnel, with
// grace as the window a departure has to outlive before it becomes a verdict.
//
// The grace is a parameter rather than a constant because it is an operator's
// trade (see DistributedConfig.WorkerReconnectGrace), where the liveness window
// inside the query is not: that one has to be the window the membership loop
// sweeps with, or a reader would keep trusting an owner the sweeper has already
// declared dead.
//
// A held row whose owner is no longer live is PresenceReconnecting and not
// PresenceGone, deliberately. Nothing has stamped a departure on it yet, so
// there is no age to compare and the grace clock has not started; the
// membership sweep is what starts it. A replica dying must not condemn every
// worker it held.
func (r *Registry) Presence(ctx context.Context, nodeID string, grace time.Duration) (Presence, error) {
	// Refused rather than attempted, for the reason Owner refuses: now() and
	// make_interval are PostgreSQL, so on the single-binary SQLite path this
	// would fail with "no such function: now", which reads as a missing
	// migration. The refusal carries PresenceUnknown, the one value nobody may
	// act on: a deployment with no cluster has no answer about a worker's
	// tunnel, and reporting PresenceGone would license a reap.
	if !isPostgres(r.db) {
		return PresenceUnknown, fmt.Errorf("reading presence of node %q: connection ownership requires PostgreSQL, this deployment runs on %q", nodeID, r.db.Dialector.Name())
	}
	var row struct {
		Held     bool
		Departed bool
	}
	// gorm's Scan leaves the destination zero-valued when nothing matched and
	// reports no error, so "no row" and "a row whose booleans are both false"
	// are told apart by RowsAffected and by nothing else. Scan does not produce
	// gorm.ErrRecordNotFound, so matching on that would silently turn every
	// missing row into PresenceReconnecting.
	res := r.db.WithContext(ctx).Raw(presenceQuery,
		grace.Seconds(), InstanceLiveness.Seconds(), nodeID,
	).Scan(&row)
	if res.Error != nil {
		return PresenceUnknown, fmt.Errorf("reading presence of node %q: %w", nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return PresenceUnknown, nil
	}
	switch {
	case row.Held:
		// First, and redundantly so: the query already excludes held rows from
		// `departed` (on the RAW column, before the join), so either gate alone
		// answers Connected for a held row with a live owner, and no
		// behavioural spec can tell which one is doing the work.
		//
		// The redundancy is PARTIAL, not a second complete gate. With the SQL
		// gate removed and the owner DEAD, `held` is false and `departed` is
		// true, so this ordering yields Gone where Reconnecting is correct.
		// That is why the SQL gate carries its own assertion in the
		// statement-shape spec: removing it is caught there, not here.
		return PresenceConnected, nil
	case row.Departed:
		return PresenceGone, nil
	default:
		return PresenceReconnecting, nil
	}
}
