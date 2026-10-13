package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// The claim queue is the WorkQueue of the carrier that has no broker. A queue
// group did one thing: it delivered a message to one of several competing
// consumers. A row and a lock do the same, and the row stays until someone has
// done the work, which a published message did not.
//
// The states of a row:
//
//	pending  -> claimed   a replica took it (ClaimNext)
//	claimed  -> pending   the work got no answer (ReleaseClaim), or the replica
//	                      that held it is gone (ReapAbandoned)
//	claimed  -> deleted   the work was answered (CompleteClaim)
//	pending  -> migrated  a change of carrier took it out of the queue (MigratePending)
const (
	ClaimPending  = "pending"
	ClaimClaimed  = "claimed"
	ClaimMigrated = "migrated"
)

// WorkClaim is one unit of queued work.
//
// ClaimedBy names the frontend replica that holds the claim, never the worker
// that the work was handed to. A worker that dies in the middle of a run breaks
// the stream that the replica is reading, and the replica settles the claim. It
// is the death of the replica that leaves a row nobody drives, and only the reap
// answers that.
type WorkClaim struct {
	ID        string `gorm:"primaryKey;size:36"`
	Kind      string `gorm:"size:32;index:idx_work_claims_pick,priority:1"`
	State     string `gorm:"size:16;not null;default:pending;index:idx_work_claims_pick,priority:2"`
	Payload   []byte
	ClaimedBy string `gorm:"size:64;index"`
	ClaimedAt *time.Time
	// Attempts counts the times the row went back to the pool. It also fences:
	// a holder that was reaped and comes back with its answer carries a count
	// that no longer matches the row, so it cannot settle the claim of the
	// replica that took the work over.
	Attempts int
	// Failures counts the releases that followed a failure which says something
	// about the work itself: a worker was reached and the run failed on it. A
	// release for lack of a worker, a route or a free slot does not count. The
	// count is what ends a row that can never be run (see
	// ClaimConsumerConfig.MaxFailures).
	Failures int
	// NotBefore is the earliest the row may be claimed again. The database clock
	// stamps it. NULL means now.
	NotBefore *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"index:idx_work_claims_pick,priority:3"`
}

// TableName pins the table, because the statements below name it in raw SQL and a
// change of the naming rule of gorm would leave the two spellings disagreeing at
// run time and not at build time.
func (WorkClaim) TableName() string { return claimsTable }

const claimsTable = "work_claims"

// ErrNoWork reports that no row of the kind was there to claim. It is an
// ordinary emptiness and never an error about the database.
var ErrNoWork = errors.New("jobs: no unclaimed work")

// MigrateClaims creates the claim table, under the lock that every schema
// migration of the deployment takes, because frontends start together and two
// concurrent AutoMigrate calls on one table race in the catalog.
func MigrateClaims(ctx context.Context, db *gorm.DB) error {
	if err := advisorylock.WithLockCtx(ctx, db, advisorylock.KeySchemaMigrate, func() error {
		return db.WithContext(ctx).AutoMigrate(&WorkClaim{})
	}); err != nil {
		return fmt.Errorf("migrating the claim table: %w", err)
	}
	return nil
}

// requirePostgres refuses an operation whose statement only PostgreSQL runs. The
// refusal matters more than the syntax error it replaces: SKIP LOCKED and
// make_interval fail on SQLite with a parse error that reads like a missing
// migration, and a queue that cannot claim must not be mistaken for one whose
// table is not there yet.
func requirePostgres(db *gorm.DB, op string) error {
	if db == nil {
		return fmt.Errorf("%s: no database handle", op)
	}
	if name := db.Dialector.Name(); !strings.Contains(name, "postgres") {
		return fmt.Errorf("%s: the claim queue requires PostgreSQL, this deployment runs on %q", op, name)
	}
	return nil
}

// knownKind reports whether kind is one of the work kinds. A row of another kind
// would be one that no consumer ever asks for.
func knownKind(kind messaging.WorkKind) bool {
	switch kind {
	case messaging.WorkTask, messaging.WorkMCPCI, messaging.WorkAgentRun:
		return true
	}
	return false
}

// EnqueueClaim writes one pending row and returns its id.
//
// created_at is stamped by the database. It is the order in which competing
// replicas take work, so it is measured on the one clock they share; a time taken
// in Go would make the order depend on which replica enqueued.
func EnqueueClaim(ctx context.Context, db *gorm.DB, kind messaging.WorkKind, payload any) (string, error) {
	if err := requirePostgres(db, "enqueueing work"); err != nil {
		return "", err
	}
	if !knownKind(kind) {
		return "", fmt.Errorf("enqueueing work: unknown work kind %q", kind)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encoding the payload of a %s claim: %w", kind, err)
	}
	if err := messaging.CheckWorkSize(kind, len(raw)); err != nil {
		return "", err
	}
	id := uuid.New().String()
	if err := db.WithContext(ctx).Model(&WorkClaim{}).Create(map[string]any{
		"id":         id,
		"kind":       string(kind),
		"state":      ClaimPending,
		"payload":    raw,
		"claimed_by": "",
		"attempts":   0,
		"failures":   0,
		"created_at": gorm.Expr("now()"),
	}).Error; err != nil {
		return "", fmt.Errorf("enqueueing a %s claim: %w", kind, err)
	}
	return id, nil
}

// claimNextSQL takes at most one pending row of a kind in one statement.
//
// FOR UPDATE SKIP LOCKED is the whole mechanism. Without SKIP LOCKED two
// replicas that poll together do not take two rows: the second blocks on the
// lock of the first and then looks again, so a slow claimant stalls every other
// replica. Without FOR UPDATE they can both read one row and both write it.
// It is one statement and not a select followed by an update for the same reason:
// the gap between the two is where two claimants see one row.
//
// claimed_at is stamped by the database, like created_at.
const claimNextSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimClaimed + `', claimed_by = ?, claimed_at = now()
WHERE id = (
	SELECT id FROM ` + claimsTable + `
	WHERE state = '` + ClaimPending + `' AND kind = ?
	  AND (not_before IS NULL OR not_before <= now())
	ORDER BY created_at, id
	FOR UPDATE SKIP LOCKED
	LIMIT 1
)
RETURNING id, kind, state, payload, claimed_by, claimed_at, attempts, failures, not_before, created_at`

// ClaimNext takes the oldest pending row of kind and marks it as held by owner.
// It returns ErrNoWork when there is none.
func ClaimNext(ctx context.Context, db *gorm.DB, owner string, kind messaging.WorkKind) (*WorkClaim, error) {
	if err := requirePostgres(db, "claiming work"); err != nil {
		return nil, err
	}
	if owner == "" {
		// A claim nobody owns cannot be reaped when its holder dies, because the
		// reap asks whether the owner is live. The symptom of such a claim is
		// work that is never retried and never answered.
		return nil, errors.New("claiming work: no owner named, so the claim could never be reaped")
	}
	var claim WorkClaim
	res := db.WithContext(ctx).Raw(claimNextSQL, owner, string(kind)).Scan(&claim)
	if res.Error != nil {
		return nil, fmt.Errorf("claiming work as %q: %w", owner, res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrNoWork
	}
	return &claim, nil
}

// The wait before a row that got no answer may be claimed again.
//
// There is no dead letter, and that is a decision. The only outcome that reaches
// ReleaseClaim is one where nothing was learned about the work: no agent worker
// was connected, the tunnel broke, a peer could not be reached. None of them is
// the worker saying that it ran the job and the job failed, and failing a job on
// any of them would report a missing connection as the verdict of a worker. A
// fleet that is down for a day must run its queued work when it comes back, and
// not find it failed.
//
// What is a defect is retrying at the poll interval for ever. At two seconds a
// row that can never be dispatched costs 43000 updates a day. Rows go oldest
// first, so it would also be claimed again ahead of every newer row on every
// tick and hold a slot while it fails. So the retry has no end and its rate has
// one: each release stamps the earliest time of the next claim, doubling from
// claimBackoffBase to claimBackoffCap.
const (
	// claimBackoffBase is the wait after the first release.
	claimBackoffBase = 2 * time.Second
	// claimBackoffCap bounds the wait, and with it how long queued work waits
	// after a fleet comes back.
	claimBackoffCap = 60 * time.Second
	// claimBackoffMaxShift clamps the exponent, so that power() stays away from
	// infinity for a row that has been retried for months.
	claimBackoffMaxShift = 16
)

// releaseClaimSQL returns a row to the pool and stamps its next eligibility, in
// one statement and from the clock of the database. The row is matched on its
// holder and its attempt count, so a holder that was reaped cannot release the
// claim of the replica that took over.
const releaseClaimSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimPending + `', claimed_by = '', claimed_at = NULL, attempts = attempts + 1,
    not_before = now() + make_interval(secs => LEAST(?, ? * power(2, LEAST(attempts, ?))))
WHERE id = ? AND state = '` + ClaimClaimed + `' AND claimed_by = ? AND attempts = ?`

// ReleaseClaim returns a claim to the pool with one more attempt and a wait. It
// is what a failure does that says nothing about the work. It reports false when
// the claim was no longer the holder's, which is not an error: another replica
// has the work now.
func ReleaseClaim(ctx context.Context, db *gorm.DB, claim *WorkClaim) (bool, error) {
	if err := requirePostgres(db, "releasing a claim"); err != nil {
		return false, err
	}
	res := db.WithContext(ctx).Exec(releaseClaimSQL,
		claimBackoffCap.Seconds(), claimBackoffBase.Seconds(), claimBackoffMaxShift,
		claim.ID, claim.ClaimedBy, claim.Attempts)
	if res.Error != nil {
		return false, fmt.Errorf("releasing claim %q: %w", claim.ID, res.Error)
	}
	return res.RowsAffected == 1, nil
}

// releaseClaimFailedSQL is releaseClaimSQL for a failure that says something
// about the work, which it counts.
const releaseClaimFailedSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimPending + `', claimed_by = '', claimed_at = NULL, attempts = attempts + 1, failures = failures + 1,
    not_before = now() + make_interval(secs => LEAST(?, ? * power(2, LEAST(attempts, ?))))
WHERE id = ? AND state = '` + ClaimClaimed + `' AND claimed_by = ? AND attempts = ?`

// ReleaseClaimFailed is ReleaseClaim for a run that reached a worker and failed
// there. It counts the failure, so that a row which fails every time can be ended.
func ReleaseClaimFailed(ctx context.Context, db *gorm.DB, claim *WorkClaim) (bool, error) {
	if err := requirePostgres(db, "releasing a claim"); err != nil {
		return false, err
	}
	res := db.WithContext(ctx).Exec(releaseClaimFailedSQL,
		claimBackoffCap.Seconds(), claimBackoffBase.Seconds(), claimBackoffMaxShift,
		claim.ID, claim.ClaimedBy, claim.Attempts)
	if res.Error != nil {
		return false, fmt.Errorf("releasing claim %q: %w", claim.ID, res.Error)
	}
	return res.RowsAffected == 1, nil
}

// releaseOwnedSQL returns the rows of a kind that a replica holds to the pool, with
// no wait.
const releaseOwnedSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimPending + `', claimed_by = '', claimed_at = NULL, attempts = attempts + 1, not_before = NULL
WHERE state = '` + ClaimClaimed + `' AND claimed_by = ? AND kind = ?`

// ReleaseOwned returns every claim of kind that owner holds to the pool. A
// process that starts to claim calls it first: a row that its own id holds was
// held by an earlier process with that id, which is gone. The reap cannot see
// that, because the id is live again, and the row would be held for ever.
func ReleaseOwned(ctx context.Context, db *gorm.DB, owner string, kind messaging.WorkKind) (int64, error) {
	if err := requirePostgres(db, "releasing the claims of this replica"); err != nil {
		return 0, err
	}
	res := db.WithContext(ctx).Exec(releaseOwnedSQL, owner, string(kind))
	if res.Error != nil {
		return 0, fmt.Errorf("releasing the claims held by %q: %w", owner, res.Error)
	}
	return res.RowsAffected, nil
}

// HeldClaim names a claim that a replica holds now.
type HeldClaim struct {
	ID       string
	Attempts int
}

// HeldBy lists the claims that owner holds. A run in flight whose claim is not in
// the list has lost it: a reap gave the work to another replica.
func HeldBy(ctx context.Context, db *gorm.DB, owner string) ([]HeldClaim, error) {
	if err := requirePostgres(db, "listing the claims of this replica"); err != nil {
		return nil, err
	}
	var held []HeldClaim
	if err := db.WithContext(ctx).Model(&WorkClaim{}).
		Select("id, attempts").
		Where("state = ? AND claimed_by = ?", ClaimClaimed, owner).
		Scan(&held).Error; err != nil {
		return nil, fmt.Errorf("listing the claims held by %q: %w", owner, err)
	}
	return held, nil
}

// CompleteClaim deletes the row. It is what an answer of the worker does,
// success or failure: the work ran and said what happened, so it must not run
// again. It reports false when the claim was no longer the holder's.
func CompleteClaim(ctx context.Context, db *gorm.DB, claim *WorkClaim) (bool, error) {
	if db == nil {
		return false, errors.New("completing a claim: no database handle")
	}
	res := db.WithContext(ctx).
		Where("id = ? AND state = ? AND claimed_by = ? AND attempts = ?", claim.ID, ClaimClaimed, claim.ClaimedBy, claim.Attempts).
		Delete(&WorkClaim{})
	if res.Error != nil {
		return false, fmt.Errorf("completing claim %q: %w", claim.ID, res.Error)
	}
	return res.RowsAffected == 1, nil
}

// reapAbandonedSQL releases every claim whose holder is not a live replica.
//
// The test is the absence of the replica and not the age of the claim. A claim
// that a heartbeating replica has held for an hour is a slow job, and taking it
// would run the work twice. A claim that a dead replica held for a second is work
// nobody drives. Age cannot tell the two apart, so there is no age in the
// statement: no time after which a live replica loses its work.
//
// It reads cluster.LiveInstanceIDsSQL and does not restate what live means, so
// this reap and every other reader of the absence of a replica move together. It
// is one statement, so a replica cannot die or return between the decision on who
// is live and the act. It stamps no wait: work whose replica died was never
// dispatched anywhere and has nothing to back off from.
const reapAbandonedSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimPending + `', claimed_by = '', claimed_at = NULL, attempts = attempts + 1, not_before = NULL
WHERE state = '` + ClaimClaimed + `'
  AND claimed_by NOT IN (` + cluster.LiveInstanceIDsSQL + `)`

// ReapAbandoned returns the claims of replicas that are no longer live to the
// pool, and says how many. liveness is the window of cluster.InstanceLiveness. It
// is not a timeout on the work.
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

// ownerIsLiveSQL asks whether one replica is live, with the predicate of the
// reap. A replica that the reap would not keep must not claim: its claims would
// look like those of a dead replica, and another replica would take the work
// while it still runs.
const ownerIsLiveSQL = `SELECT count(*) FROM (` + cluster.LiveInstanceIDsSQL + ` AND instances.id = ?) AS live`

// OwnerIsLive reports whether owner is a replica that the deployment considers
// live. A replica with no row in the instances table answers false, which is
// the honest answer: nothing about it is visible to its peers.
func OwnerIsLive(ctx context.Context, db *gorm.DB, owner string, liveness time.Duration) (bool, error) {
	if err := requirePostgres(db, "checking whether this replica is live"); err != nil {
		return false, err
	}
	var n int64
	if err := db.WithContext(ctx).Raw(ownerIsLiveSQL, liveness.Seconds(), owner).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("checking whether %q is live: %w", owner, err)
	}
	return n > 0, nil
}

// migratePendingSQL takes the pending rows out of the queue. Rows that a replica
// holds are not touched: the replica drives them to their end where they are.
//
// claimed_at holds the time of the sweep, for the purge of the rows that stay
// behind.
const migratePendingSQL = `UPDATE ` + claimsTable + `
SET state = '` + ClaimMigrated + `', claimed_at = now()
WHERE state = '` + ClaimPending + `'
RETURNING id, kind, state, payload, claimed_by, claimed_at, attempts, failures, not_before, created_at`

// MigratePending takes every pending row out of the queue and returns it, for a
// change to a carrier that has its own queue. A row is migrated once: the
// statement is a compare-and-set on the state, so it never goes to a claimant and
// to the sweep, and a second sweep returns nothing.
//
// The caller publishes the rows after this returns, and puts back the ones it
// could not publish (RestorePending). A sweeper that dies between the two loses
// the rows, as the queue of the new carrier loses them, and the alternative, to
// publish first, would run a job twice.
func MigratePending(ctx context.Context, db *gorm.DB) ([]*WorkClaim, error) {
	if err := requirePostgres(db, "migrating pending work"); err != nil {
		return nil, err
	}
	var rows []*WorkClaim
	if err := db.WithContext(ctx).Raw(migratePendingSQL).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("migrating pending work: %w", err)
	}
	return rows, nil
}

// RestorePending puts migrated rows back in the queue. It is for the rows that a
// change of carrier took out and could not hand to the new carrier: they wait in
// the table, and the next sweep takes them again, and none is lost.
func RestorePending(ctx context.Context, db *gorm.DB, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := requirePostgres(db, "restoring migrated work"); err != nil {
		return err
	}
	res := db.WithContext(ctx).Model(&WorkClaim{}).
		Where("id IN ? AND state = ?", ids, ClaimMigrated).
		Updates(map[string]any{"state": ClaimPending, "claimed_at": nil})
	if res.Error != nil {
		return fmt.Errorf("restoring migrated work: %w", res.Error)
	}
	return nil
}

// MigratedRetention is how long a row that a change of carrier moved stays in the
// table. It is only evidence of what moved. The payload of a row is the whole
// request of the work, so it does not stay for ever.
const MigratedRetention = 24 * time.Hour

// PurgeMigrated deletes the rows that were migrated more than olderThan ago, with
// their payloads, and says how many.
func PurgeMigrated(ctx context.Context, db *gorm.DB, olderThan time.Duration) (int64, error) {
	if err := requirePostgres(db, "purging migrated work"); err != nil {
		return 0, err
	}
	res := db.WithContext(ctx).Exec(`DELETE FROM `+claimsTable+
		` WHERE state = ? AND COALESCE(claimed_at, created_at) < now() - make_interval(secs => ?)`,
		ClaimMigrated, olderThan.Seconds())
	if res.Error != nil {
		return 0, fmt.Errorf("purging migrated work: %w", res.Error)
	}
	return res.RowsAffected, nil
}
