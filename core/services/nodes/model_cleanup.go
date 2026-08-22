package nodes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

const (
	modelCleanupInterval = time.Second
	// Exact stops are bounded to ten seconds. Claiming one row for two minutes
	// keeps ownership durable even under scheduler stalls and avoids a batch's
	// later rows losing their lease while earlier stops run.
	modelCleanupLease    = 2 * time.Minute
	modelCleanupBatch    = 1
	modelCleanupMaxDelay = 5 * time.Minute
)

type ModelCleanupService struct {
	registry ModelCleanupRegistry
	stopper  ExactModelStopper
	now      func() time.Time
}

func NewModelCleanupService(registry ModelCleanupRegistry, stopper ExactModelStopper) *ModelCleanupService {
	return &ModelCleanupService{registry: registry, stopper: stopper, now: time.Now}
}

func (s *ModelCleanupService) Cleanup(ctx context.Context, replicas []NodeModel, force bool) int {
	pending := 0
	for _, replica := range replicas {
		reply, err := s.stopper.StopModelReplica(ctx, replica.NodeID, replica, force)
		if err == nil && reply.Terminated {
			removed, removeErr := s.registry.RemoveClaimedModelCleanup(ctx, replica)
			if removeErr != nil {
				xlog.Warn("Removing terminated model replica failed", "nodeID", replica.NodeID, "model", replica.ModelName, "replica", replica.ReplicaIndex, "error", removeErr)
				pending++
			} else if !removed {
				pending++
			}
			continue
		}
		pending++

		cleanupErr := conciseCleanupError(err, reply.Error)
		nextRetry := s.now().Add(modelCleanupBackoff(replica.CleanupAttempts))
		if recordErr := s.registry.RecordModelCleanupFailure(ctx, replica.NodeID, replica.ModelName, replica.ReplicaIndex, cleanupErr, nextRetry); recordErr != nil {
			xlog.Warn("Recording model cleanup retry failed", "nodeID", replica.NodeID, "model", replica.ModelName, "replica", replica.ReplicaIndex, "error", recordErr)
		}
	}
	return pending
}

func (s *ModelCleanupService) Run(ctx context.Context) {
	ticker := time.NewTicker(modelCleanupInterval)
	defer ticker.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *ModelCleanupService) runOnce(ctx context.Context) {
	now := s.now()
	replicas, err := s.registry.ClaimModelCleanupRetries(ctx, now, now.Add(modelCleanupLease), modelCleanupBatch)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			xlog.Warn("Claiming model cleanup retries failed", "error", err)
		}
		return
	}
	s.Cleanup(ctx, replicas, false)
}

func modelCleanupBackoff(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 8 {
		attempts = 8
	}
	delay := time.Second * time.Duration(1<<attempts)
	if delay > modelCleanupMaxDelay {
		return modelCleanupMaxDelay
	}
	return delay
}

func conciseCleanupError(err error, replyError string) string {
	message := strings.TrimSpace(replyError)
	if err != nil {
		message = err.Error()
	}
	if i := strings.LastIndex(message, ": "); i >= 0 {
		message = message[i+2:]
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "termination not confirmed"
	}
	const max = 240
	if len(message) > max {
		return message[:max]
	}
	return message
}
