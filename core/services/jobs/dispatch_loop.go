// SPDX-License-Identifier: MIT

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/concurrency"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// The three ports below exist because neither `jobs` to `nodes` nor `nodes` to
// `jobs` is an import edge today and neither should become one for a dispatch
// loop. Each is named for what it DOES here, never for the concrete type that
// satisfies it: nodes.Rebroadcaster is a struct, and a local interface called
// Rebroadcaster in package jobs would be a second type with one name whose only
// implementation is the first, which is exactly the shape a reader
// mis-resolves.

// AgentPicker chooses a worker to hand a claim to. Satisfied by
// *nodes.AgentSelector.
type AgentPicker interface {
	PickConnected(ctx context.Context) (nodeID, nodeType string, err error)
}

// StreamCaller issues one streaming control RPC. Satisfied by
// *nodes.ControlClient.
type StreamCaller interface {
	CallStreaming(ctx context.Context, nodeID, path string, req, reply any,
		onProgress func(subject string, raw json.RawMessage)) error
}

// ProgressBroadcaster re-publishes a worker's progress line when that worker's
// node type is allowed to ask for it. Satisfied by *nodes.Rebroadcaster.
// It returns a bool and no error, deliberately: see nodes.Rebroadcaster.Handle.
type ProgressBroadcaster interface {
	Handle(nodeType, subject string, raw json.RawMessage) bool
}

// TerminalWriter records a dispatched job's terminal state.
//
// A port rather than *JobStore so the ORDER of the two writes settleClaim makes
// can be pinned: a store that refuses must leave the claim in place, and there
// is no way to make the real store refuse on demand.
type TerminalWriter interface {
	UpdateJobStatus(jobID, status, result, errMsg string) error
}

// The real writer, asserted here so a signature drift fails to COMPILE in the
// file that states the contract rather than in the wiring, which opens a
// database and therefore has no unit spec.
var _ TerminalWriter = (*JobStore)(nil)

// ClaimReply is the terminal line a worker sends for a dispatched claim.
//
// It is the same four fields JobResultEvent carries, because it says the same
// thing; what changed is the carrier. On the bus a result was a message the
// frontend had to already be subscribed to. Here it is the last line of the
// response body the claiming replica is reading, so there is no window in which
// it can be published to nobody.
type ClaimReply struct {
	JobID  string `json:"job_id,omitempty"`
	Status string `json:"status,omitempty"` // "completed" | "failed" | "cancelled"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	// DefaultDispatchInterval is how often a replica looks for claimable work.
	DefaultDispatchInterval = 2 * time.Second

	// DefaultMaxConcurrent bounds how many claims one replica drives at once.
	//
	// It is not a tuning knob so much as the difference between a working queue
	// and a serial one. A dispatched claim is driven SYNCHRONOUSLY: the RPC's
	// response body is what carries the worker's progress and its terminal
	// line, so the goroutine holding the claim is reading it for as long as the
	// work runs, which for an MCP CI job is up to ten minutes by default. With
	// one in flight, the second queued job in a deployment waits for the first
	// to finish, which the queue group this replaces never did.
	DefaultMaxConcurrent = 4

	// DefaultClaimLiveness is the replica-liveness window the reap measures an
	// owner's absence against.
	//
	// cluster.InstanceLiveness rather than a number of this package's own,
	// because it is the SAME fact: a replica that its peers have written off is
	// one whose claims nobody is driving. A second window here would let a
	// replica be dead for the tunnel table and alive for the claim table.
	DefaultClaimLiveness = cluster.InstanceLiveness
)

// reasonNoTaskDispatcher is what a plain task job is failed with.
//
// No agent worker serves ClaimKindTask, exactly as no NATS subscriber ever did:
// the frontend published jobs.new and, in every deployment this ships in,
// nothing consumed it. That was invisible because a publish to an empty queue
// group succeeds and the job row simply stayed `running` for ever. A claim row
// makes it visible, and this makes it legible.
const reasonNoTaskDispatcher = "no worker in this deployment serves plain task jobs"

// DispatchConfig is everything a DispatchLoop needs.
type DispatchConfig struct {
	DB        *gorm.DB
	Owner     string              // the instance id
	Selector  AgentPicker         // *nodes.AgentSelector
	Control   StreamCaller        // *nodes.ControlClient
	Broadcast ProgressBroadcaster // *nodes.Rebroadcaster
	Store     TerminalWriter      // *jobs.JobStore
	Interval  time.Duration       // poll interval, DefaultDispatchInterval when zero
	Liveness  time.Duration       // replica-liveness window, DefaultClaimLiveness when zero
	// MaxConcurrent is how many claims this replica drives at once,
	// DefaultMaxConcurrent when zero. See that constant.
	MaxConcurrent int
}

// DispatchLoop claims work on this replica and drives it on an agent worker.
//
// The claim is made by a FRONTEND replica and never by an agent worker, because
// an agent worker has no database and so cannot claim. What the worker gets is
// a streaming control RPC, which is what puts its progress, its agent SSE
// events and its terminal result on the response body this replica is already
// reading. That is what lets the terminal line be persisted BEFORE the claim is
// released, structurally rather than by retry.
type DispatchLoop struct {
	db        *gorm.DB
	owner     string
	selector  AgentPicker
	control   StreamCaller
	broadcast ProgressBroadcaster
	store     TerminalWriter
	interval  time.Duration
	liveness  time.Duration

	// sem bounds in-flight dispatches and wg joins them, so Stop returns only
	// once every claim this replica holds has been settled. A claim left held
	// by a process that has already exited is one no other replica may take
	// until the liveness window has passed.
	sem chan struct{}
	wg  sync.WaitGroup
	// unregistered remembers that this replica has already said it cannot
	// claim, so the refusal is one line per transition rather than one per
	// poll interval for as long as the misconfiguration lasts.
	unregistered atomic.Bool
	cancel       context.CancelFunc
	done         chan struct{}
}

// NewDispatchLoop returns the dispatch loop for one replica.
//
// Every refusal below is a piece of wiring whose absence has no symptom. A loop
// with no selector claims rows and releases them for ever; one with no control
// client does the same; one with no owner writes claims that the reap cannot
// attribute. All three present as work that is accepted and never done, which
// is indistinguishable at the API from a busy deployment.
func NewDispatchLoop(cfg DispatchConfig) (*DispatchLoop, error) {
	if cfg.DB == nil {
		return nil, errors.New("the dispatch loop was built with no database to claim work from")
	}
	if cfg.Owner == "" {
		return nil, errors.New("the dispatch loop was built with no instance id: its claims could not be told from ones held by a dead replica")
	}
	if cfg.Selector == nil {
		return nil, errors.New("the dispatch loop was built with no way to pick an agent worker")
	}
	if cfg.Control == nil {
		return nil, errors.New("the dispatch loop was built with no control transport to reach an agent worker over")
	}
	l := &DispatchLoop{
		db:        cfg.DB,
		owner:     cfg.Owner,
		selector:  cfg.Selector,
		control:   cfg.Control,
		broadcast: cfg.Broadcast,
		store:     cfg.Store,
		interval:  cfg.Interval,
		liveness:  cfg.Liveness,
		done:      make(chan struct{}),
	}
	if l.interval <= 0 {
		l.interval = DefaultDispatchInterval
	}
	if l.liveness <= 0 {
		l.liveness = DefaultClaimLiveness
	}
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	l.sem = make(chan struct{}, maxConcurrent)
	return l, nil
}

// Start begins claiming and dispatching until ctx is cancelled or Stop is
// called.
func (l *DispatchLoop) Start(ctx context.Context) error {
	ctx, l.cancel = context.WithCancel(ctx)
	go l.run(ctx)
	xlog.Info("Job dispatch loop started", "instance", l.owner, "interval", l.interval)
	return nil
}

// Stop ends the loop and waits for every in-flight dispatch to settle.
//
// Waiting matters: a dispatch may be mid-RPC holding a claim, and returning
// before it settles would leave the claim held by a replica that is on its way
// out, which the next reap can only release after the liveness window.
func (l *DispatchLoop) Stop() {
	if l == nil || l.cancel == nil {
		return
	}
	l.cancel()
	<-l.done
}

func (l *DispatchLoop) run(ctx context.Context) {
	// Ordered so the wait happens BEFORE done is closed: Stop reads done, and a
	// Stop that returned while a dispatch was still in flight would leave the
	// claim held by a process on its way out.
	defer close(l.done)
	defer l.wg.Wait()
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case l.sem <- struct{}{}:
			default:
				// Already driving as much as this replica may. Skipping the
				// tick rather than queueing is deliberate: a claim taken now
				// would be held by this replica with nothing driving it, and
				// another replica could have taken it instead.
				continue
			}
			l.wg.Add(1)
			concurrency.SafeGo(func() {
				defer l.wg.Done()
				defer func() { <-l.sem }()
				if err := l.DispatchOnce(ctx); err != nil && ctx.Err() == nil {
					xlog.Warn("A dispatch tick failed", "instance", l.owner, "error", err)
				}
			})
		}
	}
}

// DispatchOnce reaps what dead replicas left, then claims and drives at most
// one unit of work.
//
// Exported because it is the seam every dispatch spec drives: a tick that a
// spec has to wait for is a spec that fails one run in ten.
func (l *DispatchLoop) DispatchOnce(ctx context.Context) error {
	// This replica's OWN liveness, checked before it touches the queue at all.
	//
	// A replica with no row in the instances table is invisible to its peers,
	// which is a supported if degraded state for serving requests but not for
	// holding a claim: the reap asks whether a claim's owner is live, so a
	// claim written here would be reaped out from under this replica while the
	// work was still running, and the work would run twice. Refusing to claim
	// is the conservative half of that choice, and it heals by itself the
	// moment membership registers.
	live, err := OwnerIsLive(ctx, l.db, l.owner, l.liveness)
	if err != nil {
		return fmt.Errorf("checking whether this replica may claim work: %w", err)
	}
	if !live {
		// Logged on the TRANSITION rather than on every tick. The state lasts
		// for as long as the misconfiguration does, and at one line per poll
		// interval it would bury everything else in the deployment's logs; a
		// line that scrolls the rest away is a line nobody reads.
		if l.unregistered.CompareAndSwap(false, true) {
			xlog.Error("This replica is not registered in the cluster, so it will not claim queued work: another replica could not tell its claims from abandoned ones",
				"instance", l.owner, "knob", "LOCALAI_DISTRIBUTED_ADVERTISE_ADDR")
		}
		return nil
	}
	if l.unregistered.CompareAndSwap(true, false) {
		xlog.Info("This replica is registered in the cluster again and is claiming queued work", "instance", l.owner)
	}

	if released, err := ReapAbandoned(ctx, l.db, l.liveness); err != nil {
		// Reported and not returned: a reap that could not run says nothing
		// about the claim this tick is about to take, and giving up here would
		// stop dispatch entirely over a read that will very likely succeed next
		// tick.
		xlog.Warn("Could not reap claims left by departed replicas", "error", err)
	} else if released > 0 {
		xlog.Info("Released work claimed by replicas that are no longer live", "count", released)
	}

	claim, err := ClaimNext(ctx, l.db, l.owner, []ClaimKind{ClaimKindMCPCI, ClaimKindAgentRun, ClaimKindTask})
	if errors.Is(err, ErrNoWork) {
		return nil
	}
	if err != nil {
		return err
	}
	return l.drive(ctx, claim)
}

// drive hands one claimed row to a worker and settles it.
func (l *DispatchLoop) drive(ctx context.Context, claim *WorkClaim) error {
	path, served := verbFor(ClaimKind(claim.Kind))
	if !served {
		// A verdict this DEPLOYMENT reached without asking anyone, which is
		// still a verdict: no build of any worker serves this kind, so retrying
		// it on another worker cannot change the answer. Settled as an answer
		// for that reason, and with a reason string, because the alternative is
		// the row this change exists to make visible going quiet again.
		xlog.Warn("Failing a claim no worker in this deployment serves", "kind", claim.Kind, "claim", claim.ID)
		return l.settleClaim(ctx, claim, &ClaimReply{
			JobID:  jobIDOf(claim),
			Status: "failed",
			Error:  reasonNoTaskDispatcher,
		}, nil)
	}

	nodeID, nodeType, err := l.selector.PickConnected(ctx)
	if err != nil {
		// No worker was reachable. Nothing was asked of anyone and nothing was
		// learned, so the work goes back.
		return l.settleClaim(ctx, claim, nil, fmt.Errorf("picking an agent worker for a %s claim: %w", claim.Kind, err))
	}

	var reply ClaimReply
	callErr := l.control.CallStreaming(ctx, nodeID, path, json.RawMessage(claim.Payload), &reply,
		func(subject string, raw json.RawMessage) {
			l.rebroadcast(nodeType, subject, raw)
		})
	if callErr != nil {
		callErr = fmt.Errorf("driving a %s claim on agent worker %q: %w", claim.Kind, nodeID, callErr)
	}
	return l.settleClaim(ctx, claim, &reply, callErr)
}

// rebroadcast forwards one progress line's broadcast REQUEST.
//
// A line with no subject is for this caller alone, which is what every
// pre-existing progress tick is, and it is dropped here rather than handed on:
// the allow list denies the empty subject anyway, so passing it would turn
// every ordinary tick into a refusal that gets logged as a worker asking for
// something it has no business on.
//
// The node type travels from the SELECTION rather than being looked up again.
// It is what the allow list is keyed on, so getting it wrong denies everything
// or, worse, allows what should not be, and there must not be a second place in
// the tree that decides what a node's type is.
func (l *DispatchLoop) rebroadcast(nodeType, subject string, raw json.RawMessage) {
	if subject == "" || l.broadcast == nil {
		return
	}
	l.broadcast.Handle(nodeType, subject, raw)
}

// settleClaim is the ONE place the release rule is stated, and every exit path
// through drive calls it.
//
//	| Outcome                     | Action                              | Why |
//	| a reply line came back      | persist the terminal line, then     | the worker ran it and said what happened |
//	|                             | CompleteClaim                       | |
//	| anything else               | ReleaseClaim                        | an unreachable peer, a lost tunnel or a refused stream is not a verdict |
//
// The line between the two rows is whether a REPLY LINE was decoded, and it is
// deliberately NOT cluster.IsWorkerAnswer, though the plan for this task said
// it should be. IsWorkerAnswer accepts the tunnel's stream-refusal vocabulary,
// which a worker writes BEFORE any request body reaches its control server:
// ErrStreamTagUnknown and ErrStreamTargetUnavailable both mean "I could not
// carry this to my own control plane", which is the opposite of "I ran your job
// and here is what happened". Completing on those would DISCARD work that never
// ran, silently, which is the collapse this whole phase exists to prevent
// pointed at work instead of at nodes. nodes.agentVerb draws the same line for
// the same reason.
//
// The order of the two writes in the first row is the point of the row. The
// terminal line is persisted BEFORE the claim is completed, so a store that
// refuses leaves the claim in place; completing first would delete the only
// record that the work is outstanding and leave the job row `running` for ever,
// which is the dropped-result defect the bus carrier had.
func (l *DispatchLoop) settleClaim(ctx context.Context, claim *WorkClaim, reply *ClaimReply, callErr error) error {
	if callErr != nil {
		xlog.Warn("Releasing a claim whose dispatch obtained no answer", "claim", claim.ID, "kind", claim.Kind, "error", callErr)
		if err := ReleaseClaim(ctx, l.db, claim.ID); err != nil {
			return fmt.Errorf("%w (and the claim could not be released: %w)", callErr, err)
		}
		return callErr
	}
	if err := l.persistTerminal(reply); err != nil {
		// Left CLAIMED on purpose. The work ran, so releasing would run it
		// again; the row stays, attributed to this replica, and becomes
		// claimable only if this replica dies. That is a visible outstanding
		// row rather than a silent loss.
		return err
	}
	return CompleteClaim(ctx, l.db, claim.ID)
}

// persistTerminal writes the job row's terminal state, if the reply named a job.
//
// An agent run names none: its output reaches the user as SSE events, and there
// is no job row to close. A reply that names a job but no status is still
// closed out, as failed, because the one outcome this change exists to remove
// is a job left `running` after the work has finished.
func (l *DispatchLoop) persistTerminal(reply *ClaimReply) error {
	if reply == nil || reply.JobID == "" || l.store == nil {
		return nil
	}
	status, errMsg := reply.Status, reply.Error
	if status == "" {
		status, errMsg = "failed", "the worker answered without naming a terminal status"
	}
	if err := l.store.UpdateJobStatus(reply.JobID, status, reply.Result, errMsg); err != nil {
		return fmt.Errorf("persisting the terminal state of job %q: %w", reply.JobID, err)
	}
	return nil
}

// verbFor maps a claim kind onto the control verb that serves it.
//
// ClaimKindTask maps to nothing, and that is a fact about this deployment
// rather than an omission here: no worker build serves it. See
// reasonNoTaskDispatcher.
func verbFor(kind ClaimKind) (string, bool) {
	switch kind {
	case ClaimKindMCPCI:
		return workerctl.PathMCPCIRun, true
	case ClaimKindAgentRun:
		return workerctl.PathAgentExecute, true
	default:
		return "", false
	}
}

// jobIDOf reads the job id out of a claim's payload, for the kinds whose
// payload is a JobEvent. It returns "" for anything else, which the caller
// treats as "there is no job row to close".
func jobIDOf(claim *WorkClaim) string {
	var evt JobEvent
	if err := json.Unmarshal(claim.Payload, &evt); err != nil {
		return ""
	}
	return evt.JobID
}
