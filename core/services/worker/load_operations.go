package worker

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// One random identity per worker OS process, shared by registration and control.
var workerIncarnation = uuid.NewString()

func (s *backendSupervisor) checkOperationLocked(id *workerctl.OperationIdentity, key string) error {
	if id != nil && (id.Incarnation != workerIncarnation || id.Generation == "" || id.TrackingKey == "") {
		return fmt.Errorf("invalid operation identity or worker incarnation")
	}
	if op := s.operations[key]; op != nil && (id == nil || op.Identity != *id) {
		return fmt.Errorf("process slot belongs to another operation")
	}
	return nil
}

// installReservation is local to one invocation, never a caller-supplied ID.
// Its process pointer is bound under mu at spawn/reuse, not by address later.
type installReservation struct {
	key       string
	operation *workerctl.LoadOperation
	process   *backendProcess
}

func (s *backendSupervisor) beginLoadOperation(req workerctl.BackendInstallRequest) (*installReservation, error) {
	key := buildProcessKey(req.ModelID, req.Backend, int(req.ReplicaIndex))
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOperationLocked(req.Operation, key); err != nil {
		return nil, err
	}
	if s.installs[key] != nil {
		return nil, fmt.Errorf("install already active")
	}
	if req.Operation != nil {
		if req.Force || req.ModelID != req.Operation.TrackingKey || req.ReplicaIndex < 0 {
			return nil, fmt.Errorf("invalid fenced install target")
		}
		if s.operations[key] != nil || s.processes[key] != nil {
			return nil, fmt.Errorf("process slot is already occupied")
		}
	}
	if bp := s.processes[key]; bp != nil && req.ConfigRevision != "" && bp.revision != "" && bp.revision != req.ConfigRevision {
		return nil, fmt.Errorf("process configuration revision mismatch")
	}
	if s.operations == nil {
		s.operations = make(map[string]*workerctl.LoadOperation)
	}
	if s.installs == nil {
		s.installs = make(map[string]*installReservation)
	}
	op := &workerctl.LoadOperation{ProcessKey: key, Phase: "install", Active: true}
	if req.Operation != nil {
		op.Identity = *req.Operation
	}
	token := &installReservation{key: key, operation: op, process: s.processes[key]}
	s.operations[key] = op
	s.installs[key] = token
	return token, nil
}

func (s *backendSupervisor) finishLoadInstall(req workerctl.BackendInstallRequest, token *installReservation) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == nil || s.installs[token.key] != token || s.operations[token.key] != token.operation {
		return ""
	}
	delete(s.installs, token.key)
	if req.Operation == nil {
		delete(s.operations, token.key)
	} else {
		token.operation.Active = false
		token.operation.Phase = "stage-or-load"
	}
	if bp := s.processes[token.key]; bp != nil && bp == token.process {
		if req.ConfigRevision != "" {
			bp.revision = req.ConfigRevision
		}
		if req.Operation != nil {
			id := *req.Operation
			bp.operation = &id
		}
		return bp.instance
	}
	return ""
}

// Keep a tombstone: delayed installs from a cancelled frontend must not restart
// the same generation. Stage/load work remains uncertain until worker reboot.
func (s *backendSupervisor) retireOperationLocked(key string) {
	if op := s.operations[key]; op != nil {
		op.Phase = "terminated-process-work-uncertain"
	}
}

// Anonymous file requests have no trustworthy process association. Report each
// invocation separately; never attribute them to an owned generation.
func (s *backendSupervisor) beginStaging(id *workerctl.OperationIdentity, key string) (*workerctl.LoadOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == nil {
		if s.anonymousStages == nil {
			s.anonymousStages = make(map[*workerctl.LoadOperation]struct{})
		}
		op := &workerctl.LoadOperation{Phase: "legacy-stage", Active: true}
		s.anonymousStages[op] = struct{}{}
		return op, nil
	}
	if err := s.checkOperationLocked(id, key); err != nil {
		return nil, err
	}
	op := s.operations[key]
	if op == nil || op.Active || op.Phase != "stage-or-load" {
		return nil, fmt.Errorf("operation is not available for staging")
	}
	// Replace the record so a duplicate completion from an earlier stage cannot
	// clear a later invocation with the same public operation identity.
	next := *op
	next.Active = true
	next.Phase = "stage"
	s.operations[key] = &next
	return &next, nil
}
func (s *backendSupervisor) endStaging(token *workerctl.LoadOperation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == nil {
		return
	}
	if _, ok := s.anonymousStages[token]; ok {
		delete(s.anonymousStages, token)
		return
	}
	if s.operations[token.ProcessKey] == token {
		token.Active = false
		token.Phase = "stage-or-load"
	}
}
