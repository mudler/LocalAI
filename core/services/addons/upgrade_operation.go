// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
)

type upgradeExecutionKey struct{}

type upgradeSnapshot struct {
	Command LifecycleCommand
	Actor   contract.Actor
	Status  OperationStatus
}

// SubmitUpgrade accepts one owner-managed upgrade. There is deliberately no
// queue: contention is a conflict, and replay only observes the original ID.
func (m *Manager) SubmitUpgrade(ctx context.Context, actor contract.Actor, cmd LifecycleCommand) (OperationStatus, error) {
	if err := ctx.Err(); err != nil {
		return OperationStatus{}, err
	}
	if m.admin == nil || m.admin(ctx, actor) != nil {
		return OperationStatus{}, ErrForbidden
	}
	if cmd.Action != "upgrade" || !addonID.MatchString(cmd.AddonID) || cmd.OperationID == "" || len(cmd.OperationID) > 256 {
		return OperationStatus{}, ErrConflict
	}
	m.upgradeMu.Lock()
	defer m.upgradeMu.Unlock()
	if m.upgradeClosing {
		return OperationStatus{}, ErrNotOwner
	}
	if old, ok := m.upgradeViews[cmd.OperationID]; ok {
		if old.Command != cmd || old.Actor != actor {
			return OperationStatus{}, ErrConflict
		}
		return old.Status, nil
	}
	// Identify the same pinned transition from immutable projections even
	// while execution owns mu; callers receive the same typed conflict then.
	for _, old := range m.upgradeViews {
		if cmd.ExpectedDigest != "" && old.Command.Action == "upgrade" && old.Command.AddonID == cmd.AddonID && old.Command.ExpectedDigest == cmd.ExpectedDigest && old.Command.CandidateDigest == cmd.CandidateDigest && old.Status.Phase != string(lifecycleFailed) {
			return OperationStatus{}, ErrTransitionConflict
		}
	}
	if m.upgradeActive || !m.mu.TryLock() {
		return OperationStatus{}, ErrConflict
	}
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return OperationStatus{}, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return OperationStatus{}, ErrRecoveryRequired
	}
	if old, ok := m.journal.Operations[cmd.OperationID]; ok {
		if old.Command != cmd || old.Actor != actor {
			return OperationStatus{}, ErrConflict
		}
		return old.Status, nil
	}
	if !m.journal.Addons[cmd.AddonID].Installed {
		return OperationStatus{}, ErrConflict
	}
	if (cmd.ExpectedDigest != "" || cmd.CandidateDigest != "") && (cmd.ExpectedDigest == "" || cmd.CandidateDigest == "") {
		return OperationStatus{}, ErrConflict
	}
	for _, op := range m.journal.Operations {
		if cmd.ExpectedDigest != "" && op.Command.Action == "upgrade" && op.Command.AddonID == cmd.AddonID && op.Command.ExpectedDigest == cmd.ExpectedDigest && op.Command.CandidateDigest == cmd.CandidateDigest && op.Status.Phase != string(lifecycleFailed) {
			return OperationStatus{}, ErrTransitionConflict
		}
		if op.Command.AddonID == cmd.AddonID && op.Status.Phase != string(lifecycleCompleted) && op.Status.Phase != string(lifecycleFailed) {
			return OperationStatus{}, ErrConflict
		}
	}
	// Preserve typed precondition refusals without accepting invalid work. The
	// worker rechecks the pins before effects, since acceptance is not a promise
	// that a mutable library candidate will stay unchanged.
	if cmd.ExpectedDigest != "" {
		if _, err := m.checkPins(ctx, cmd, m.journal.Addons[cmd.AddonID]); err != nil {
			return OperationStatus{}, err
		}
	}
	op := journalOperation{Command: cmd, Actor: actor, Async: true, Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: string(lifecycleAccepted)}}
	m.journal.Operations[cmd.OperationID] = op
	// persist normally refreshes snapshots. We already hold the projection lock.
	if e := m.persistJournal(string(lifecycleAccepted)); e != nil {
		return OperationStatus{}, e
	}
	m.upgradeViews[cmd.OperationID] = upgradeSnapshot{cmd, actor, op.Status}
	m.upgradeActive = true
	m.upgradeWG.Add(1)
	go func() {
		defer m.upgradeWG.Done()
		_, err := m.submit(context.WithValue(m.upgradeCtx, upgradeExecutionKey{}, m), actor, cmd, true)
		m.mu.Lock()
		current := m.journal.Operations[cmd.OperationID]
		if err != nil && current.Status.Phase != string(lifecycleCompleted) && current.Status.Phase != string(lifecycleFailed) && !m.uncertain {
			phase, _ := upgradeTransition(lifecycleRunning, upgradeFail)
			current.Status.Phase = string(phase)
			current.Status.FailureCode = "upgrade_failed"
			if errors.Is(err, context.Canceled) {
				current.Status.FailureCode = "interrupted"
			}
			if m.dataBlocked(cmd.AddonID) {
				current.Status.FailureCode = "recovery_required"
			}
			m.journal.Operations[cmd.OperationID] = current
			_ = m.persist(string(lifecycleFailed))
		}
		m.mu.Unlock()
		if err != nil {
			xlog.Warn("Owner upgrade ended", "operationId", cmd.OperationID, "error", err)
		}
		m.upgradeMu.Lock()
		m.upgradeActive = false
		m.upgradeMu.Unlock()
	}()
	return op.Status, nil
}

// refreshUpgradeViews is called only while mu is held. Values contain no
// mutable slices or pointers and may be returned without the execution lock.
func (m *Manager) refreshUpgradeViews() {
	m.upgradeMu.Lock()
	defer m.upgradeMu.Unlock()
	for id, op := range m.journal.Operations {
		if op.Async {
			status := op.Status
			if m.uncertain || m.runtimeRecoveryRequired {
				status.RuntimeState = string(lifecycleRecovery)
			}
			m.upgradeViews[id] = upgradeSnapshot{op.Command, op.Actor, status}
		}
	}
}

func (m *Manager) upgradeStage(id, stage string) error {
	op := m.journal.Operations[id]
	if !op.Async {
		return nil
	}
	op.Status.Stage = stage
	m.journal.Operations[id] = op
	return m.persist(stage)
}
