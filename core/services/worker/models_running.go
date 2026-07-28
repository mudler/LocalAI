package worker

import (
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/messaging"
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
	if hash < 0 {
		return "", 0, false
	}
	replica, err := strconv.Atoi(key[hash+1:])
	if err != nil {
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
func (s *backendSupervisor) runningModels() []messaging.RunningModelInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	running := make([]messaging.RunningModelInfo, 0, len(s.processes))
	for key, bp := range s.processes {
		if bp == nil || bp.stopping || bp.proc == nil || !bp.proc.IsAlive() {
			continue
		}
		modelID, replicaIndex, ok := parseProcessKey(key)
		if !ok {
			xlog.Warn("Skipping unparseable process key when reporting running models", "key", key)
			continue
		}
		running = append(running, messaging.RunningModelInfo{
			ModelID:      modelID,
			ReplicaIndex: replicaIndex,
			Address:      bp.addr,
		})
	}
	return running
}

// handleModelsRunning answers a models.running request with this worker's live
// process set.
func (s *backendSupervisor) handleModelsRunning(_ []byte, reply func([]byte)) {
	running := s.runningModels()
	xlog.Debug("Answering models.running", "nodeID", s.nodeID, "count", len(running))
	replyJSON(reply, messaging.ModelsRunningReply{Models: running})
}
