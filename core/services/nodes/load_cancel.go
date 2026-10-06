package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"
)

// LoadCancelState is what a cancel achieved.
type LoadCancelState string

const (
	// LoadCancelStopped: the worker confirmed the load's work ended.
	LoadCancelStopped LoadCancelState = "stopped"
	// LoadCancelStopping: the cancel is recorded and the stop is pending. The
	// model is released at the stop deadline regardless.
	LoadCancelStopping LoadCancelState = "stopping"
	// LoadCancelGone: no load exists for the model any more.
	LoadCancelGone LoadCancelState = "gone"
)

// ErrLoadCancelConflict means another generation holds the model.
// CurrentJobID on the result names it.
var ErrLoadCancelConflict = errors.New("a different load generation is current")

// LoadCancelResult is the outcome of one cancel.
type LoadCancelResult struct {
	State LoadCancelState
	// RetryAfter is how long until the model is released if the stop never
	// confirms. Zero when the state is stopped or gone.
	RetryAfter time.Duration
	// CurrentJobID is the generation that holds the model, set with
	// ErrLoadCancelConflict.
	CurrentJobID string
}

// LoadCancelService cancels distributed loads. It is the one path that cancel,
// unload and node deregistration share: it records the cancel on the job, then
// stops the remote work through the single stop call.
type LoadCancelService struct {
	Registry *NodeRegistry
	// Stopper stops one load operation. It may be nil: the cancel is then
	// recorded and the worker's own watchdog and the stop window bound the work.
	Stopper LoadOperationStopper
}

// Cancel cancels the load attempt ref names. It is idempotent: repeating it on
// a failed attempt retries the stop and never extends the stop window.
func (s *LoadCancelService) Cancel(ctx context.Context, ref LoadJobRef) (LoadCancelResult, error) {
	outcome, job, err := s.Registry.CancelLoadJob(ctx, ref)
	if err != nil {
		return LoadCancelResult{}, err
	}
	switch outcome {
	case CancelGone:
		return LoadCancelResult{State: LoadCancelGone}, nil
	case CancelConflict:
		current := ""
		if job != nil {
			current = job.Generation
		}
		return LoadCancelResult{CurrentJobID: current}, fmt.Errorf("%w: %s", ErrLoadCancelConflict, current)
	}

	// The attempt is failed and cancelled. Stop its remote work.
	if !job.OpConfirmed && job.NodeID != "" && s.Stopper != nil {
		if StopOperationAcked(ctx, s.Stopper, job.NodeID, ref, job.ReplicaIndex) {
			if cerr := s.Registry.ConfirmLoadOp(ctx, ref); cerr != nil && !errors.Is(cerr, ErrStaleLoadJob) {
				xlog.Warn("Failed to record the stop confirmation", "model", ref.TrackingKey, "error", cerr)
			}
			return LoadCancelResult{State: LoadCancelStopped}, nil
		}
	}
	if job.OpConfirmed {
		return LoadCancelResult{State: LoadCancelStopped}, nil
	}
	result := LoadCancelResult{State: LoadCancelStopping}
	if fresh, gerr := s.Registry.GetLoadJob(ctx, ref.TrackingKey); gerr == nil && fresh != nil && fresh.StopDeadline != nil {
		result.RetryAfter = max(time.Until(*fresh.StopDeadline), time.Second)
	}
	return result, nil
}

// CancelModelOnNode cancels the load of modelName that runs on nodeID, or has
// no node yet. A load placed on another node is left alone: unloading a replica
// here must not cancel a load there. It reports the attempts it cancelled.
//
// An unload of a loaded replica calls this first and then carries on with the
// normal unload: stopping the load's operation never stops a model that
// finished loading, because the worker refuses a stop whose operation ended.
func (s *LoadCancelService) CancelModelOnNode(ctx context.Context, nodeID, modelName string) ([]LoadJobRef, error) {
	job, err := s.Registry.GetLoadJob(ctx, modelName)
	if err != nil || job == nil {
		return nil, err
	}
	if job.NodeID != "" && job.NodeID != nodeID {
		return nil, nil
	}
	if _, err := s.Cancel(ctx, job.Ref()); err != nil && !errors.Is(err, ErrLoadCancelConflict) {
		return nil, err
	}
	return []LoadJobRef{job.Ref()}, nil
}

// CancelNodeLoads cancels every load placed on nodeID. Deregistering or
// draining a node calls it before the node's rows are removed.
func (s *LoadCancelService) CancelNodeLoads(ctx context.Context, nodeID string) error {
	jobs, err := s.Registry.ListLoadJobsOnNode(ctx, nodeID)
	if err != nil {
		return err
	}
	var errs []error
	for _, job := range jobs {
		if _, err := s.Cancel(ctx, job.Ref()); err != nil && !errors.Is(err, ErrLoadCancelConflict) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// retryLoadStops retries the stop of failed attempts whose remote work is not
// confirmed ended, once per reconciler pass, until the worker acknowledges or
// the stop deadline releases the job.
func (rc *ReplicaReconciler) retryLoadStops(ctx context.Context) {
	stopper, ok := rc.unloader.(LoadOperationStopper)
	if !ok && rc.adapter != nil {
		stopper, ok = rc.adapter, true
	}
	if !ok {
		return
	}
	jobs, err := rc.registry.ListLoadJobsAwaitingStop(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to list load jobs awaiting stop", "error", err)
		return
	}
	for _, job := range jobs {
		if !StopOperationAcked(ctx, stopper, job.NodeID, job.Ref(), job.ReplicaIndex) {
			continue
		}
		if err := rc.registry.ConfirmLoadOp(ctx, job.Ref()); err != nil && !errors.Is(err, ErrStaleLoadJob) {
			xlog.Warn("Reconciler: failed to record a stop confirmation", "model", job.TrackingKey, "error", err)
		}
	}
}
