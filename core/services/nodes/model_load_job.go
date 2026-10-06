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

	// loadJobOrphanWindow is how long a job may go without a heartbeat before
	// another replica may reclaim it. Generous relative to the 1s heartbeat: a
	// frontend under GC pressure or a stalled DB write must not have its
	// perfectly healthy multi-GB transfer stolen and restarted from zero.
	loadJobOrphanWindow = 60 * time.Second

	// loadJobFailureGrace is how long a failed job row is kept before deletion.
	// Without it a waiter polling just after the failure finds no row, concludes
	// "not loading", and starts a duplicate load of a model that just failed —
	// a retry storm dressed as recovery.
	loadJobFailureGrace = 15 * time.Second

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

// IsOrphaned reports whether the job's owner has stopped heartbeating and the
// job may be reclaimed by another replica.
func (j *ModelLoadJob) IsOrphaned(now time.Time) bool {
	return now.Sub(j.LastProgress) > loadJobOrphanWindow
}

// reclaimable reports whether a new owner may replace this row. A running job
// is replaceable once its owner stops heartbeating. A failed job is replaceable
// once its grace window ends: the window keeps waiters that arrive right after
// the failure from starting a duplicate load, and it is read from the row, so a
// restarted frontend releases the model the same way as the one that failed it.
func (j *ModelLoadJob) reclaimable(now time.Time) bool {
	if j.State == LoadJobStateFailed {
		return now.Sub(j.LastProgress) >= loadJobFailureGrace
	}
	return j.IsOrphaned(now)
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

// activeLoadJob limits a write to an attempt that has not failed. A failed row
// only changes through its own grace-gated release.
func activeLoadJob(q *gorm.DB) *gorm.DB {
	return q.Where("state <> ?", LoadJobStateFailed)
}

// A ref with no generation names a legacy row. Nothing that runs today can own
// one, so every owner write rejects it before it reaches the database.
func (ref LoadJobRef) owned() bool { return ref.Generation != "" }

// backfillLoadJobGenerations gives every row written before the generation
// column existed a generation of its own. A legacy row then follows the same
// rules as any other: it is a waiter's target while its owner heartbeats, and
// it is reclaimed once the heartbeat stops. Rows an old binary writes after
// this runs keep the empty default and follow the same path in ClaimLoadJob.
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
	return loadJobResult(activeLoadJob(ownedLoadJobOn(tx, ref)).UpdateColumn("generation", ref.Generation))
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
}

// ClaimLoadJob decides, under the per-model advisory lock, whether this replica
// owns the cold load of trackingKey. It returns the live job and claimed=false
// when another replica is already loading it (or it just failed and is inside
// its grace window), or a fresh `pending` job with claimed=true when this
// replica took the work.
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
		var existing ModelLoadJob
		err := r.db.WithContext(ctx).First(&existing, "tracking_key = ?", trackingKey).Error
		switch {
		case err == nil:
			if !existing.reclaimable(time.Now()) {
				job, claimed = &existing, false
				return nil
			}
			// The owner is gone (a crashed frontend, or a failed load whose
			// grace window is over). Without this the model stays wedged:
			// every later request would wait for a load nobody runs. The
			// delete names the generation it observed, so a row another
			// writer replaced meanwhile is not removed.
			if err := r.ownedLoadJob(ctx, existing.Ref()).Delete(&ModelLoadJob{}).Error; err != nil {
				return fmt.Errorf("deleting reclaimable model load job: %w", err)
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return fmt.Errorf("reading model load job: %w", err)
		}

		now := time.Now()
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
		job, claimed = fresh, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return job, claimed, nil
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

// UpdateLoadJob applies a phase transition or heartbeat. LastProgress is always
// touched: it is the liveness signal the orphan check reads, and it must tick
// even during phases that move no bytes at all. It returns ErrStaleLoadJob when
// the attempt no longer owns the job, which is the owner's signal to stop.
func (r *NodeRegistry) UpdateLoadJob(ctx context.Context, ref LoadJobRef, u LoadJobUpdate) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	now := time.Now()
	fields := map[string]any{
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
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Updates(fields))
}

// FailLoadJob records the real failure on the job row so every waiter, local
// or on another replica, reports the same cause instead of an anonymous
// timeout. The row stays for loadJobFailureGrace, then the next claim replaces
// it or DeleteFailedLoadJob removes it.
func (r *NodeRegistry) FailLoadJob(ctx context.Context, ref LoadJobRef, msg string) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	now := time.Now()
	return loadJobResult(activeLoadJob(r.ownedLoadJob(ctx, ref)).Updates(map[string]any{
		"state":         LoadJobStateFailed,
		"last_error":    msg,
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

// DeleteFailedLoadJob removes a failed job once its grace window is over. It
// keeps the table tidy when nobody retries; the next claim does not need it.
func (r *NodeRegistry) DeleteFailedLoadJob(ctx context.Context, ref LoadJobRef) error {
	if !ref.owned() {
		return ErrStaleLoadJob
	}
	cutoff := time.Now().Add(-loadJobFailureGrace)
	return loadJobResult(r.ownedLoadJob(ctx, ref).
		Where("state = ? AND last_progress <= ?", LoadJobStateFailed, cutoff).
		Delete(&ModelLoadJob{}))
}
