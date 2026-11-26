package upgraderun

import (
	"strings"
	"time"
)

// Config holds the limits the reducer schedules with.
type Config struct {
	// BackendLimit is how many backend items run at once. The gallery
	// service has the same limit (BackendOperationLimit); keeping it here too
	// means a queued item says why it waits instead of looking started.
	BackendLimit int
	// ModelLimit is how many model items run at once. A model upgrade stops
	// the model and loads the new version as a check, so one at a time keeps
	// the host from running out of memory.
	ModelLimit int
}

func (c Config) backendLimit() int {
	if c.BackendLimit < 1 {
		return 1
	}
	return c.BackendLimit
}

func (c Config) modelLimit() int {
	if c.ModelLimit < 1 {
		return 1
	}
	return c.ModelLimit
}

// Env is what a reducer step reads besides the state and the event.
type Env struct {
	Config Config
	Now    time.Time
}

// Event is an input of the reducer.
type Event interface{ isEvent() }

// RunRequested creates the run. Items come from the planner, in order, each
// queued or skipped.
type RunRequested struct {
	ID     string
	Source Source
	Actor  string
	Items  []Item
}

// ItemStarted: the operation of an item was accepted and has a job id.
type ItemStarted struct {
	Key         string
	JobID       string
	Cancellable bool
}

// ItemStartFailed: the operation of an item could not be started. Skip turns
// the item skipped (it became held, say) instead of failed.
type ItemStartFailed struct {
	Key       string
	ErrorKey  string
	Error     string
	Skip      bool
	Retryable bool
}

// ItemProgress is a new status of the operation of a running item.
type ItemProgress struct {
	Key         string
	Progress    int
	Message     string
	Cancellable bool
	Nodes       []NodeState
}

// JobOutcome is how the operation of an item ended.
type JobOutcome string

const (
	JobOK        JobOutcome = "ok"
	JobError     JobOutcome = "error"
	JobCancelled JobOutcome = "cancelled"
	// JobLost: the status of the operation is gone, so its outcome is
	// unknown. A fresh check decides.
	JobLost JobOutcome = "lost"
)

// ItemJobEnded: the operation of an item ended.
type ItemJobEnded struct {
	Key      string
	Outcome  JobOutcome
	ErrorKey string
	Error    string
	// RolledBack marks a failure that put the previous version back.
	RolledBack bool
	// Nodes is the final result of each node, when the operation has one.
	Nodes []NodeState
}

// VerifyResult is the answer of the fresh check of an item.
type VerifyResult string

const (
	// VerifyGone: the check no longer lists the item.
	VerifyGone VerifyResult = "gone"
	// VerifyListed: the check still lists the item with the same target.
	VerifyListed VerifyResult = "listed"
	// VerifyUnanswered: no check could answer.
	VerifyUnanswered VerifyResult = "unanswered"
)

// ItemVerified is the answer of the fresh check of an item.
type ItemVerified struct {
	Key    string
	Result VerifyResult
}

// CancelRequested cancels the whole run.
type CancelRequested struct{}

// CancelItem cancels one item.
type CancelItem struct{ Key string }

// RetryRequested queues the retryable items of a running run again.
type RetryRequested struct{}

// RestartRecovered: a runner took the run over after a restart, the death of
// the previous runner or the loss of its lock. Live holds the items whose
// operation still has a status: a lost lock does not stop the operations, so
// the new runner follows them. The operations of the other busy items died
// with the previous runner, and a fresh check decides them.
type RestartRecovered struct {
	Live map[string]bool
}

// NodeResult is the state of one node of a cluster backend item.
type NodeResult struct {
	Key  string
	Node NodeState
}

// Dismiss hides a finished run.
type Dismiss struct{}

func (RunRequested) isEvent()     {}
func (ItemStarted) isEvent()      {}
func (ItemStartFailed) isEvent()  {}
func (ItemProgress) isEvent()     {}
func (ItemJobEnded) isEvent()     {}
func (ItemVerified) isEvent()     {}
func (CancelRequested) isEvent()  {}
func (CancelItem) isEvent()       {}
func (RetryRequested) isEvent()   {}
func (RestartRecovered) isEvent() {}
func (NodeResult) isEvent()       {}
func (Dismiss) isEvent()          {}

// Effect is something the runner has to do after a step.
type Effect interface{ isEffect() }

// StartJob starts the operation of an item.
type StartJob struct{ Key string }

// CancelJob cancels the operation of an item.
type CancelJob struct {
	Key   string
	JobID string
}

// VerifyItem runs a fresh check for an item.
type VerifyItem struct{ Key string }

// RunEnded says the run reached its final state.
type RunEnded struct{ State RunState }

func (StartJob) isEffect()   {}
func (CancelJob) isEffect()  {}
func (VerifyItem) isEffect() {}
func (RunEnded) isEffect()   {}

// Result is the outcome of a reducer step. Err is a refusal of the event; the
// run is then unchanged and there are no effects.
type Result struct {
	Run     Run
	Effects []Effect
	Err     error
	// Changed reports whether the step changed the run.
	Changed bool
}

// Item messages. They are short plain text; the UI may show its own words for
// the phase instead.
const (
	msgStarting  = "starting"
	msgChecking  = "checking that the update applied"
	msgRecovered = "the server restarted; checking whether the update applied"
	msgStale     = "the operation ended but the update is still listed"
	// msgInterrupted is the end of a recovered item that the check still
	// lists: its operation did not end, the restart stopped it.
	msgInterrupted = "the update was interrupted by a restart"
	msgNoAnswer    = "no check could confirm the update"
	msgCancelled   = "cancelled"
)

// Reduce applies one event to a run. It never changes its input and does no
// I/O.
func Reduce(in Run, ev Event, env Env) Result {
	r := in.Clone()
	var fx []Effect

	if _, ok := ev.(RunRequested); !ok && r.ID == "" {
		return Result{Run: in, Err: refuse(CodeNotFound, "", "no such run")}
	}
	switch ev.(type) {
	case RunRequested, Dismiss:
	default:
		if !r.Active() {
			return Result{Run: in, Err: refuse(CodeRunFinished, "", "the run is over")}
		}
	}

	switch e := ev.(type) {
	case RunRequested:
		if r.ID != "" {
			return Result{Run: in, Err: refuse(CodeInvalid, "", "the run exists")}
		}
		r = Run{ID: e.ID, Source: e.Source, Actor: e.Actor, CreatedAt: env.Now, State: RunRunning}
		seen := map[string]bool{}
		for _, it := range e.Items {
			if seen[it.Key] {
				continue
			}
			seen[it.Key] = true
			if it.Phase != PhaseSkipped {
				it.Phase = PhaseQueued
			}
			r.Items = append(r.Items, it)
		}

	case ItemStarted:
		it := r.Item(e.Key)
		if it == nil || it.Phase != PhaseRunning || it.Verifying || it.JobID != "" {
			return Result{Run: in}
		}
		it.JobID = e.JobID
		it.Cancellable = e.Cancellable
		fx = append(fx, maybeCancel(it)...)

	case ItemStartFailed:
		it := r.Item(e.Key)
		if it == nil || it.Phase != PhaseRunning || it.JobID != "" {
			return Result{Run: in}
		}
		it.Cancellable, it.Verifying = false, false
		msg := safeText(e.Error)
		it.ErrorKey, it.Error, it.Message = orUnknown(e.ErrorKey), msg, msg
		if e.Skip {
			it.Phase, it.Retryable = PhaseSkipped, false
		} else {
			// The starter may know a retry is useless; the key decides too.
			it.Phase, it.Retryable = PhaseFailed, e.Retryable && RetryableKey(it.ErrorKey)
		}

	case ItemProgress:
		it := r.Item(e.Key)
		if it == nil || it.Phase != PhaseRunning || it.Verifying {
			return Result{Run: in}
		}
		it.Progress = clampProgress(e.Progress)
		if e.Message != "" {
			it.Message = safeText(e.Message)
		}
		it.Cancellable = e.Cancellable && it.JobID != ""
		if e.Nodes != nil {
			it.Nodes = safeNodes(e.Nodes)
		}
		fx = append(fx, maybeCancel(it)...)

	case ItemJobEnded:
		it := r.Item(e.Key)
		if it == nil || it.Phase != PhaseRunning || it.Verifying {
			return Result{Run: in}
		}
		it.Cancellable = false
		if e.Nodes != nil {
			it.Nodes = safeNodes(e.Nodes)
		}
		switch e.Outcome {
		case JobOK:
			// Not updated yet: the operation only says it ended. The item is
			// updated when a fresh check no longer lists it.
			it.Verifying, it.Progress, it.Message = true, 100, msgChecking
			fx = append(fx, VerifyItem{Key: it.Key})
		case JobCancelled:
			it.Phase, it.Message, it.Retryable = PhaseCancelled, msgCancelled, false
		case JobLost:
			it.Phase, it.Verifying, it.Message = PhaseUnconfirmed, true, msgChecking
			fx = append(fx, VerifyItem{Key: it.Key})
		default:
			it.Phase = PhaseFailed
			if e.RolledBack {
				it.Phase = PhaseRolledBack
			}
			msg := safeText(e.Error)
			it.ErrorKey, it.Error, it.Message = orUnknown(e.ErrorKey), msg, msg
			it.Retryable = RetryableKey(it.ErrorKey)
		}

	case ItemVerified:
		it := r.Item(e.Key)
		if it == nil || !it.Verifying {
			return Result{Run: in}
		}
		it.Verifying = false
		switch e.Result {
		case VerifyGone:
			it.Phase, it.Progress, it.Message = PhaseUpdated, 100, ""
			it.ErrorKey, it.Error, it.Retryable = "", "", false
		case VerifyListed:
			key, msg := ErrKeyStale, msgStale
			// The message of RestartRecovered marks an item that the restart
			// settled. It is stored with the item, so it survives another
			// restart before the check answers.
			if it.Message == msgRecovered {
				key, msg = ErrKeyInterrupted, msgInterrupted
			}
			it.Phase, it.ErrorKey, it.Error, it.Message, it.Retryable = PhaseFailed, key, msg, msg, RetryableKey(key)
		default:
			// No check answered: a retry runs the check again with the update.
			it.Phase, it.Message, it.Retryable = PhaseUnconfirmed, msgNoAnswer, true
		}

	case CancelRequested:
		if r.CancelRequested {
			return Result{Run: in}
		}
		r.CancelRequested = true
		for i := range r.Items {
			it := &r.Items[i]
			switch {
			case it.Phase == PhaseQueued:
				cancelQueued(it)
			case it.Phase == PhaseRunning && !it.Verifying:
				it.CancelWanted = true
				fx = append(fx, maybeCancel(it)...)
			}
		}

	case CancelItem:
		it := r.Item(e.Key)
		if it == nil {
			return Result{Run: in, Err: refuse(CodeNotFound, e.Key, "no such item in the run")}
		}
		switch {
		case it.Phase == PhaseQueued:
			cancelQueued(it)
		case it.Phase == PhaseRunning && !it.Verifying && it.CancelSent:
			return Result{Run: in}
		case it.Phase == PhaseRunning && !it.Verifying && it.Cancellable && it.JobID != "":
			it.CancelWanted = true
			fx = append(fx, maybeCancel(it)...)
		default:
			return Result{Run: in, Err: refuse(CodeNotCancellable, e.Key, "the item cannot be cancelled now")}
		}

	case RetryRequested:
		if r.CancelRequested {
			return Result{Run: in, Err: refuse(CodeRunFinished, "", "the run was cancelled")}
		}
		n := 0
		for i := range r.Items {
			it := &r.Items[i]
			if !it.Retryable || !retryablePhase(it) {
				continue
			}
			requeue(it)
			n++
		}
		if n == 0 {
			return Result{Run: in, Err: refuse(CodeNothingToRetry, "", "no item can be retried")}
		}

	case RestartRecovered:
		for i := range r.Items {
			it := &r.Items[i]
			if it.Phase == PhaseRunning && !it.Verifying && it.JobID != "" && e.Live[it.Key] {
				// The operation goes on: follow it. Its cancel is asked for
				// again if it was wanted.
				it.CancelSent = false
				fx = append(fx, maybeCancel(it)...)
				continue
			}
			if it.Busy() {
				it.Phase, it.Verifying, it.Cancellable = PhaseUnconfirmed, true, false
				it.CancelWanted, it.CancelSent = false, false
				it.Message = msgRecovered
				fx = append(fx, VerifyItem{Key: it.Key})
			}
		}

	case NodeResult:
		it := r.Item(e.Key)
		if it == nil {
			return Result{Run: in}
		}
		e.Node.Progress = clampProgress(e.Node.Progress)
		replaced := false
		for i := range it.Nodes {
			if it.Nodes[i].ID == e.Node.ID {
				it.Nodes[i], replaced = e.Node, true
			}
		}
		if !replaced {
			it.Nodes = append(it.Nodes, e.Node)
		}

	case Dismiss:
		if r.Active() {
			return Result{Run: in, Err: refuse(CodeRunNotFinished, "", "the run still runs")}
		}
		if r.Dismissed {
			return Result{Run: in}
		}
		r.Dismissed = true
		return Result{Run: r, Changed: true}
	}

	fx = append(fx, schedule(&r, env.Config)...)
	fx = append(fx, finishIfDone(&r, env.Now)...)
	return Result{Run: r, Effects: fx, Changed: true}
}

// maybeCancel asks for the cancel of an item's operation once it is wanted
// and the operation says it can stop.
func maybeCancel(it *Item) []Effect {
	if !it.CancelWanted || it.CancelSent || !it.Cancellable || it.JobID == "" {
		return nil
	}
	it.CancelSent = true
	return []Effect{CancelJob{Key: it.Key, JobID: it.JobID}}
}

// cancelQueued drops a queued item: it never runs, so there is no operation
// to cancel.
func cancelQueued(it *Item) {
	it.Phase, it.Message, it.QueuedReason, it.Retryable = PhaseCancelled, msgCancelled, "", false
}

func retryablePhase(it *Item) bool {
	switch it.Phase {
	case PhaseFailed, PhaseRolledBack:
		return true
	case PhaseUnconfirmed:
		return !it.Verifying
	}
	return false
}

func requeue(it *Item) {
	it.Phase, it.Progress, it.Message = PhaseQueued, 0, ""
	it.ErrorKey, it.Error, it.Retryable = "", "", false
	it.JobID, it.Cancellable, it.Verifying = "", false, false
	it.CancelWanted, it.CancelSent, it.Nodes = false, false, nil
}

func orUnknown(key string) string {
	if key == "" {
		return ErrKeyUnknown
	}
	return key
}

func clampProgress(p int) int {
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	}
	return p
}

// backendID is the backend an item installs, so a gallery and a library item
// of the same backend never run at once: they write the same directory.
func backendID(it *Item) string {
	if it.Kind == KindLibrary {
		if i := strings.LastIndex(it.Name, "/"); i >= 0 {
			return it.Name[i+1:]
		}
	}
	return it.Name
}

// schedule starts the queued items that may start and writes why the others
// wait. Backends go first, up to the backend limit and never two of the same
// backend; models go after every backend; add-ons go last, one at a time,
// because the add-on owner refuses a second lifecycle operation.
func schedule(r *Run, cfg Config) []Effect {
	if !r.Active() {
		return nil
	}
	var backendsLeft, modelsLeft bool
	var runningBackends, runningModels, runningAddons int
	busyBackend := map[string]bool{}
	for i := range r.Items {
		it := &r.Items[i]
		pending := it.Phase == PhaseQueued || it.Busy()
		switch {
		case it.Kind.IsBackend():
			backendsLeft = backendsLeft || pending
			if it.Busy() {
				runningBackends++
				busyBackend[backendID(it)] = true
			}
		case it.Kind == KindModel:
			modelsLeft = modelsLeft || pending
			if it.Busy() {
				runningModels++
			}
		case it.Kind == KindAddon:
			if it.Busy() {
				runningAddons++
			}
		}
	}
	if r.CancelRequested {
		return nil
	}

	var fx []Effect
	start := func(it *Item) {
		it.Phase, it.Progress, it.Message, it.QueuedReason = PhaseRunning, 0, msgStarting, ""
		it.JobID, it.Cancellable, it.Verifying = "", false, false
		fx = append(fx, StartJob{Key: it.Key})
	}
	for i := range r.Items {
		it := &r.Items[i]
		if it.Phase != PhaseQueued {
			continue
		}
		switch {
		case it.Kind.IsBackend():
			id := backendID(it)
			switch {
			case busyBackend[id]:
				it.QueuedReason = WaitSameBackend
			case runningBackends >= cfg.backendLimit():
				it.QueuedReason = WaitBackendLimit
			default:
				start(it)
				runningBackends++
				busyBackend[id] = true
			}
		case it.Kind == KindModel:
			switch {
			case backendsLeft:
				it.QueuedReason = WaitAfterBackends
			case runningModels >= cfg.modelLimit():
				it.QueuedReason = WaitOneModelAtATime
			default:
				start(it)
				runningModels++
			}
		case it.Kind == KindAddon:
			switch {
			case backendsLeft:
				it.QueuedReason = WaitAfterBackends
			case modelsLeft:
				it.QueuedReason = WaitAfterModels
			case runningAddons >= 1:
				it.QueuedReason = WaitOneAddonAtATime
			default:
				start(it)
				runningAddons++
			}
		}
	}
	return fx
}

// finishIfDone ends the run when nothing is queued or busy.
func finishIfDone(r *Run, now time.Time) []Effect {
	if !r.Active() {
		return nil
	}
	for i := range r.Items {
		if r.Items[i].Phase == PhaseQueued || r.Items[i].Busy() {
			return nil
		}
	}
	r.State = RunFinished
	if r.CancelRequested {
		r.State = RunCancelled
	}
	t := now
	r.FinishedAt = &t
	return []Effect{RunEnded{State: r.State}}
}
