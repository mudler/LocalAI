package worker

import (
	"context"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// parseProcessKey is the inverse of buildProcessKey: it splits a
// `modelID#replicaIndex` process key back into its parts.
//
// The split is on the LAST '#' because model ids are user-supplied and may
// themselves contain one; only the trailing "#N" is the supervisor's suffix.
// Returns ok=false for anything that does not carry a numeric replica suffix,
// so a malformed key is skipped rather than reported under a wrong identity.
func parseProcessKey(key string) (modelID string, replicaIndex int, ok bool) {
	hash := strings.LastIndex(key, "#")
	if hash <= 0 {
		return "", 0, false
	}
	replica, err := strconv.Atoi(key[hash+1:])
	if err != nil || replica < 0 {
		return "", 0, false
	}
	return key[:hash], replica, true
}

// runningModels returns the model backend processes this worker currently has
// alive, in the (modelID, replicaIndex, address) shape the controller's
// registry rows are keyed by.
//
// Processes being stopped are excluded: they are alive but on their way out,
// and reporting them would resurrect a replica the controller just released.
func (s *backendSupervisor) runningModels() []workerctl.RunningModelInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	running := make([]workerctl.RunningModelInfo, 0, len(s.processes))
	for key, bp := range s.processes {
		if bp == nil || bp.stopping || bp.proc == nil || !bp.proc.IsAlive() {
			continue
		}
		modelID, replicaIndex, ok := parseProcessKey(key)
		if !ok {
			xlog.Warn("Skipping unparseable process key when reporting running models", "key", key)
			continue
		}
		running = append(running, workerctl.RunningModelInfo{
			ProcessInstance: bp.instance,
			ConfigRevision:  bp.revision,
			Operation:       bp.operation,
			ModelID:         modelID,
			ReplicaIndex:    replicaIndex,
			Address:         bp.addr,
		})
	}
	return running
}

// modelsRunning answers a models.running request with this worker's live
// process set.
func (s *backendSupervisor) modelsRunning(_ context.Context, _ workerctl.ModelsRunningRequest) workerctl.ModelsRunningReply {
	running := s.runningModels()
	xlog.Debug("Answering models.running", "nodeID", s.nodeID, "count", len(running))
	s.mu.Lock()
	defer s.mu.Unlock()
	operations := make([]workerctl.LoadOperation, 0, len(s.operations))
	for _, op := range s.operations {
		operations = append(operations, *op)
	}
	return workerctl.ModelsRunningReply{Models: running, Incarnation: workerIncarnation, ReportsOperations: true, Operations: operations}
}
