package nodes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Cold-load job states. `pending` covers node selection and replica
// allocation, which report nothing a waiter could act on; the rest name the
// phase the load is actually in. There is no terminal `ready` state — a
// successful job deletes its row and leaves the NodeModel row as the record.
const (
	LoadJobStatePending    = "pending"
	LoadJobStateInstalling = "installing"
	LoadJobStateStaging    = "staging"
	LoadJobStateLoading    = "loading"
	LoadJobStateFailed     = "failed"
)

const (
	// loadJobHeartbeatInterval is how often a running job touches LastProgress.
	// It matches the staging broadcast debounce so a job writes at most one row
	// per second regardless of how many 32 KB chunks land in it.
	loadJobHeartbeatInterval = stagingBroadcastInterval

	// loadJobLeaseTTL is how long a job's lease lasts after each renewal. The
	// owner renews on every heartbeat, so it has about thirty chances. A frontend
	// under GC pressure or a slow database write must not have a healthy
	// multi-GB transfer taken from it, and a dead one must not hold a model for
	// long.
	loadJobLeaseTTL = 30 * time.Second

	// loadJobStopWindow is how long a failed job keeps its model when remote
	// work may still run: the lease, a worker-side bound, and a margin. After it
	// the job is released. A silent worker therefore delays a retry by a bounded
	// time and never blocks the model for ever.
	loadJobStopWindow = 150 * time.Second

	// loadJobLegacyStopWindow is the stop window for a worker that cannot confirm
	// a stop or watch operations: the longest a load RPC may run. Such a worker
	// gives no sooner bound, so the model is held no longer than the load itself
	// could have run, which is what a stuck load cost before leases existed.
	loadJobLegacyStopWindow = 45 * time.Minute

	// loadJobFailureReport is how long a failure is kept when the work is known
	// to have ended. It exists so waiters and callers that arrive right after
	// the failure read the real cause instead of starting a duplicate load of a
	// model that just failed.
	loadJobFailureReport = 15 * time.Second

	// loadJobPollInterval is how often a waiter on a non-owning replica polls
	// the job row. The DB is the authority: NATS staging broadcasts are
	// fire-and-forget, so a missed terminal event must not strand a waiter.
	loadJobPollInterval = 2 * time.Second
)

// loadJobLockPrefix namespaces the per-model advisory lock key. It is the same
// key the whole cold load used to hold; only the guarded section changed.
const loadJobLockPrefix = "model-load:"

var (
	replicaIDOnce  sync.Once
	replicaIDValue string
)

// ReplicaID returns this process's identity, generated once at startup and held
// for the process lifetime. It is recorded on jobs for diagnostics only, never
// for correctness decisions: a replica cannot be assumed alive just because its
// ID is on a row, which is what the LastProgress heartbeat is for.
func ReplicaID() string {
	replicaIDOnce.Do(func() { replicaIDValue = uuid.New().String() })
	return replicaIDValue
}

// Progress returns overall completion as a percentage, or 0 when the job has
// not reported enough to compute one.
func (j *ModelLoadJob) Progress() float64 {
	if j.TotalBytes <= 0 {
		return 0
	}
	filePct := float64(j.BytesSent) / float64(j.TotalBytes) * 100
	if j.TotalFiles <= 1 || j.FileIndex <= 0 {
		return filePct
	}
	return (float64(j.FileIndex-1)*100 + filePct) / float64(j.TotalFiles)
}

// ETA returns the estimated time remaining for the transfer, and false when the
// job has not moved enough bytes for the observed rate to mean anything. A
// confidently wrong ETA on a twenty-minute wait is worse than none, so this
// omits rather than guesses.
func (j *ModelLoadJob) ETA(now time.Time) (time.Duration, bool) {
	if j.State != LoadJobStateStaging || j.BytesSent <= 0 || j.TotalBytes <= j.BytesSent {
		return 0, false
	}
	if j.StartedAt.IsZero() {
		return 0, false
	}
	elapsed := now.Sub(j.StartedAt)
	if elapsed < loadJobHeartbeatInterval {
		return 0, false
	}
	rate := float64(j.BytesSent) / elapsed.Seconds()
	if rate <= 0 {
		return 0, false
	}
	return time.Duration(float64(j.TotalBytes-j.BytesSent)/rate) * time.Second, true
}

// LoadJobRef names one attempt to load a model. TrackingKey alone is not
// enough: a replica may delete a job and another may claim the same model, and
// a slow writer from the first attempt must not act on the second.
type LoadJobRef struct{ TrackingKey, Generation string }

// Ref returns the attempt this row records.
func (j *ModelLoadJob) Ref() LoadJobRef { return LoadJobRef{j.TrackingKey, j.Generation} }

// ErrStaleLoadJob means the attempt no longer owns its job: the row is gone,
// failed, or now belongs to another generation. An owner that sees it must stop.
var ErrStaleLoadJob = errors.New("stale model load job ownership")

// ErrLoadLeaseExpired means the owner could not extend its lease before the
// lease it last held ran out. The owner stops its own work: it must not outlive
// a lease it can no longer extend.
var ErrLoadLeaseExpired = errors.New("model load job lease expired")

// ErrLoadOperationLost means the worker no longer knows the load's operation: it
// restarted, or its watchdog ended it. The work is gone and the load cannot
// finish.
var ErrLoadOperationLost = errors.New("the worker lost the load operation")

// loadJobResult turns the outcome of a write on one job row into an error.
// Zero rows is not a database failure: it is the proof that this attempt lost
// the job.
func loadJobResult(res *gorm.DB) error {
	if res.Error != nil {
		return fmt.Errorf("writing model load job: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return ErrStaleLoadJob
	}
	return nil
}

// ownedLoadJobOn is the only place a query on model_load_jobs starts. Every
// update and delete of a job goes through it, so every write carries the
// generation predicate.
func ownedLoadJobOn(db *gorm.DB, ref LoadJobRef) *gorm.DB {
	return db.Model(&ModelLoadJob{}).
		Where("tracking_key = ? AND generation = ?", ref.TrackingKey, ref.Generation)
}

func (r *NodeRegistry) ownedLoadJob(ctx context.Context, ref LoadJobRef) *gorm.DB {
	return ownedLoadJobOn(r.db.WithContext(ctx), ref)
}

// now is the frontend clock. It stamps display fields only. Whether a lease
// has expired is always decided by the database clock (dbNow), so a wrong
// frontend clock cannot expire a live lease or keep a dead one.
func (r *NodeRegistry) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}

// dbNow is the clock every lease and deadline comparison uses. On PostgreSQL
// it is the database's own now(). SQLite has no clock of its own to speak of: it
// serves one process, so that process's clock is the database clock.
func (r *NodeRegistry) dbNow() clause.Expr {
	if r.db.Dialector.Name() == "postgres" {
		return gorm.Expr("now()")
	}
	return gorm.Expr("?", time.Now())
}

// dbAfter is a deadline d after dbNow.
func (r *NodeRegistry) dbAfter(d time.Duration) clause.Expr {
	if r.db.Dialector.Name() == "postgres" {
		return gorm.Expr("now() + make_interval(secs => ?)", d.Seconds())
	}
	return gorm.Expr("?", time.Now().Add(d))
}

func (r *NodeRegistry) leaseExpr() clause.Expr {
	ttl := loadJobLeaseTTL
	if r.leaseTTL > 0 {
		ttl = r.leaseTTL
	}
	return r.dbAfter(ttl)
}

// activeLoadJob limits a write to an attempt that has not failed. A failed row
// only changes through its own grace-gated release.
func activeLoadJob(q *gorm.DB) *gorm.DB {
	return q.Where("state <> ?", LoadJobStateFailed)
}

// A ref with no generation names a legacy row. Nothing that runs today can own
// one, so every owner write rejects it before it reaches the database.
func (ref LoadJobRef) owned() bool { return ref.Generation != "" }

// backfillLoadJobGenerations gives every row written before the generation
// column existed a generation of its own. The lease and stop deadline are left
// empty on purpose. A running row with no lease counts as expired, because no
// old binary renews one, and a failed row with no stop deadline gets its window
// the first time a claim or a sweep sees it. Rows an old binary writes after
// this runs follow the same path.
//
// The UUIDs are generated here, not by the database, so the migration runs the
// same on PostgreSQL and SQLite. Each write names the empty generation it
// observed, so a row another frontend already upgraded is left alone.
func backfillLoadJobGenerations(ctx context.Context, db *gorm.DB) error {
	const batch = 100
	for {
		var legacy []ModelLoadJob
		if err := db.WithContext(ctx).Where("generation = ?", "").Limit(batch).Find(&legacy).Error; err != nil {
			return err
		}
		if len(legacy) == 0 {
			return nil
		}
		for _, row := range legacy {
			if err := ownedLoadJobOn(db.WithContext(ctx), row.Ref()).Update("generation", uuid.NewString()).Error; err != nil {
				return err
			}
		}
	}
}

type (
	loadOwnershipKey struct{}
	loadPathKey      struct{}
)

// ErrLoadOwnershipMissing means a write on the load path carried no generation.
// The write is refused: a load that cannot say which attempt it belongs to must
// not publish anything.
var ErrLoadOwnershipMissing = errors.New("load path write without load job ownership")

// withLoadOwnership attaches the attempt a load runs for. Registry writes made
// with the returned context are fenced on that attempt.
func withLoadOwnership(ctx context.Context, ref LoadJobRef) context.Context {
	return context.WithValue(ctx, loadOwnershipKey{}, ref)
}

// withLoadPath marks a context as running a distributed cold load. On that path
// a missing ownership value is a bug, not a caller that has nothing to fence.
func withLoadPath(ctx context.Context) context.Context {
	return context.WithValue(ctx, loadPathKey{}, true)
}

// requireLoadOwnership checks, inside the transaction that publishes a replica,
// that the attempt still owns its job. The no-op update holds the job row lock
// until the publish commits, so the owner cannot lose the job between the check
// and the write. A load-path context with no ownership fails closed. A context
// that is not on the load path (routing, health checks, tests) has nothing to
// fence and passes.
func requireLoadOwnership(ctx context.Context, tx *gorm.DB) error {
	return requireLoadOwnershipFor(ctx, tx, false)
}

// requireLoadOwnershipFor is requireLoadOwnership with a choice about a failed
// attempt. Publishing needs a live attempt. Removing the attempt's own replica
// row does not: a cancelled or failed attempt must still clean up after itself,
// and the generation still stops it from touching a successor's row.
func requireLoadOwnershipFor(ctx context.Context, tx *gorm.DB, allowFailed bool) error {
	ref, ok := ctx.Value(loadOwnershipKey{}).(LoadJobRef)
	if !ok {
		if ctx.Value(loadPathKey{}) != nil {
			return ErrLoadOwnershipMissing
		}
		return nil
	}
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	q := ownedLoadJobOn(tx, ref)
	if !allowFailed {
		q = activeLoadJob(q)
	}
	return loadJobResult(q.UpdateColumn("generation", ref.Generation))
}

// LoadJobUpdate is a partial update to a running job. Empty node fields are
// left untouched so a heartbeat does not erase the placement the runner
// reported earlier.
type LoadJobUpdate struct {
	State        string
	NodeID       string
	NodeName     string
	ReplicaIndex int
	BytesSent    int64
	TotalBytes   int64
	FileIndex    int
	TotalFiles   int
	// StartedAt anchors the rate the ETA is derived from. Set by the runner the
	// first time the transfer reports bytes; zero leaves the stored value alone.
	StartedAt time.Time
	// LegacyWorker records that the worker cannot name operations.
	LegacyWorker bool
}

// claimRow is a job row together with the database's verdict on its deadlines.
type claimRow struct {
	ModelLoadJob
	LeaseExpired bool
	StopPassed   bool
}

// ClaimLoadJob decides, under the per-model advisory lock, whether this replica
// owns the cold load of trackingKey. It returns claimed=true with a fresh
// `pending` job when this replica took the work, and claimed=false with the
// existing job otherwise. The rules, all judged by the database clock:
//
//  1. No row: insert.
//  2. Running, lease live: return it. The caller waits.
//  3. Running, lease expired: mark it failed with a stop window and return it.
//     The caller retries once the window ends.
//  4. Failed, stop window over: delete it and insert a new generation.
//  5. Failed, window still open: return it with its cause.
//
// A row with no lease (written by an older binary) counts as expired.
//
// The lock is held only across these statements — no network, file, or gRPC I/O
// happens inside it, which is the entire point of the job row. The primary key
// on TrackingKey is the real guard: if the lock were somehow bypassed the
// INSERT fails rather than producing two loaders.
func (r *NodeRegistry) ClaimLoadJob(ctx context.Context, trackingKey, owner string) (*ModelLoadJob, bool, error) {
	var (
		job     *ModelLoadJob
		claimed bool
	)
	lockKey := advisorylock.KeyFromString(loadJobLockPrefix + trackingKey)
	err := advisorylock.WithLockCtx(ctx, r.db, lockKey, func() error {
		row, found, err := r.readClaimRow(ctx, trackingKey)
		if err != nil {
			return err
		}
		if found {
			existing := row.ModelLoadJob
			switch {
			case existing.State == LoadJobStateFailed && existing.StopDeadline == nil:
				// Written by an older binary: give it the window it never got.
				if err := r.setStopWindow(ctx, existing.Ref()); err != nil {
					return err
				}
				job, claimed = r.rereadOr(ctx, trackingKey, &existing), false
				return nil
			case existing.State == LoadJobStateFailed && !row.StopPassed:
				job, claimed = &existing, false
				return nil
			case existing.State != LoadJobStateFailed && !row.LeaseExpired:
				job, claimed = &existing, false
				return nil
			case existing.State != LoadJobStateFailed:
				// The owner stopped renewing. Fail it instead of replacing it:
				// its remote work may still run, so the model stays held for
				// the stop window.
				err := r.expireLoadJob(ctx, existing.Ref())
				if err != nil && !errors.Is(err, ErrStaleLoadJob) {
					return err
				}
				job, claimed = r.rereadOr(ctx, trackingKey, &existing), false
				return nil
			}
			// Failed, and the stop window is over. The delete names the
			// generation it observed, so a row another writer replaced
			// meanwhile is not removed.
			if err := r.ownedLoadJob(ctx, existing.Ref()).Delete(&ModelLoadJob{}).Error; err != nil {
				return fmt.Errorf("deleting released model load job: %w", err)
			}
		}

		now := r.now()
		fresh := &ModelLoadJob{
			TrackingKey:  trackingKey,
			Generation:   uuid.NewString(),
			State:        LoadJobStatePending,
			OwnerReplica: owner,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastProgress: now,
		}
		if err := r.db.WithContext(ctx).Create(fresh).Error; err != nil {
			return fmt.Errorf("creating model load job: %w", err)
		}
		if err := r.ownedLoadJob(ctx, fresh.Ref()).Update("lease_until", r.leaseExpr()).Error; err != nil {
			return fmt.Errorf("leasing model load job: %w", err)
		}
		job, claimed = fresh, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return job, claimed, nil
}

func (r *NodeRegistry) readClaimRow(ctx context.Context, trackingKey string) (claimRow, bool, error) {
	var row claimRow
	res := r.db.WithContext(ctx).Raw(`SELECT *,
		(lease_until IS NULL OR lease_until < ?) AS lease_expired,
		(stop_deadline IS NOT NULL AND stop_deadline < ?) AS stop_passed
		FROM model_load_jobs WHERE tracking_key = ?`, r.dbNow(), r.dbNow(), trackingKey).Scan(&row)
	if res.Error != nil {
		return row, false, fmt.Errorf("reading model load job: %w", res.Error)
	}
	return row, res.RowsAffected > 0, nil
}

// rereadOr returns the row as it is now, or fallback when it cannot be read.
func (r *NodeRegistry) rereadOr(ctx context.Context, trackingKey string, fallback *ModelLoadJob) *ModelLoadJob {
	if job, err := r.GetLoadJob(ctx, trackingKey); err == nil && job != nil {
		return job
	}
	return fallback
}

// expireLoadJob fails a running job whose lease has run out, as judged by the
// database clock at the moment of the write. A renewal that landed first makes
// it a no-op and returns ErrStaleLoadJob.
func (r *NodeRegistry) expireLoadJob(ctx context.Context, ref LoadJobRef) error {
	now := r.now()
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).
		Where("lease_until IS NULL OR lease_until < ?", r.dbNow()).
		Updates(map[string]any{
			"state":         LoadJobStateFailed,
			"last_error":    "the load owner stopped renewing its lease",
			"op_confirmed":  false,
			"stop_deadline": r.dbAfter(loadJobStopWindow),
			"last_progress": now,
			"updated_at":    now,
		}))
}

func (r *NodeRegistry) setStopWindow(ctx context.Context, ref LoadJobRef) error {
	return loadJobResult(r.ownedLoadJob(ctx, ref).
		Where("state = ? AND stop_deadline IS NULL", LoadJobStateFailed).
		Update("stop_deadline", r.dbAfter(loadJobStopWindow)))
}

// LoadJobSweep counts what one SweepLoadJobs pass did.
type LoadJobSweep struct {
	Expired  int
	Released []LoadJobRef
}

// SweepLoadJobs applies the lease rules to every job without waiting for a
// request: it fails jobs whose lease ran out and releases failed jobs whose
// stop window is over. Each write is fenced by the generation read, so a job
// replaced during the sweep is not touched.
func (r *NodeRegistry) SweepLoadJobs(ctx context.Context) (LoadJobSweep, error) {
	var out LoadJobSweep
	var running, failed []ModelLoadJob
	if err := r.db.WithContext(ctx).
		Where("state <> ? AND (lease_until IS NULL OR lease_until < ?)", LoadJobStateFailed, r.dbNow()).
		Find(&running).Error; err != nil {
		return out, fmt.Errorf("listing expired model load jobs: %w", err)
	}
	if err := r.db.WithContext(ctx).
		Where("state = ? AND (stop_deadline IS NULL OR stop_deadline < ?)", LoadJobStateFailed, r.dbNow()).
		Find(&failed).Error; err != nil {
		return out, fmt.Errorf("listing releasable model load jobs: %w", err)
	}
	for _, j := range running {
		switch err := r.expireLoadJob(ctx, j.Ref()); {
		case err == nil:
			out.Expired++
		case !errors.Is(err, ErrStaleLoadJob):
			return out, err
		}
	}
	for _, j := range failed {
		if j.StopDeadline == nil {
			if err := r.setStopWindow(ctx, j.Ref()); err != nil && !errors.Is(err, ErrStaleLoadJob) {
				return out, err
			}
			continue
		}
		switch err := r.DeleteFailedLoadJob(ctx, j.Ref()); {
		case err == nil:
			out.Released = append(out.Released, j.Ref())
		case !errors.Is(err, ErrStaleLoadJob):
			return out, err
		}
	}
	return out, nil
}

// GetLoadJob returns the active job for trackingKey, or (nil, nil) when none is
// active. Callers on a non-owning replica poll this; it is the authority for
// both readiness and failure.
func (r *NodeRegistry) GetLoadJob(ctx context.Context, trackingKey string) (*ModelLoadJob, error) {
	var job ModelLoadJob
	err := r.db.WithContext(ctx).First(&job, "tracking_key = ?", trackingKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading model load job: %w", err)
	}
	return &job, nil
}

// ListActiveLoadJobs returns every in-flight load in stable tracking-key order.
func (r *NodeRegistry) ListActiveLoadJobs(ctx context.Context) ([]ModelLoadJob, error) {
	jobs := []ModelLoadJob{}
	if err := r.db.WithContext(ctx).Order("tracking_key ASC").Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("listing model load jobs: %w", err)
	}
	return jobs, nil
}

// UpdateLoadJob applies a phase transition or heartbeat and renews the lease
// from the database clock. It must tick even during phases that move no bytes
// at all: a checkpoint load moves none for many minutes. It returns ErrStaleLoadJob when
// the attempt no longer owns the job, which is the owner's signal to stop.
func (r *NodeRegistry) UpdateLoadJob(ctx context.Context, ref LoadJobRef, u LoadJobUpdate) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	now := r.now()
	fields := map[string]any{
		"lease_until":   r.leaseExpr(),
		"last_progress": now,
		"updated_at":    now,
		"bytes_sent":    u.BytesSent,
		"total_bytes":   u.TotalBytes,
		"file_index":    u.FileIndex,
		"total_files":   u.TotalFiles,
	}
	if u.State != "" {
		fields["state"] = u.State
	}
	if u.NodeID != "" {
		fields["node_id"] = u.NodeID
		fields["replica_index"] = u.ReplicaIndex
	}
	if u.NodeName != "" {
		fields["node_name"] = u.NodeName
	}
	if !u.StartedAt.IsZero() {
		fields["started_at"] = u.StartedAt
	}
	if u.LegacyWorker {
		fields["legacy_worker"] = true
	}
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Updates(fields))
}

// FailLoadJob records the real failure on the job row so every waiter, local
// or on another replica, reports the same cause instead of an anonymous
// timeout. workMayRun says whether remote work may outlive the failure (a
// deadline, a cancel, a lost lease). If it may, the model stays held for the
// stop window. If the work is known to have ended, the row is kept only for the
// short report window. The next claim after that deadline replaces the row.
func (r *NodeRegistry) FailLoadJob(ctx context.Context, ref LoadJobRef, msg string, workMayRun bool) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	hold := loadJobFailureReport
	if workMayRun {
		hold = loadJobStopWindow
	}
	now := r.now()
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Updates(map[string]any{
		"state":         LoadJobStateFailed,
		"last_error":    msg,
		"op_confirmed":  !workMayRun,
		"stop_deadline": r.dbAfter(hold),
		"last_progress": now,
		"updated_at":    now,
	}))
}

// DeleteLoadJob removes the job of an attempt that succeeded. The NodeModel row
// is the record of a loaded model. A failed job is not removed here: it leaves
// through DeleteFailedLoadJob or the next claim, so a late success from a
// stale owner cannot erase the failure its waiters need to read.
func (r *NodeRegistry) DeleteLoadJob(ctx context.Context, ref LoadJobRef) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Delete(&ModelLoadJob{}))
}

// DeleteFailedLoadJob releases a failed job once its stop deadline has passed
// on the database clock. The next claim does the same, so this only keeps the
// table tidy when nobody retries.
func (r *NodeRegistry) DeleteFailedLoadJob(ctx context.Context, ref LoadJobRef) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	return loadJobResult(r.ownedLoadJob(ctx, ref).
		Where("state = ? AND stop_deadline < ?", LoadJobStateFailed, r.dbNow()).
		Delete(&ModelLoadJob{}))
}

// ConfirmLoadOp records that the remote work of a failed attempt ended: the
// worker acknowledged a stop, or it restarted. It shortens the stop window to
// the report window, so a model whose worker answers is free again in seconds
// and not after the full stop window. It never lengthens a window and never
// touches an attempt that has not failed. A replaced attempt returns
// ErrStaleLoadJob.
func (r *NodeRegistry) ConfirmLoadOp(ctx context.Context, ref LoadJobRef) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	if err := loadJobResult(r.ownedLoadJob(ctx, ref).
		Where("state = ?", LoadJobStateFailed).
		Update("op_confirmed", true)); err != nil {
		return err
	}
	// Shorten only: a window already shorter than the report window stays.
	if err := r.ownedLoadJob(ctx, ref).
		Where("state = ? AND stop_deadline > ?", LoadJobStateFailed, r.dbAfter(loadJobFailureReport)).
		Update("stop_deadline", r.dbAfter(loadJobFailureReport)).Error; err != nil {
		return err
	}
	return r.removeAttemptReplicas(ctx, ref)
}

// removeAttemptReplicas removes the replica rows a confirmed-dead attempt left
// before they reached serving. The owner's own cleanup cannot be relied on: it
// may be stuck in a call, or gone. The rows carry the attempt's generation, so
// no other attempt's row is touched.
func (r *NodeRegistry) removeAttemptReplicas(ctx context.Context, ref LoadJobRef) error {
	var rows []NodeModel
	if err := r.db.WithContext(ctx).
		Where("model_name = ? AND load_generation = ? AND state IN ?", ref.TrackingKey, ref.Generation, preServingStates).
		Find(&rows).Error; err != nil {
		return fmt.Errorf("listing the replicas of a dead load attempt: %w", err)
	}
	for _, row := range rows {
		if err := r.RemoveNodeModel(ctx, row.NodeID, row.ModelName, row.ReplicaIndex); err != nil {
			return err
		}
	}
	return nil
}

// ConfirmNodeLoadOps confirms every failed attempt that ran on nodeID. A new
// worker incarnation calls it: the backends of the previous process exited with
// their parent, so none of that node's operations still runs.
func (r *NodeRegistry) ConfirmNodeLoadOps(ctx context.Context, nodeID string) (int, error) {
	var jobs []ModelLoadJob
	if err := r.db.WithContext(ctx).
		Where("state = ? AND op_confirmed = ? AND node_id = ?", LoadJobStateFailed, false, nodeID).
		Find(&jobs).Error; err != nil {
		return 0, fmt.Errorf("listing unconfirmed load operations: %w", err)
	}
	confirmed := 0
	for _, j := range jobs {
		switch err := r.ConfirmLoadOp(ctx, j.Ref()); {
		case err == nil:
			confirmed++
		case !errors.Is(err, ErrStaleLoadJob):
			return confirmed, err
		}
	}
	return confirmed, nil
}

// ListLoadJobsAwaitingStop returns failed attempts on a known node whose remote
// work is not yet confirmed ended. The reconciler retries their stop.
func (r *NodeRegistry) ListLoadJobsAwaitingStop(ctx context.Context) ([]ModelLoadJob, error) {
	var jobs []ModelLoadJob
	if err := r.db.WithContext(ctx).
		Where("state = ? AND op_confirmed = ? AND node_id <> ?", LoadJobStateFailed, false, "").
		Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("listing load jobs awaiting stop: %w", err)
	}
	return jobs, nil
}

// SetLegacyStopWindow gives a failed attempt on a worker that cannot confirm a
// stop the longest window the controller knows: the load RPC deadline. Such a
// worker does not watch operations, so nothing bounds its work sooner.
func (r *NodeRegistry) SetLegacyStopWindow(ctx context.Context, ref LoadJobRef, window time.Duration) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	return loadJobResult(r.ownedLoadJob(ctx, ref).
		Where("state = ? AND op_confirmed = ?", LoadJobStateFailed, false).
		Update("stop_deadline", r.dbAfter(window)))
}

// CancelOutcome is what CancelLoadJob did.
type CancelOutcome int

const (
	// CancelRecorded: the job was running and is now failed and cancelled.
	CancelRecorded CancelOutcome = iota
	// CancelAlready: the job had already failed. The stop window is untouched.
	CancelAlready
	// CancelGone: no job exists for the model.
	CancelGone
	// CancelConflict: another generation holds the model. The returned job is it.
	CancelConflict
)

// CancelLoadJob records an administrator's cancel of one attempt. It fails the
// attempt with the stop window, so the model is held while the remote work is
// stopped, and it marks the cancel. Repeating it never extends the window.
// The returned job is the row as it is after the call (nil for CancelGone).
func (r *NodeRegistry) CancelLoadJob(ctx context.Context, ref LoadJobRef) (CancelOutcome, *ModelLoadJob, error) {
	return r.cancelLoadJob(ctx, ref, "cancelled by an administrator", true)
}

// cancelLoadJob is CancelLoadJob with the recorded cause. byAdmin says whether
// the cancel is an administrator's request. A node that is removed or drained
// cancels its loads with a neutral cause and does not claim an administrator
// asked.
func (r *NodeRegistry) cancelLoadJob(ctx context.Context, ref LoadJobRef, reason string, byAdmin bool) (CancelOutcome, *ModelLoadJob, error) {
	now := r.now()
	var outcome CancelOutcome
	err := advisorylock.WithLockCtx(ctx, r.db, advisorylock.KeyFromString(loadJobLockPrefix+ref.TrackingKey), func() error {
		job, err := r.GetLoadJob(ctx, ref.TrackingKey)
		if err != nil {
			return err
		}
		switch {
		case job == nil:
			outcome = CancelGone
			return nil
		case job.Generation != ref.Generation:
			outcome = CancelConflict
			return nil
		case job.State == LoadJobStateFailed:
			outcome = CancelAlready
			return nil
		}
		werr := loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Updates(map[string]any{
			"state":            LoadJobStateFailed,
			"cancel_requested": byAdmin,
			"last_error":       reason,
			"op_confirmed":     false,
			"stop_deadline":    r.dbAfter(loadJobStopWindow),
			"last_progress":    now,
			"updated_at":       now,
		}))
		if errors.Is(werr, ErrStaleLoadJob) {
			outcome = CancelConflict
			return nil
		}
		outcome = CancelRecorded
		return werr
	})
	if err != nil {
		return outcome, nil, err
	}
	if outcome == CancelGone {
		return outcome, nil, nil
	}
	job, err := r.GetLoadJob(ctx, ref.TrackingKey)
	return outcome, job, err
}

// ListLoadJobsOnNode returns the jobs placed on nodeID.
func (r *NodeRegistry) ListLoadJobsOnNode(ctx context.Context, nodeID string) ([]ModelLoadJob, error) {
	var jobs []ModelLoadJob
	if err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("listing load jobs on node: %w", err)
	}
	return jobs, nil
}

// ObserveWorkerIncarnation notes the incarnation a worker reported. When it
// differs from the one stored, the worker restarted: every operation of the
// previous process ended, so the failed attempts that ran there are confirmed.
// Most calls change nothing and cost no query, because the last value seen per
// node is cached.
func (r *NodeRegistry) ObserveWorkerIncarnation(ctx context.Context, nodeID, incarnation string) error {
	if incarnation == "" {
		return nil
	}
	if seen, ok := r.incarnations.Load(nodeID); ok && seen == incarnation {
		return nil
	}
	var node BackendNode
	if err := r.db.WithContext(ctx).Select("id", "worker_incarnation").First(&node, "id = ?", nodeID).Error; err != nil {
		return nil // an unknown node is the heartbeat's business
	}
	if node.WorkerIncarnation != incarnation {
		if err := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
			Update("worker_incarnation", incarnation).Error; err != nil {
			return fmt.Errorf("recording worker incarnation: %w", err)
		}
		if node.WorkerIncarnation != "" {
			if _, err := r.ConfirmNodeLoadOps(ctx, nodeID); err != nil {
				return err
			}
		}
	}
	r.incarnations.Store(nodeID, incarnation)
	return nil
}
