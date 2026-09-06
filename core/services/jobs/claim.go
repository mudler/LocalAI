// SPDX-License-Identifier: MIT

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cluster"
	"gorm.io/gorm"
)

// ClaimKind names what a claimed row asks a worker to do. It replaces the
// subject a publisher used to choose, and it is stored on the row so a claim
// that no dispatcher serves is visible rather than silent.
type ClaimKind string

const (
	ClaimKindTask     ClaimKind = "task"      // was jobs.new
	ClaimKindMCPCI    ClaimKind = "mcp-ci"    // was jobs.mcp-ci.new
	ClaimKindAgentRun ClaimKind = "agent-run" // was agent.execute
)

// WorkClaim is one unit of dispatchable work.
//
// It replaces a queue group, which was never a broker feature this design has
// to reproduce: what a queue group did was deliver one message to exactly one
// of several competing consumers, and that is a row and a lock.
//
// ClaimedBy names the frontend REPLICA holding the claim, never the worker the
// work was handed to. That is the whole difference between this table and a
// lease on a worker: a worker that dies mid-verb makes the streaming RPC fail,
// which releases the claim through the ordinary settle path; it is the
// claiming REPLICA's death that leaves a row nobody is driving, and only the
// reap can answer that.
type WorkClaim struct {
	ID        string `gorm:"primaryKey;size:36"`
	Kind      string `gorm:"size:32;index"`
	Payload   []byte
	ClaimedBy string     `gorm:"size:64;index"`
	ClaimedAt *time.Time `gorm:"index"`
	Attempts  int
	// NotBefore is the earliest this row may be claimed again, stamped by
	// ReleaseClaim from the DATABASE clock. NULL means "now", which is what
	// every freshly enqueued row carries and what a reaped row goes back to.
	// See claimBackoff.
	NotBefore *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"index"`
}

// TableName pins the table this maps onto, because ReapAbandoned and ClaimNext
// name it in raw SQL and a gorm pluralisation change would leave the two
// spellings disagreeing at runtime rather than at build time.
func (WorkClaim) TableName() string { return claimsTable }

const claimsTable = "work_claims"

// ErrNoWork reports that no row of the requested kinds was available to claim.
// It is an ordinary emptiness and never an error about the database.
var ErrNoWork = errors.New("jobs: no unclaimed work")

// MigrateClaims creates the claim table.
//
// Under the same advisory lock every other schema migration in this deployment
// takes, because frontends and workers start at the same time and two
// concurrent AutoMigrate calls on one table race in PostgreSQL's catalog.
func MigrateClaims(ctx context.Context, db *gorm.DB) error {
	if err := advisorylock.WithLockCtx(ctx, db, advisorylock.KeySchemaMigrate, func() error {
		return db.WithContext(ctx).AutoMigrate(&WorkClaim{})
	}); err != nil {
		return fmt.Errorf("migrating the claim table: %w", err)
	}
	return nil
}

// requirePostgres refuses an operation whose statement only PostgreSQL runs.
//
// Stated once and called from every entry point that emits such a statement.
// The refusal matters more than the syntax error it prevents: SKIP LOCKED and
// make_interval fail on SQLite with a driver-level parse error that reads like
// a missing migration, and a claim queue that cannot claim must not be
// mistaken for one whose table is not there yet.
func requirePostgres(db *gorm.DB, op string) error {
	if db == nil {
		return fmt.Errorf("%s: no database handle", op)
	}
	if name := db.Dialector.Name(); !strings.Contains(name, "postgres") {
		return fmt.Errorf("%s: the claim queue requires PostgreSQL, this deployment runs on %q", op, name)
	}
	return nil
}

// EnqueueClaim writes one claim row and returns its id.
//
// It replaces a publish, and unlike a publish it cannot succeed while nothing
// is listening: a row with no dispatcher is a row that is still there.
//
// created_at is stamped by the DATABASE. It is the order competing replicas
// take work in, so it has to be measured on the one clock they all share; a
// Go-side time.Now() would make the queue order depend on which replica
// happened to enqueue.
func EnqueueClaim(ctx context.Context, db *gorm.DB, kind ClaimKind, payload any) (string, error) {
	if err := requirePostgres(db, "enqueueing work"); err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encoding the payload of a %s claim: %w", kind, err)
	}
	id := uuid.New().String()
	if err := db.WithContext(ctx).Model(&WorkClaim{}).Create(map[string]any{
		"id":         id,
		"kind":       string(kind),
		"payload":    raw,
		"claimed_by": "",
		"claimed_at": nil,
		"attempts":   0,
		"not_before": nil,
		"created_at": gorm.Expr("now()"),
	}).Error; err != nil {
		return "", fmt.Errorf("enqueueing a %s claim: %w", kind, err)
	}
	return id, nil
}

// claimNextSQL takes at most one unclaimed row in ONE statement.
//
// FOR UPDATE SKIP LOCKED is the whole mechanism. Without SKIP LOCKED two
// replicas polling at the same instant do not take two rows: the second BLOCKS
// on the first's row lock and only then re-evaluates, so a slow claimant stalls
// every other replica in the deployment behind it. Without FOR UPDATE at all
// they can both read the same unclaimed row and both write it, which is the
// exactly-once property gone.
//
// One statement rather than select-then-update for the same reason: the gap
// between the two is where two claimants both see an unclaimed row.
//
// claimed_at is stamped by the database, like created_at and for the same
// reason: the reap compares it against replica liveness, which is measured on
// that clock.
const claimNextSQL = `UPDATE ` + claimsTable + `
SET claimed_by = ?, claimed_at = now()
WHERE id = (
	SELECT id FROM ` + claimsTable + `
	WHERE claimed_at IS NULL AND kind IN ?
	  AND (not_before IS NULL OR not_before <= now())
	ORDER BY created_at, id
	FOR UPDATE SKIP LOCKED
	LIMIT 1
)
RETURNING id, kind, payload, claimed_by, claimed_at, attempts, not_before, created_at`

// ClaimNext takes at most one unclaimed row of any kind in kinds, marking it
// claimed by owner. It returns ErrNoWork when there is none.
func ClaimNext(ctx context.Context, db *gorm.DB, owner string, kinds []ClaimKind) (*WorkClaim, error) {
	if err := requirePostgres(db, "claiming work"); err != nil {
		return nil, err
	}
	if owner == "" {
		// A claim nobody owns cannot be reaped when its holder dies, because
		// the reap asks whether the OWNER is still live. Refused here rather
		// than written, since the symptom of an unowned claim is work that is
		// never retried and never completed.
		return nil, errors.New("claiming work: no owner named, so the claim could never be reaped")
	}
	if len(kinds) == 0 {
		return nil, ErrNoWork
	}
	wanted := make([]string, 0, len(kinds))
	for _, k := range kinds {
		wanted = append(wanted, string(k))
	}
	var claim WorkClaim
	res := db.WithContext(ctx).Raw(claimNextSQL, owner, wanted).Scan(&claim)
	if res.Error != nil {
		return nil, fmt.Errorf("claiming work as %q: %w", owner, res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrNoWork
	}
	return &claim, nil
}

// The retry schedule a released claim comes back on.
//
// There is no dead letter here, and that is a decision rather than an omission.
// Read settleClaim: the only outcome that reaches ReleaseClaim is one where
// NOTHING was learned about the work. No agent worker was connected, the tunnel
// broke, a peer could not be reached, the stream was refused before the request
// body left this replica. Not one of those is the worker saying it ran the job
// and it failed, and failing a job on any of them would report an absent
// connection as a worker's verdict, which is the one collapse this whole design
// exists to prevent. A deployment whose fleet is down for a day must run its
// queued work when the fleet comes back, not find it failed.
//
// What IS a defect is retrying at the poll interval for ever. At the default
// two seconds a permanently undispatchable row costs ~43000 UPDATEs a day, and
// it costs more than writes: rows are claimed oldest-first, so the oldest
// stuck row is re-claimed ahead of every newer one on every single tick and
// holds a dispatch slot while it fails. One poison row starves the queue behind
// it.
//
// So the retry is unbounded and the RATE is not. Each release stamps the row
// with the earliest it may be claimed again, doubling per attempt from
// claimBackoffBase up to claimBackoffCap. A first failure costs the base delay,
// a fleet that has been down for a minute retries at the cap, and work becomes
// claimable again within one cap of the fleet returning. The exponent is
// clamped so the arithmetic cannot overflow however long a row has been stuck.
const (
	// claimBackoffBase is the delay after the first failed dispatch. One poll
	// interval: a row that failed because nothing was connected should not be
	// re-tried before the loop would have looked again anyway.
	claimBackoffBase = 2 * time.Second
	// claimBackoffCap bounds the delay, and with it how long queued work waits
	// after a fleet comes back. Kept short for that reason: the point of the
	// backoff is to stop a stuck row from spinning, not to give up on it.
	claimBackoffCap = 60 * time.Second
	// claimBackoffMaxShift clamps the exponent. 2^16 base seconds is already
	// far past the cap, so this only keeps power() away from infinity for a row
	// that has been retried for months.
	claimBackoffMaxShift = 16
)

// releaseClaimSQL returns a row to the pool and stamps its next eligibility.
//
// One statement, and the delay computed IN the statement, because it reads the
// row's own attempts count and stamps now() from the DATABASE clock. Competing
// replicas order the queue on that clock; a Go-side deadline would make how
// long a row waits depend on the clock skew of whichever replica happened to
// fail it.
const releaseClaimSQL = `UPDATE ` + claimsTable + `
SET claimed_by = '', claimed_at = NULL, attempts = attempts + 1,
    not_before = now() + make_interval(secs => LEAST(?, ? * power(2, LEAST(attempts, ?))))
WHERE id = ?`

// ReleaseClaim returns a row to the pool, incrementing Attempts and stamping
// the backoff described above.
//
// It is what a TRANSPORT failure does: nothing was learned about the work, so
// it must be retried, possibly by another replica against another worker.
func ReleaseClaim(ctx context.Context, db *gorm.DB, id string) error {
	if err := requirePostgres(db, "releasing a claim"); err != nil {
		return err
	}
	res := db.WithContext(ctx).Exec(releaseClaimSQL,
		claimBackoffCap.Seconds(), claimBackoffBase.Seconds(), claimBackoffMaxShift, id)
	if res.Error != nil {
		return fmt.Errorf("releasing claim %q: %w", id, res.Error)
	}
	return nil
}

// CompleteClaim deletes the row.
//
// It is what a WORKER'S ANSWER does, success or failure: the worker ran the
// work and said what happened, so it must not run again.
func CompleteClaim(ctx context.Context, db *gorm.DB, id string) error {
	if db == nil {
		return errors.New("completing a claim: no database handle")
	}
	if err := db.WithContext(ctx).Where("id = ?", id).Delete(&WorkClaim{}).Error; err != nil {
		return fmt.Errorf("completing claim %q: %w", id, err)
	}
	return nil
}

// reapAbandonedSQL releases every claim whose owner is no longer a live replica.
//
// The predicate is REPLICA ABSENCE and deliberately not claim age, and that is
// the whole of this task's invariant applied to work. A claim held for an hour
// by a replica that is heartbeating is a slow job, and stealing it would run
// the work twice; a claim held for a second by a replica that is gone is work
// nobody is driving. Age cannot tell those apart and liveness can, so the age
// term that a lease design would carry is absent on purpose: there is no window
// after which a live replica's work is taken away from it.
//
// It reuses cluster.LiveInstanceIDsSQL rather than restating what "live" means,
// so this reap and every other reader of replica absence in the deployment move
// together, and it runs as ONE statement so a replica cannot die, or come back,
// between deciding who is live and acting on it.
// It stamps NO backoff, unlike ReleaseClaim. A claim abandoned because its
// replica died was never dispatched anywhere: there is nothing to back off
// from, and delaying it would punish the work for the death of the process that
// held it. It becomes claimable at once, by whichever replica polls next.
const reapAbandonedSQL = `UPDATE ` + claimsTable + `
SET claimed_by = '', claimed_at = NULL, attempts = attempts + 1, not_before = NULL
WHERE claimed_at IS NOT NULL
  AND claimed_by NOT IN (` + cluster.LiveInstanceIDsSQL + `)`

// ReapAbandoned releases the claims held by replicas that are no longer live
// within the given window, and returns how many it released.
//
// The window is the replica-liveness window, cluster.InstanceLiveness in
// production. It is NOT a timeout on the work: see reapAbandonedSQL.
func ReapAbandoned(ctx context.Context, db *gorm.DB, liveness time.Duration) (int64, error) {
	if err := requirePostgres(db, "reaping abandoned claims"); err != nil {
		return 0, err
	}
	res := db.WithContext(ctx).Exec(reapAbandonedSQL, liveness.Seconds())
	if res.Error != nil {
		return 0, fmt.Errorf("reaping abandoned claims: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ownerIsLiveSQL asks whether ONE named replica is currently live.
//
// The same predicate the reap uses, read the same way, because a replica that
// would not survive the reap must not be allowed to claim in the first place:
// its claim would be indistinguishable from one held by a dead replica and
// another replica would take the work away from it while it was still running.
const ownerIsLiveSQL = `SELECT count(*) FROM (` + cluster.LiveInstanceIDsSQL + ` AND instances.id = ?) AS live`

// OwnerIsLive reports whether owner is a replica this deployment currently
// considers alive.
//
// It exists for the dispatch loop's own guard. A replica that is not in the
// instances table at all (core/application/distributed.go leaves membership
// unstarted when no peer-reachable address can be derived) answers false, which
// is the honest answer: nothing about it is observable to its peers, so nothing
// can tell its claims from abandoned ones.
func OwnerIsLive(ctx context.Context, db *gorm.DB, owner string, liveness time.Duration) (bool, error) {
	if err := requirePostgres(db, "checking whether this replica is live"); err != nil {
		return false, err
	}
	var n int64
	if err := db.WithContext(ctx).Raw(ownerIsLiveSQL, liveness.Seconds(), owner).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("checking whether replica %q is live: %w", owner, err)
	}
	return n > 0, nil
}
