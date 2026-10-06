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
	// it is considered orphaned, not safe to reclaim. Relative to the 1s heartbeat, a
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

// IsOrphaned reports lease expiry only; it does not establish remote work safety.
func (j *ModelLoadJob) IsOrphaned(now time.Time) bool {
	return now.Sub(j.LastProgress) > loadJobOrphanWindow
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

// LoadJobRef identifies one immutable attempt, not whichever job currently owns a model.
type LoadJobRef struct{ TrackingKey, Generation string }

func (j *ModelLoadJob) Ref() LoadJobRef { return LoadJobRef{j.TrackingKey, j.Generation} }

// ErrStaleLoadJob means the attempt is absent, terminal, legacy, or replaced.
var ErrStaleLoadJob = errors.New("stale model load job ownership")

func loadJobResult(res *gorm.DB) error {
	if res.Error != nil {
		return fmt.Errorf("writing model load job: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return ErrStaleLoadJob
	}
	return nil
}

func (r *NodeRegistry) ownedLoadJob(ctx context.Context, ref LoadJobRef) *gorm.DB {
	return r.db.WithContext(ctx).Model(&ModelLoadJob{}).
		Where("tracking_key = ? AND generation = ? AND generation <> ''", ref.TrackingKey, ref.Generation)
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
			// Lease expiry is not evidence that remote work stopped. Legacy rows
			// remain quarantined: inventing a generation cannot fence their writer.
			if existing.Generation == "" || existing.WorkUncertain || existing.TerminalUntil == nil || time.Now().Before(*existing.TerminalUntil) {
				job, claimed = &existing, false
				return nil
			}
			if err := r.DeleteLoadJob(ctx, existing.Ref()); err != nil {
				return err
			}

		case errors.Is(err, gorm.ErrRecordNotFound):
		default:
			return fmt.Errorf("reading model load job: %w", err)
		}

		now := time.Now()
		fresh := &ModelLoadJob{
			TrackingKey:   trackingKey,
			Generation:    uuid.NewString(),
			WorkUncertain: true,
			State:         LoadJobStatePending,
			OwnerReplica:  owner,
			CreatedAt:     now,
			UpdatedAt:     now,
			LastProgress:  now,
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
// even during phases that move no bytes at all.
func (r *NodeRegistry) UpdateLoadJob(ctx context.Context, ref LoadJobRef, u LoadJobUpdate) error {
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
	res := r.ownedLoadJob(ctx, ref).Where("terminal_until IS NULL").Updates(fields)
	return loadJobResult(res)
}

// FailLoadJob records the real failure on the job row so every waiter — local
// or on another replica — reports the same cause instead of an anonymous
// timeout. Unknown remote work remains protected beyond the durable grace.
func (r *NodeRegistry) FailLoadJob(ctx context.Context, ref LoadJobRef, msg string) error {
	now := time.Now()
	res := r.ownedLoadJob(ctx, ref).Where("terminal_until IS NULL").
		Updates(map[string]any{
			"state":          LoadJobStateFailed,
			"terminal_until": now.Add(loadJobFailureGrace),
			"work_uncertain": true,
			"last_error":     msg,
			"last_progress":  now,
			"updated_at":     now,
		})
	return loadJobResult(res)
}

// DeleteLoadJob removes a terminal job row. Success deletes immediately (the
// NodeModel row is the record of a loaded model); failures delete after their
// grace window and confirmation that remote work stopped.
func (r *NodeRegistry) DeleteLoadJob(ctx context.Context, ref LoadJobRef) error {
	// Active deletion is an owner-confirmed success. Failed work can only be
	// removed after durable grace, and only if no remote placement is unresolved.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("tracking_key = ? AND generation = ? AND generation <> ''", ref.TrackingKey, ref.Generation).
			Where("terminal_until IS NULL OR (terminal_until <= ? AND work_uncertain = ?)", time.Now(), false).Delete(&ModelLoadJob{})
		if err := loadJobResult(res); err != nil {
			return err
		}
		if err := tx.Where("expires_at < ?", time.Now()).Delete(&LoadJobTombstone{}).Error; err != nil {
			return err
		}
		// Bound retained history even under sustained successful load churn.
		if err := tx.Where("generation IN (?)", tx.Model(&LoadJobTombstone{}).Select("generation").Order("expires_at DESC").Offset(4095)).Delete(&LoadJobTombstone{}).Error; err != nil {
			return err
		}
		return tx.Create(&LoadJobTombstone{Generation: ref.Generation, TrackingKey: ref.TrackingKey, ExpiresAt: time.Now().Add(15 * time.Minute)}).Error
	})
}

// Tombstones are durable, time-limited evidence of owner-confirmed completion.
type LoadJobTombstone struct {
	Generation  string    `gorm:"primaryKey;size:36"`
	TrackingKey string    `gorm:"index;size:255"`
	ExpiresAt   time.Time `gorm:"index"`
}
