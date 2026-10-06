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

// Retain the reservation after install: staging and direct backend LoadModel
// run outside this supervisor. Neither an idle process nor an RPC timeout
// proves that those calls ended. Only exact termination releases ownership.
func (s *backendSupervisor) beginLoadOperation(req workerctl.BackendInstallRequest) error {
	key := buildProcessKey(req.ModelID, req.Backend, int(req.ReplicaIndex))
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOperationLocked(req.Operation, key); err != nil {
		return err
	}
	if req.Operation == nil {
		return nil
	}
	if req.Force || req.ModelID != req.Operation.TrackingKey || req.ReplicaIndex < 0 {
		return fmt.Errorf("invalid fenced install target")
	}
	if s.operations[key] != nil || s.processes[key] != nil {
		return fmt.Errorf("process slot is already occupied")
	}
	if s.operations == nil {
		s.operations = make(map[string]*workerctl.LoadOperation)
	}
	s.operations[key] = &workerctl.LoadOperation{Identity: *req.Operation, ProcessKey: key, Phase: "install", Active: true}
	return nil
}

func (s *backendSupervisor) finishLoadInstall(req workerctl.BackendInstallRequest) string {
	key := buildProcessKey(req.ModelID, req.Backend, int(req.ReplicaIndex))
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.operations[key]; op != nil {
		op.Active = false
		op.Phase = "stage-or-load"
	}
	if bp := s.processes[key]; bp != nil {
		bp.revision = req.ConfigRevision
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

func (s *backendSupervisor) beginStaging(id *workerctl.OperationIdentity, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkOperationLocked(id, key); err != nil {
		return err
	}
	op := s.operations[key]
	if op == nil || op.Active || op.Phase != "stage-or-load" {
		return fmt.Errorf("operation is not available for staging")
	}
	op.Active = true
	op.Phase = "stage"
	return nil
}
func (s *backendSupervisor) endStaging(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op := s.operations[key]; op != nil {
		op.Active = false
		op.Phase = "stage-or-load"
	}
}
