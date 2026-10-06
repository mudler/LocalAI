package nodes

import (
	"context"
	"gorm.io/gorm"
	"time"
)

type LoadRecoveryOutcome string

const (
	LoadTerminalConfirmed LoadRecoveryOutcome = "terminal-confirmed"
	LoadStillActive       LoadRecoveryOutcome = "still-active"
	LoadUncertain         LoadRecoveryOutcome = "uncertain"
)

type LoadRecoveryResult struct {
	Outcome LoadRecoveryOutcome
	Reason  string
}

// LoadRecoveryService never treats a lease or worker boot as proof that direct
// backend calls stopped. Failed attempts remain quarantined until verified cleanup.
type LoadRecoveryService struct{ Registry *NodeRegistry }

func (s *LoadRecoveryService) Reconcile(ctx context.Context, ref LoadJobRef) (LoadRecoveryResult, error) {
	result := LoadRecoveryResult{Outcome: LoadUncertain, Reason: "remote operation termination is not proven"}
	err := s.Registry.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The conditional UPDATE acquires the same row lock as heartbeat updates.
		// Recheck expiry in SQL: a heartbeat after a sweep read must win safely.
		now := time.Now()
		res := tx.Model(&ModelLoadJob{}).Where("tracking_key = ? AND generation = ? AND terminal_until IS NULL AND last_progress < ?", ref.TrackingKey, ref.Generation, now.Add(-loadJobOrphanWindow)).Updates(map[string]any{"state": LoadJobStateFailed, "work_uncertain": true, "terminal_until": now.Add(loadJobFailureGrace), "last_error": "owner lease expired; remote work uncertain; verified operator cleanup required", "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		var job ModelLoadJob
		if err := tx.First(&job, "tracking_key = ? AND generation = ?", ref.TrackingKey, ref.Generation).Error; err != nil {
			return err
		}
		if job.TerminalUntil == nil {
			result = LoadRecoveryResult{Outcome: LoadStillActive, Reason: "owner lease is live"}
		} else if !job.WorkUncertain && job.Generation != "" {
			result = LoadRecoveryResult{Outcome: LoadTerminalConfirmed, Reason: "durable terminal confirmation"}
		}
		return nil
	})
	return result, err
}

// Fail records a bounded owner exit without claiming remote termination.
func (s *LoadRecoveryService) Fail(ctx context.Context, ref LoadJobRef, reason string) (LoadRecoveryResult, error) {
	return LoadRecoveryResult{Outcome: LoadUncertain, Reason: reason}, s.Registry.FailLoadJob(ctx, ref, reason)
}

// loadOwnershipKey carries generation authority, never inferred from model name.
type loadOwnershipKey struct{}

func requireLoadOwnership(ctx context.Context, tx *gorm.DB) error {
	ref, ok := ctx.Value(loadOwnershipKey{}).(LoadJobRef)
	if !ok {
		return nil
	}
	// A no-op conditional UPDATE holds the job row lock until publication commits.
	return loadJobResult(tx.Model(&ModelLoadJob{}).Where("tracking_key = ? AND generation = ? AND generation <> '' AND terminal_until IS NULL", ref.TrackingKey, ref.Generation).UpdateColumn("generation", ref.Generation))
}
