package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// maxDrivePicks bounds how many agent workers one run is offered to. A worker
// that is full is no fault of the fleet, but a fleet whose first three workers
// have no slot is busy, and the claim goes back to the pool with a wait.
const maxDrivePicks = 3

// The ports below exist because neither jobs to nodes nor nodes to jobs is an
// import edge, and neither should become one for a driver. Each is named for what
// it does here and not for the type that satisfies it.

// AgentPicker chooses an agent worker for a run, leaving out those already tried.
// Satisfied by *nodes.AgentSelector.
type AgentPicker interface {
	PickConnectedExcluding(ctx context.Context, tried map[string]bool) (nodeID, nodeType string, err error)
}

// StreamCaller issues one streaming control request. Satisfied by
// *nodes.ControlClient.
type StreamCaller interface {
	CallStreaming(ctx context.Context, nodeID, verb string, req, reply any,
		onProgress func(subject string, raw json.RawMessage)) error
}

// ProgressBroadcaster publishes the line that a worker asked to have published,
// when the node type of the worker may ask for it. Satisfied by
// *nodes.Rebroadcaster. It returns a bool and no error on purpose: a refused or
// failed publish is not the verdict of the worker about the work.
type ProgressBroadcaster interface {
	Handle(nodeType, subject string, raw json.RawMessage) bool
}

// TerminalWriter records the terminal state of a job. Satisfied by *JobStore.
type TerminalWriter interface {
	UpdateJobStatus(jobID, status, result, errMsg string) error
}

var _ TerminalWriter = (*JobStore)(nil)

// AgentDriverConfig is what an AgentDriver needs.
type AgentDriverConfig struct {
	Picker    AgentPicker
	Control   StreamCaller
	Broadcast ProgressBroadcaster
	// Store records the terminal state of the jobs that name one. Without it a
	// job is not closed by this driver, and its result reaches the store only
	// through the broadcast of the worker.
	Store TerminalWriter
	// Hints is where the cancel of a job is heard. Without it a cancel does not
	// reach a run.
	Hints messaging.Broadcaster
}

// AgentDriver is the handler of the claims on the tunnel carrier. A frontend
// replica claims the work, and the driver hands it to an agent worker and reads
// the stream that comes back. The replica claims, and the worker does not,
// because an agent worker has no database.
//
// The events of the run, its progress and its result arrive as lines of the
// response that this replica is already reading. That is what lets the terminal
// state be written before the claim is completed, by the order of the calls and
// not by a retry.
type AgentDriver struct {
	cfg AgentDriverConfig

	// cancels holds a function that ends the stream of each job that this replica
	// drives. A cancel is heard by every replica and applied by the one that holds
	// the job.
	cancels messaging.CancelRegistry
	// cancelled remembers the jobs whose stream a cancel ended, so that the end
	// of the stream is read as the answer to the cancel and not as a link that
	// broke.
	cancelled cancelledJobs
	sub       atomic.Pointer[messaging.Subscription]
}

// NewAgentDriver returns a driver. It refuses a configuration that would claim
// work and never hand it on.
func NewAgentDriver(cfg AgentDriverConfig) (*AgentDriver, error) {
	if cfg.Picker == nil {
		return nil, errors.New("the agent driver was built with no way to pick an agent worker")
	}
	if cfg.Control == nil {
		return nil, errors.New("the agent driver was built with no control transport to reach an agent worker over")
	}
	return &AgentDriver{cfg: cfg}, nil
}

// Start listens for the cancel of jobs.
//
// A cancel has no consumer on the NATS carrier: Dispatcher.Cancel publishes it
// and nothing subscribes, as before. On this carrier the replica that holds the
// job ends its stream, and the run on the worker ends with its request.
func (d *AgentDriver) Start(_ context.Context) error {
	if d.cfg.Hints == nil {
		return nil
	}
	sub, err := messaging.SubscribeJSON(d.cfg.Hints, messaging.SubjectJobCancelWildcard, func(evt CancelEvent) {
		d.cancelJob(evt.JobID)
	})
	if err != nil {
		return fmt.Errorf("subscribing to the cancel of jobs: %w", err)
	}
	d.sub.Store(&sub)
	return nil
}

// Stop ends the subscription to the cancel of jobs.
func (d *AgentDriver) Stop() {
	if sub := d.sub.Swap(nil); sub != nil {
		_ = (*sub).Unsubscribe()
	}
}

func (d *AgentDriver) cancelJob(jobID string) {
	if jobID == "" {
		return
	}
	// Marked first: the stream ends as soon as the function runs, and the driver
	// that sees it end must already know why.
	d.cancelled.add(jobID)
	if !d.cancels.Cancel(jobID) {
		d.cancelled.remove(jobID)
	}
}

// Handler returns the handler of a kind, for a ClaimConsumer.
func (d *AgentDriver) Handler(kind messaging.WorkKind) (messaging.WorkHandler, error) {
	switch kind {
	case messaging.WorkAgentRun:
		return d.handler(kind, workerctl.VerbAgentExecute), nil
	case messaging.WorkMCPCI:
		return d.handler(kind, workerctl.VerbMCPCIRun), nil
	case messaging.WorkTask:
		return d.task, nil
	}
	return nil, fmt.Errorf("the agent driver has no handler for the work kind %q", kind)
}

// task answers a plain task. No worker serves one, and none did on the NATS
// carrier: the job was published to a subject nobody consumed and stayed running
// until the reaper failed it. The claim row makes that visible and does not
// change it. The row is completed, so that it does not wait for ever, and the job
// is left to the same reaper, so that both carriers behave alike.
func (d *AgentDriver) task(_ context.Context, payload []byte, _ messaging.Publisher) error {
	xlog.Warn("Dropping a plain task job: no worker in this deployment serves it, and the reaper will fail the job",
		"job", jobIDOf(payload))
	return nil
}

func (d *AgentDriver) handler(kind messaging.WorkKind, verb string) messaging.WorkHandler {
	return func(ctx context.Context, payload []byte, _ messaging.Publisher) error {
		return d.drive(ctx, kind, verb, payload)
	}
}

// JobStatusReader reads the status of a job. *JobStore is one. The driver reads
// it before it hands a job to a worker, so that a job that was cancelled while it
// waited in the queue is not run.
type JobStatusReader interface {
	JobStatus(jobID string) (string, error)
}

// drive hands one claim to a worker and returns what settles it:
//
//   - nil when the worker answered, when the job was cancelled, and when a run
//     that had started lost its link (the job is then closed as failed);
//   - ErrKeepClaim when the worker answered and the answer could not be written;
//   - an error marked noAnswerYet when the run was not started: no worker, no
//     route, no free slot, an old worker, or the carrier was released first;
//   - any other error when a worker was reached and the call failed before a
//     single line of the run came back.
//
// A run that has started is never released. Releasing it would start the job a
// second time, and a job may run a CI pipeline or call an agent with side effects.
func (d *AgentDriver) drive(ctx context.Context, kind messaging.WorkKind, verb string, payload []byte) error {
	jobID := ""
	if kind == messaging.WorkMCPCI {
		jobID = jobIDOf(payload)
	}
	if jobID != "" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		// Registered before the status is read: a cancel that comes after the
		// read ends the run, and one that came before is in the status.
		d.cancels.Register(jobID, cancel)
		defer d.cancels.Deregister(jobID)
		defer d.cancelled.remove(jobID)
	}

	scope := broadcastScope(kind, jobID, payload)

	// started becomes true when the first line of the run comes back. From then on
	// the job may have done work, and the claim is not released.
	var started atomic.Bool
	tried := make(map[string]bool, maxDrivePicks)
	var last error
	for range maxDrivePicks {
		if d.cancelledNow(ctx, jobID) {
			return d.persistCancelled(jobID)
		}
		nodeID, nodeType, err := d.cfg.Picker.PickConnectedExcluding(ctx, tried)
		if err != nil {
			switch {
			case carrierReleased(ctx):
				return noAnswerYet(fmt.Errorf("the carrier was released before the %s claim started: %w", kind, err))
			case d.cancelledNow(ctx, jobID):
				return d.persistCancelled(jobID)
			case last != nil:
				// Every worker that was offered the run had no slot, no route or an
				// old build. That is the evidence, and the end of the list is not.
				return noAnswerYet(last)
			}
			return noAnswerYet(fmt.Errorf("picking an agent worker for a %s claim: %w", kind, err))
		}
		tried[nodeID] = true

		var reply workerctl.RunReply
		err = d.cfg.Control.CallStreaming(ctx, nodeID, verb, json.RawMessage(payload), &reply,
			func(subject string, raw json.RawMessage) {
				started.Store(true)
				d.rebroadcast(nodeType, subject, raw, scope)
			})
		switch {
		case err == nil:
			return d.persistTerminal(&reply)
		case carrierReleased(ctx):
			if started.Load() {
				return d.cutOff(jobID)
			}
			return noAnswerYet(fmt.Errorf("the carrier was released before the %s claim started: %w", kind, err))
		case jobID != "" && d.cancelled.has(jobID) && ctx.Err() != nil:
			// The stream was ended by the cancel of the job. The worker's run dies
			// with its request, and the job is closed here, because nothing else
			// will.
			return d.persistCancelled(jobID)
		case !started.Load() && (errors.Is(err, workerctl.ErrWorkerBusy) ||
			errors.Is(err, workerctl.ErrNoRoute) || errors.Is(err, workerctl.ErrVerbNotServed)):
			// The run did not start on this worker: it has no slot, no route is
			// held to it, or it is too old to serve the verb. The next worker may
			// take it, and the claim waits for no backoff.
			last = fmt.Errorf("offering a %s claim to agent worker %q: %w", kind, nodeID, err)
			continue
		case started.Load():
			return d.failStarted(jobID, nodeID, err)
		default:
			return fmt.Errorf("driving a %s claim on agent worker %q: %w", kind, nodeID, err)
		}
	}
	return noAnswerYet(last)
}

// cancelledNow says whether the job was cancelled: this replica ended its run for
// that reason, or the job row says so. A job cancelled while it waited in the
// queue has no run to end, so the row is the only place that knows.
func (d *AgentDriver) cancelledNow(ctx context.Context, jobID string) bool {
	if jobID == "" {
		return false
	}
	if d.cancelled.has(jobID) && ctx.Err() != nil {
		return true
	}
	reader, ok := d.cfg.Store.(JobStatusReader)
	if !ok {
		return false
	}
	status, err := reader.JobStatus(jobID)
	return err == nil && status == "cancelled"
}

// persistCancelled closes a job as cancelled. The claim is then completed: a
// cancel is an answer.
func (d *AgentDriver) persistCancelled(jobID string) error {
	return d.persistTerminal(&workerctl.RunReply{JobID: jobID, Status: "cancelled", Error: "cancelled"})
}

// failStarted settles the claim of a run that had started when its link broke.
// The claim is completed and not released, because the run may have done work
// that a second run would repeat. The job is closed as failed, so that it does
// not stay running until the reaper finds it. A run with no job is a chat, whose
// output reached the user as events; there is nothing to close.
func (d *AgentDriver) failStarted(jobID, nodeID string, cause error) error {
	xlog.Error("The link to an agent worker broke while a run was in progress; the run is not started again",
		"job", jobID, "node", nodeID, "error", cause)
	if jobID == "" {
		return nil
	}
	return d.persistTerminal(&workerctl.RunReply{
		JobID: jobID, Status: "failed",
		Error: "the link to the worker broke while the job ran: " + cause.Error(),
	})
}

// failExhausted records that a job could not be run after the failures that the
// consumer allows. It is the OnExhausted of the consumer.
func (d *AgentDriver) failExhausted(_ context.Context, _ messaging.WorkKind, payload []byte, cause error) error {
	jobID := jobIDOf(payload)
	if jobID == "" || d.cfg.Store == nil {
		return nil
	}
	return d.cfg.Store.UpdateJobStatus(jobID, "failed", "", "the job could not be run after repeated failures: "+cause.Error())
}

// carrierReleased reports that ctx ended because the carrier of the loop was
// released at the end of a drain. It is not a stop of the replica, and not the
// cancel of a job.
func carrierReleased(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), messaging.ErrCarrierReleased)
}

// cutOff settles the claim of a run that the end of the drain cut off after it had
// started. The claim is completed, and not released: the run did start, so a
// carrier that took over the queue must not start it a second time. The job is
// left as it is, and the reaper fails it if it stays running, as it does for a job
// that lost its worker. A run that had not started is released instead, and the
// carrier that takes over runs it.
func (d *AgentDriver) cutOff(jobID string) error {
	xlog.Warn("A run was cut off by the end of the drain of its carrier; its job is left to the reaper", "job", jobID)
	return nil
}

// broadcastScope returns the subjects that the run being driven may publish on, or
// nil when the run names none. A worker that holds one run may ask the frontend to
// publish the progress of that run, and not the progress or the events of another
// job or another user: the allow list of the node type is wide, and the run is the
// narrower right.
func broadcastScope(kind messaging.WorkKind, jobID string, payload []byte) map[string]bool {
	switch kind {
	case messaging.WorkMCPCI:
		if jobID == "" {
			return nil
		}
		return map[string]bool{
			messaging.SubjectJobProgress(jobID): true,
			messaging.SubjectJobResult(jobID):   true,
		}
	case messaging.WorkAgentRun:
		var evt struct {
			AgentName string `json:"agent_name"`
			UserID    string `json:"user_id"`
		}
		if err := json.Unmarshal(payload, &evt); err != nil || evt.AgentName == "" {
			return nil
		}
		return map[string]bool{messaging.SubjectAgentEvents(evt.AgentName, evt.UserID): true}
	}
	return nil
}

// rebroadcast publishes the broadcast that a progress line asked for. A line with
// no subject is meant for the caller alone, as every private tick is, and is not
// passed on: the allow list denies the empty subject, and passing it would log a
// refusal for each ordinary tick. A line that names a subject outside the scope of
// the run is refused. A run with no scope is held to the allow list of its node
// type only.
func (d *AgentDriver) rebroadcast(nodeType, subject string, raw json.RawMessage, scope map[string]bool) {
	if subject == "" || d.cfg.Broadcast == nil {
		return
	}
	if scope != nil && !scope[subject] {
		xlog.Warn("Refusing a broadcast that a worker asked for outside the run it was given", "nodeType", nodeType, "subject", subject)
		return
	}
	d.cfg.Broadcast.Handle(nodeType, subject, raw)
}

// persistTerminal writes the terminal state of the job that a reply names. A run
// that names none is an agent chat, whose output reaches the user as events.
//
// A reply that names a job and no status closes the job as failed. The outcome
// to avoid is a job left running after its work has ended.
func (d *AgentDriver) persistTerminal(reply *workerctl.RunReply) error {
	if reply == nil || reply.JobID == "" || d.cfg.Store == nil {
		return nil
	}
	status, errMsg := reply.Status, reply.Error
	if status == "" {
		status, errMsg = "failed", "the worker answered without naming a terminal status"
	}
	if err := d.cfg.Store.UpdateJobStatus(reply.JobID, status, reply.Result, errMsg); err != nil {
		// The work ran. Releasing the claim would run it again, so the claim stays
		// with this replica, and it becomes claimable only if this replica dies.
		return fmt.Errorf("writing the terminal state of job %q: %w: %w", reply.JobID, err, ErrKeepClaim)
	}
	return nil
}

// jobIDOf reads the job id out of the payload of an MCP CI claim. It returns ""
// for anything that is not a JobEvent.
func jobIDOf(payload []byte) string {
	var evt JobEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return ""
	}
	return evt.JobID
}

// cancelledJobs is a set of job ids, safe for concurrent use.
type cancelledJobs struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func (c *cancelledJobs) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ids == nil {
		c.ids = map[string]struct{}{}
	}
	c.ids[id] = struct{}{}
}

func (c *cancelledJobs) remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.ids, id)
}

func (c *cancelledJobs) has(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.ids[id]
	return ok
}
