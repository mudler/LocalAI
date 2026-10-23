package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

const (
	// defaultOperationKillTTL is how long a load operation may go without a
	// renewal before the worker kills it. It is longer than the controller's
	// 30 second lease, so the controller always expires its own row first.
	defaultOperationKillTTL = 90 * time.Second

	// defaultOperationDeadline bounds an operation whose request carried no
	// deadline: a controller older than the field. It matches the controller's
	// own cap on one cold load.
	defaultOperationDeadline = 45 * time.Minute

	// operationTickInterval is how often the watchdog looks for expired
	// operations.
	operationTickInterval = time.Second
)

// workerIncarnation identifies this worker process. A new value after a restart
// is proof that every operation of the previous process ended, because the
// backends exit with their parent.
var workerIncarnation = uuid.NewString()

// loadOperation is one load the worker watches. It lives from the install
// request until the controller completes it, the watchdog kills it, or a stop
// ends it.
type loadOperation struct {
	id         string
	processKey string
	// instance is the incarnation of the backend process the operation started
	// or attached to. Empty until the process exists.
	instance string
	// anonymous marks an operation made for a request that named none. It is
	// bounded by its deadline only, never by missing renewals: the controller
	// that sent it does not know how to renew.
	anonymous bool
	deadline  time.Time
	lastRenew time.Time
	// expired is set by the watchdog. A caller still inside the install reads
	// it to learn that its backend was killed under it.
	expired bool
}

func (s *backendSupervisor) killTTL() time.Duration {
	if s.opKillTTL > 0 {
		return s.opKillTTL
	}
	return defaultOperationKillTTL
}

// beginOperation registers the load a backend.install request belongs to. It
// returns nil for an install with no model (an admin backend install): there is
// no load to bound.
func (s *backendSupervisor) beginOperation(req workerctl.BackendInstallRequest) *loadOperation {
	if req.ModelID == "" {
		return nil
	}
	now := time.Now()
	op := &loadOperation{
		id:         req.OperationID,
		processKey: model.BackendProcessKey(req.ModelID, int(req.ReplicaIndex)),
		lastRenew:  now,
		deadline:   now.Add(defaultOperationDeadline),
	}
	if req.DeadlineMs > 0 {
		op.deadline = now.Add(time.Duration(req.DeadlineMs) * time.Millisecond)
	}
	if op.id == "" {
		op.id = "anonymous-" + uuid.NewString()
		op.anonymous = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.operations == nil {
		s.operations = make(map[string]*loadOperation)
	}
	if existing, ok := s.operations[op.id]; ok {
		// A retry of the same install: keep its record, extend its lease.
		existing.lastRenew = now
		return existing
	}
	s.operations[op.id] = op
	return op
}

// attachOperation records which process instance an operation runs, and stamps
// the operation on the process so inventories can name it.
func (s *backendSupervisor) attachOperation(op *loadOperation) (instance string) {
	if op == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if bp, ok := s.processes[op.processKey]; ok {
		op.instance = bp.instance
		bp.operationID = op.id
		return bp.instance
	}
	return ""
}

// endOperation forgets an operation. It is a no-op for an unknown one.
func (s *backendSupervisor) endOperation(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.operations[id]
	if !ok {
		return false
	}
	delete(s.operations, id)
	if bp, ok := s.processes[op.processKey]; ok && bp.operationID == id {
		bp.operationID = ""
	}
	return true
}

// operationExpired reports whether the watchdog killed this operation while the
// caller was still working on it.
func (s *backendSupervisor) operationExpired(op *loadOperation) bool {
	if op == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return op.expired
}

// serveOperations answers model.op: renew and complete operations.
func (s *backendSupervisor) serveOperations(_ context.Context, req workerctl.OperationRequest) workerctl.OperationReply {
	var reply workerctl.OperationReply
	now := time.Now()

	s.mu.Lock()
	for _, id := range req.Renew {
		if op, ok := s.operations[id]; ok {
			op.lastRenew = now
			reply.Renewed = append(reply.Renewed, id)
		} else {
			reply.Unknown = append(reply.Unknown, id)
		}
	}
	s.mu.Unlock()

	for _, id := range req.Complete {
		if s.endOperation(id) {
			reply.Completed = append(reply.Completed, id)
		} else {
			// Already ended is the state the caller asked for.
			reply.Completed = append(reply.Completed, id)
		}
	}
	return reply
}

// runOperationWatchdog kills operations the controller stopped renewing, and
// those past their deadline. It returns when ctx ends.
func (s *backendSupervisor) runOperationWatchdog(ctx context.Context) {
	tick := s.opTick
	if tick <= 0 {
		tick = operationTickInterval
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireOperations(time.Now())
		}
	}
}

// expireOperations is one watchdog pass. An operation expires when its deadline
// passed or, unless it is anonymous, when no renewal arrived for the kill TTL.
//
// A backend that already answers READY has finished loading, so killing it
// would destroy a model that serves. That happens when a completion message was
// lost. The operation is then dropped, not killed. A process that cannot answer
// (still loading, or hung) is killed.
func (s *backendSupervisor) expireOperations(now time.Time) {
	type victim struct {
		op  *loadOperation
		key string
		bp  *backendProcess
	}
	var victims []victim

	s.mu.Lock()
	for id, op := range s.operations {
		late := now.After(op.deadline)
		silent := !op.anonymous && now.Sub(op.lastRenew) > s.killTTL()
		if !late && !silent {
			continue
		}
		delete(s.operations, id)
		op.expired = true
		v := victim{op: op, key: op.processKey}
		if bp, ok := s.processes[op.processKey]; ok && (op.instance == "" || bp.instance == op.instance) {
			v.bp = bp
			bp.operationID = ""
		}
		victims = append(victims, v)
	}
	s.mu.Unlock()

	for _, v := range victims {
		if v.bp == nil {
			xlog.Warn("Load operation expired before its process existed", "operation", v.op.id, "processKey", v.key)
			continue
		}
		if s.backendReady(v.bp) {
			xlog.Info("Load operation expired but the backend already serves; leaving it running", "operation", v.op.id, "processKey", v.key)
			continue
		}
		xlog.Warn("Killing a load operation that was not renewed or passed its deadline",
			"operation", v.op.id, "processKey", v.key)
		if err := s.stopBackendExactBP(v.key, v.bp, true); err != nil {
			xlog.Error("Failed to kill expired load operation", "operation", v.op.id, "processKey", v.key, "error", err)
		}
	}
}

// backendReady reports whether the backend answers a status call with READY.
func (s *backendSupervisor) backendReady(bp *backendProcess) bool {
	if s.readyFn != nil {
		return s.readyFn(bp.addr)
	}
	client := grpc.NewClientWithToken(bp.addr, false, nil, false, s.cfg.RegistrationToken)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := client.Status(ctx)
	return err == nil && st.GetState() == pb.StatusResponse_READY
}

// checkOperationTarget decides whether a stop that names an operation may act on
// the process under req.ProcessKey. It holds s.mu. It refuses anything that is
// not exactly this operation's process: a stop for a load must never become an
// unload of a model that finished loading, or a stop of someone else's process.
func (s *backendSupervisor) checkOperationTarget(req workerctl.ModelStopRequest, bp *backendProcess) error {
	op, known := s.operations[req.OperationID]
	switch {
	case known && op.processKey != req.ProcessKey:
		return fmt.Errorf("operation %s runs process %s, not %s", req.OperationID, op.processKey, req.ProcessKey)
	case known && op.instance != "" && bp.instance != op.instance:
		return fmt.Errorf("operation %s ran a different process instance", req.OperationID)
	case !known && bp.operationID != req.OperationID:
		// The operation ended (or never was) and the process is not its own.
		return fmt.Errorf("process %s does not belong to operation %s", req.ProcessKey, req.OperationID)
	case req.ProcessInstance != "" && bp.instance != req.ProcessInstance:
		return fmt.Errorf("process instance mismatch for %s", req.ProcessKey)
	}
	return nil
}
