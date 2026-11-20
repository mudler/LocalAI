// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	"github.com/mudler/xlog"
)

// An install the administrator approved for later runs in two steps. The
// approval is recorded first, as an asynchronous operation in the accepted
// phase of the journal (AcceptInstall), while the request that carries the
// one-time preview nonce is still in hand. The host's operation queue then
// runs it, one at a time, through the same lifecycle path as every other
// install (RunAccepted), or drops it when it is cancelled before it starts
// (DropAccepted). The order and the concurrency are the queue's; the manager
// keeps its own serialization of operations.
//
// The journal is the record across a restart: Reconcile fails an operation
// that is still accepted or running with the failure code "interrupted", so
// a queued install never runs after a restart without a person asking again.

// FailureCancelled is the failure code of an accepted install that was
// cancelled before it ran.
const FailureCancelled = "cancelled"

// InstallProgress is one report of an install that runs: the phase, and for
// the download the bytes read of the total (zero when not known). Cancellable
// is false once the package is being written, where a stop would leave a
// half-done change.
type InstallProgress struct {
	Phase        string
	Bytes, Total int64
	Cancellable  bool
}

// Phases an install reports, in order.
const (
	InstallPhaseResolving   = "resolving"
	InstallPhaseVerifying   = "verifying"
	InstallPhaseDownloading = "downloading"
	InstallPhaseChecking    = "checking"
	InstallPhaseCommitting  = "committing"
	// InstallPhaseStarting is an upgrade that starts its new generation, or
	// backs up and migrates the data of the add-on first. It cannot stop.
	InstallPhaseStarting = "starting"
)

type installProgressKey struct{}

// WithInstallProgress returns a context whose install reports its progress to
// report. It reaches the manager in the same process only; a call forwarded
// to another owner reports nothing.
func WithInstallProgress(ctx context.Context, report func(InstallProgress)) context.Context {
	return context.WithValue(ctx, installProgressKey{}, report)
}

func installProgressFrom(ctx context.Context) func(InstallProgress) {
	if f, ok := ctx.Value(installProgressKey{}).(func(InstallProgress)); ok && f != nil {
		return f
	}
	return func(InstallProgress) {}
}

// stageProgress turns the reports of a Stage into install reports.
func stageProgress(report func(InstallProgress)) func(packages.Progress) {
	return func(p packages.Progress) {
		switch p.Phase {
		case "verifying":
			report(InstallProgress{Phase: InstallPhaseVerifying, Cancellable: true})
		case "download":
			report(InstallProgress{Phase: InstallPhaseDownloading, Bytes: p.Bytes, Total: p.Total, Cancellable: true})
		case "verified":
			report(InstallProgress{Phase: InstallPhaseChecking, Cancellable: true})
		}
	}
}

// AcceptInstall records an install with the administrator's approval for a
// later run. It checks what can be checked without the package: the caller is
// an administrator, the add-on is not installed and has no operation in
// flight, and the approval is present. Repeating the same command under the
// same operation ID returns the recorded status.
func (m *Manager) AcceptInstall(ctx context.Context, actor contract.Actor, cmd LifecycleCommand) (OperationStatus, error) {
	if e := ctx.Err(); e != nil {
		return OperationStatus{}, e
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return OperationStatus{}, ErrForbidden
	}
	// A local file stays on the path that installs it at once: the staged
	// file belongs to the browser session that uploaded it.
	if cmd.Action != "install" || cmd.Approval == nil || cmd.SourceID == zipformat.SourceID || cmd.ExpectedDigest != "" || cmd.CandidateDigest != "" ||
		!addonID.MatchString(cmd.AddonID) || cmd.OperationID == "" || len(cmd.OperationID) > 256 {
		return OperationStatus{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return OperationStatus{}, ErrRecoveryRequired
	}
	if old, ok := m.journal.Operations[cmd.OperationID]; ok {
		if !old.Command.same(cmd) || !sameOperationOwner(old.Actor, actor) {
			return OperationStatus{}, ErrConflict
		}
		return old.Status, nil
	}
	if m.journal.Addons[cmd.AddonID].Installed {
		return OperationStatus{}, ErrConflict
	}
	for _, op := range m.journal.Operations {
		if op.Command.AddonID == cmd.AddonID && isActive(op.Status.Phase) {
			return OperationStatus{}, ErrConflict
		}
	}
	op := journalOperation{Command: cmd, Actor: actor, Async: true, StartedAt: time.Now().UTC().Format(time.RFC3339),
		Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: string(lifecycleAccepted)}}
	m.journal.Operations[cmd.OperationID] = op
	if e := m.persist(string(lifecycleAccepted)); e != nil {
		return OperationStatus{}, e
	}
	return op.Status, nil
}

// AcceptUpgrade records an explicit, pinned upgrade for a later run by the
// host's operation queue. It makes the checks of SubmitUpgrade before it
// records anything (the caller is an administrator, the pins name the
// installed package and the candidate the library resolves now, the same
// transition is not recorded already, no operation on the add-on is in
// flight), but it does not run the upgrade and does not refuse because
// another upgrade runs: the queue orders them, and the run checks the pins
// again. Repeating the same command under the same operation ID returns the
// recorded status.
func (m *Manager) AcceptUpgrade(ctx context.Context, actor contract.Actor, cmd LifecycleCommand) (OperationStatus, error) {
	if e := ctx.Err(); e != nil {
		return OperationStatus{}, e
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return OperationStatus{}, ErrForbidden
	}
	if cmd.Action != "upgrade" || cmd.ExpectedDigest == "" || cmd.CandidateDigest == "" || cmd.Approval != nil ||
		!addonID.MatchString(cmd.AddonID) || cmd.OperationID == "" || len(cmd.OperationID) > 256 {
		return OperationStatus{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return OperationStatus{}, ErrRecoveryRequired
	}
	if old, ok := m.journal.Operations[cmd.OperationID]; ok {
		if !old.Command.same(cmd) || !sameOperationOwner(old.Actor, actor) {
			return OperationStatus{}, ErrConflict
		}
		return old.Status, nil
	}
	if !m.journal.Addons[cmd.AddonID].Installed {
		return OperationStatus{}, ErrConflict
	}
	for _, op := range m.journal.Operations {
		if sameTransition(cmd, op.Command, op.Status) {
			return OperationStatus{}, ErrTransitionConflict
		}
		if op.Command.AddonID == cmd.AddonID && isActive(op.Status.Phase) {
			return OperationStatus{}, ErrConflict
		}
	}
	if _, e := m.checkPins(ctx, cmd, m.journal.Addons[cmd.AddonID]); e != nil {
		return OperationStatus{}, e
	}
	op := journalOperation{Command: cmd, Actor: actor, Async: true, StartedAt: time.Now().UTC().Format(time.RFC3339),
		Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: string(lifecycleAccepted)}}
	m.journal.Operations[cmd.OperationID] = op
	if e := m.persist(string(lifecycleAccepted)); e != nil {
		return OperationStatus{}, e
	}
	return op.Status, nil
}

// acceptedInstall returns the accepted install or upgrade of an operation ID
// that the actor owns. The caller holds mu.
func (m *Manager) acceptedInstall(actor contract.Actor, operationID string) (journalOperation, error) {
	op, ok := m.journal.Operations[operationID]
	if !ok || !op.Async || (op.Command.Action != "install" && op.Command.Action != "upgrade") || !sameOperationOwner(op.Actor, actor) {
		return journalOperation{}, ErrOperationNotFound
	}
	return op, nil
}

// RunAccepted runs an install that AcceptInstall recorded, or an upgrade that
// AcceptUpgrade recorded. It answers the
// recorded status for an operation that has already ended, so a retry of the
// call never installs twice. The context carries the cancellation of the
// queue and, through WithInstallProgress, its progress reports. When the
// install fails before the journal records an outcome, the operation is
// failed here, so it never stays accepted and blocks the add-on.
func (m *Manager) RunAccepted(ctx context.Context, actor contract.Actor, operationID string) (OperationStatus, error) {
	m.mu.Lock()
	if e := m.check(ctx); e != nil {
		m.mu.Unlock()
		return OperationStatus{}, e
	}
	op, e := m.acceptedInstall(actor, operationID)
	m.mu.Unlock()
	if e != nil {
		return OperationStatus{}, e
	}
	if op.Status.Phase != string(lifecycleAccepted) {
		if op.Status.Phase == string(lifecycleFailed) {
			return op.Status, ErrOperationFailed
		}
		return op.Status, nil
	}
	st, err := m.submit(context.WithValue(ctx, upgradeExecutionKey{}, m), op.Actor, op.Command, false)
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.journal.Operations[operationID]
	if err != nil && isActive(current.Status.Phase) && !m.uncertain {
		current.Status.Phase = string(lifecycleFailed)
		current.Status.FailureCode = "upgrade_failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			current.Status.FailureCode = "interrupted"
		}
		m.journal.Operations[operationID] = current
		if pe := m.persist(string(lifecycleFailed)); pe != nil {
			xlog.Warn("Could not record the end of a queued add-on install", "operationId", operationID, "error", pe)
		}
		st = current.Status
	}
	if err != nil {
		xlog.Warn("Queued add-on install ended", "operationId", operationID, "addon", op.Command.AddonID, "error", err)
	}
	return st, err
}

// DropAccepted fails an accepted install that has not started, with the
// failure code "cancelled", so the add-on is free again. An install that is
// running is stopped through its context, not here (ErrConflict). An install
// that has already ended returns its status.
func (m *Manager) DropAccepted(ctx context.Context, actor contract.Actor, operationID string) (OperationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	op, e := m.acceptedInstall(actor, operationID)
	if e != nil {
		return OperationStatus{}, e
	}
	switch op.Status.Phase {
	case string(lifecycleAccepted):
	case string(lifecycleCompleted), string(lifecycleFailed):
		return op.Status, nil
	default:
		return op.Status, ErrConflict
	}
	op.Status.Phase = string(lifecycleFailed)
	op.Status.FailureCode = FailureCancelled
	m.journal.Operations[operationID] = op
	if e := m.persist(string(lifecycleFailed)); e != nil {
		return op.Status, e
	}
	return op.Status, nil
}
