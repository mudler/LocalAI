package nodes

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/model"
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
type LoadRecoveryService struct {
	Registry *NodeRegistry
	Stopper  LoadOperationStopper
}

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

var ErrLoadJobConflict = errors.New("different load generation")
var ErrLoadJobUnknown = errors.New("unknown load generation")

// Cancel records durable intent before any remote action. Direct backend load
// admission is not fenced, so even an exact process stop cannot release quarantine.
func (s *LoadRecoveryService) Cancel(ctx context.Context, ref LoadJobRef, stopper LoadOperationStopper) (LoadRecoveryResult, error) {
	if stopper == nil {
		stopper = s.Stopper
	}
	result := LoadRecoveryResult{Outcome: LoadUncertain, Reason: "cancellation requested; remote work uncertain"}
	err := s.Registry.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tomb LoadJobTombstone
		tombErr := tx.First(&tomb, "tracking_key = ? AND generation = ? AND expires_at > ?", ref.TrackingKey, ref.Generation, time.Now()).Error
		if tombErr == nil {
			result = LoadRecoveryResult{Outcome: LoadTerminalConfirmed, Reason: "owner-confirmed completion"}
			return nil
		}
		if !errors.Is(tombErr, gorm.ErrRecordNotFound) {
			return tombErr
		}

		now := time.Now()
		res := tx.Model(&ModelLoadJob{}).Where("tracking_key = ? AND generation = ? AND generation <> '' AND (terminal_until IS NULL OR work_uncertain = ?)", ref.TrackingKey, ref.Generation, true).Updates(map[string]any{"cancel_requested": true, "state": LoadJobStateFailed, "work_uncertain": true, "terminal_until": now.Add(loadJobFailureGrace), "last_error": result.Reason, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			return nil
		}
		var job ModelLoadJob
		err := tx.First(&job, "tracking_key = ?", ref.TrackingKey).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLoadJobUnknown
		}
		if err != nil {
			return err
		}
		if job.Ref() == ref && job.TerminalUntil != nil && !job.WorkUncertain {
			result = LoadRecoveryResult{Outcome: LoadTerminalConfirmed, Reason: "durable terminal confirmation"}
			return nil
		}
		return ErrLoadJobConflict
	})
	if err == nil && result.Outcome == LoadUncertain && stopper != nil {
		job, readErr := s.Registry.GetLoadJob(ctx, ref.TrackingKey)
		if readErr != nil {
			return result, readErr
		}
		if job != nil && job.Ref() == ref && job.NodeID != "" {
			if lister, ok := stopper.(interface {
				ListRunningModels(string) (*workerctl.ModelsRunningReply, error)
			}); ok {
				inventory, listErr := lister.ListRunningModels(job.NodeID)
				if listErr != nil {
					result.Reason += ": worker inventory unavailable"
					return result, nil
				}
				if inventory != nil {
					for _, process := range inventory.Models {
						op := process.Operation
						if op == nil || op.TrackingKey != ref.TrackingKey || op.Generation != ref.Generation || op.Incarnation == "" || op.Incarnation != inventory.Incarnation || process.ProcessInstance == "" || process.Address == "" {
							continue
						}
						_, stopErr := stopper.StopLoadOperation(ctx, job.NodeID, ref, op.Incarnation, model.BackendProcessKey(process.ModelID, process.ReplicaIndex), process.Address, process.ProcessInstance, process.ConfigRevision)
						if stopErr != nil {
							result.Reason += ": exact stop not confirmed"
							return result, nil
						}
					}
				}
			}
		}
	}

	return result, err
}
