// SPDX-License-Identifier: MIT
package hostgrants

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// Reasons for a reconcile removal.
const (
	RemovedNotInstalled        = "not_installed"
	RemovedIdentityMismatch    = "identity_mismatch"
	RemovedOperationIncomplete = "operation_incomplete"
)

// ReconcileInput is what startup knows about the lifecycle state.
type ReconcileInput struct {
	// Installed returns the trusted identity of an installed add-on.
	Installed func(addonID string) (identity string, ok bool)
	// OperationCompleted reports whether a lifecycle operation completed in
	// the journal. Nil treats every operation as completed.
	OperationCompleted func(operationID string) bool
}

// Removal describes one entry that reconciliation deletes.
type Removal struct {
	AddonID string
	Reason  string
	Ops     []string
}

// PlanReconcile lists the entries to delete: add-ons that are not installed,
// entries whose identity differs from the installed package, and pending
// entries whose lifecycle operation never completed (a crash between the
// grant write and the journal commit). Deny rules, blanket state and hints are
// untouched. The result is sorted by add-on ID.
func PlanReconcile(snap Snapshot, in ReconcileInput) (Patch, []Removal) {
	var removals []Removal
	patch := Patch{Addons: map[string]*AddonGrants{}}
	for id, entry := range snap.Grants {
		reason := ""
		identity, installed := "", false
		if in.Installed != nil {
			identity, installed = in.Installed(id)
		}
		switch {
		case !installed:
			reason = RemovedNotInstalled
		case identity != entry.Identity:
			reason = RemovedIdentityMismatch
		}
		if reason == "" && in.OperationCompleted != nil {
			// An operation added by an upgrade is pending on its own; the
			// operations the entry held before keep working.
			var kept []OpGrant
			var dropped []string
			for _, op := range entry.Ops {
				if in.OperationCompleted(EffectiveOperationID(entry, op)) {
					kept = append(kept, op)
				} else {
					dropped = append(dropped, op.Name)
				}
			}
			switch {
			case len(dropped) == 0:
			case len(kept) == 0:
				reason = RemovedOperationIncomplete
			default:
				trimmed := entry
				trimmed.Ops = kept
				patch.Addons[id] = &trimmed
				removals = append(removals, Removal{AddonID: id, Reason: RemovedOperationIncomplete, Ops: dropped})
				continue
			}
		}
		if reason == "" {
			continue
		}
		ops := make([]string, 0, len(entry.Ops))
		for _, op := range entry.Ops {
			ops = append(ops, op.Name)
		}
		removals = append(removals, Removal{AddonID: id, Reason: reason, Ops: ops})
		patch.Addons[id] = nil
	}
	sort.Slice(removals, func(i, j int) bool { return removals[i].AddonID < removals[j].AddonID })
	return patch, removals
}

// Reconcile deletes stale entries in one Update and returns what it removed
// so the caller can write a reconcile-removed audit entry for each. It retries
// a revision conflict a few times and does nothing when there is nothing to
// remove. The operation ID is derived from the revision, so a repeated call
// after a lost response is a replay.
func (s *Store) Reconcile(ctx context.Context, in ReconcileInput) ([]Removal, error) {
	for range 3 {
		snap, err := s.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		patch, removals := PlanReconcile(snap, in)
		if len(removals) == 0 {
			return nil, nil
		}
		_, err = s.Update(ctx, patch, snap.Revision, fmt.Sprintf("reconcile-r%d", snap.Revision))
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return slices.Clone(removals), nil
	}
	return nil, ErrConflict
}
