// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Actions of the data recovery management. They are administrator-only on the
// owner, whatever a frontend checked, and none of them is a tool.
const (
	ActionRecoveryStatus       = "recovery-status"
	ActionRecoveryBackup       = "recovery-backup"
	ActionRecoveryVerify       = "recovery-verify"
	ActionRecoveryRestore      = "recovery-restore"
	ActionRecoveryDeleteBackup = "recovery-delete-backup"
	ActionRecoveryPrune        = "recovery-prune"
	ActionRecoveryPurge        = "recovery-purge"
)

// RecoveryAction reports whether an action belongs to the recovery management.
func RecoveryAction(a string) bool {
	switch a {
	case ActionRecoveryStatus, ActionRecoveryBackup, ActionRecoveryVerify, ActionRecoveryRestore, ActionRecoveryDeleteBackup, ActionRecoveryPrune, ActionRecoveryPurge:
		return true
	}
	return false
}

// RecoveryFailure is the typed refusal the owner puts in the result of a
// recovery action. It is part of the owner's answer, so every frontend
// reports the same decision with the same type and status.
type RecoveryFailure struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// RecoveryEnvelope is the result payload of a recovery action: the data, or
// the typed failure.
type RecoveryEnvelope struct {
	Error *RecoveryFailure `json:"error,omitempty"`
	// Data is the answer as the action defines it.
	Data json.RawMessage `json:"data,omitempty"`
}

var recoveryFailures = []struct {
	err     error
	kind    string
	status  int
	message string
}{
	{recovery.ErrNeedsRecovery, "needs_recovery", http.StatusConflict, "The data needs recovery: restore a verified backup or purge it first"},
	{recovery.ErrAddonRunning, "addon_running", http.StatusConflict, "Stop the add-on first"},
	{recovery.ErrBusy, "busy", http.StatusConflict, "Another recovery operation is running for this add-on"},
	{recovery.ErrOperationConflict, "operation_conflict", http.StatusConflict, "The operation ID was used for another request; nothing was done"},
	{recovery.ErrBackupNotFound, "backup_not_found", http.StatusNotFound, "No such backup"},
	{recovery.ErrNotInstalled, "not_installed", http.StatusNotFound, "The add-on is not installed"},
	{recovery.ErrBackupCorrupt, "backup_corrupt", http.StatusConflict, "The backup failed verification and was not used"},
	{recovery.ErrIncompatibleFormat, "incompatible_format", http.StatusConflict, "The data format does not fit the installed package"},
	{recovery.ErrIdentityChanged, "identity_changed", http.StatusConflict, "The package comes from another trusted source than the data"},
	{recovery.ErrMappingRequired, "mapping_required", http.StatusConflict, "The backup was made in another ownership mode: name the account that takes the data"},
	{recovery.ErrMappingInvalid, "mapping_invalid", http.StatusBadRequest, "The ownership mapping is not valid"},
	{recovery.ErrDowngradeUnsupported, "downgrade_unsupported", http.StatusConflict, "Named-user data cannot be restored in local-owner mode"},
	{recovery.ErrSoleRecoveryCopy, "sole_recovery_copy", http.StatusConflict, "This is the only verified recovery copy and is kept"},
	{recovery.ErrConfirmation, "confirmation_mismatch", http.StatusBadRequest, "The typed confirmation does not match"},
	{recovery.ErrInvalid, "invalid_request", http.StatusBadRequest, "The request cannot be carried out as given"},
	{recovery.ErrInsufficientStorage, "insufficient_storage", http.StatusServiceUnavailable, "Not enough free storage; nothing was changed"},
}

// recoveryOutcome turns a recovery error that has a typed answer into the
// failure of the envelope. Any other error is returned unchanged, and then
// the usual rules apply: a denial is 403 and the rest is unavailable.
func recoveryOutcome(e error) (*RecoveryFailure, bool) {
	for _, f := range recoveryFailures {
		if errors.Is(e, f.err) {
			return &RecoveryFailure{Type: f.kind, Message: f.message, Status: f.status}, true
		}
	}
	return nil, false
}

func (g *Gateway) recoveryAction(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if g.Manager == nil {
		return contract.Result{}, ErrUnavailable
	}
	rec, err := g.Manager.Recovery(ctx, actor)
	if err != nil {
		return contract.Result{}, err
	}
	id := request.AddonID
	var data any
	switch request.Action {
	case ActionRecoveryStatus:
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = rec.Status(ctx, id)
	case ActionRecoveryBackup:
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = rec.BackupOp(ctx, id, request.OperationID)
	case ActionRecoveryVerify:
		var args struct {
			BackupID string `json:"backupId"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = rec.Verify(ctx, id, args.BackupID)
	case ActionRecoveryRestore:
		var args struct {
			BackupID string            `json:"backupId"`
			Mapping  map[string]string `json:"mapping"`
			Confirm  string            `json:"confirm"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		if args.Confirm != id {
			err = recovery.ErrConfirmation
			break
		}
		data, err = rec.RestoreOp(ctx, actor, request.OperationID, id, args.BackupID, recovery.OwnershipMapping(args.Mapping))
	case ActionRecoveryDeleteBackup:
		var args struct {
			BackupID string `json:"backupId"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		err = rec.DeleteBackup(ctx, actor, id, args.BackupID)
		if err == nil {
			data = struct {
				Deleted string `json:"deleted"`
			}{args.BackupID}
		}
	case ActionRecoveryPrune:
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		var removed []string
		removed, err = rec.Prune(ctx, actor, id)
		data = struct {
			Removed []string `json:"removed"`
		}{removed}
	case ActionRecoveryPurge:
		var args struct {
			Scope   recovery.PurgeScope `json:"scope"`
			Confirm string              `json:"confirm"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = rec.PurgeOp(ctx, actor, request.OperationID, id, args.Scope, args.Confirm)
	default:
		return contract.Result{}, ErrForbidden
	}
	env := RecoveryEnvelope{}
	if err != nil {
		f, typed := recoveryOutcome(err)
		if !typed {
			return contract.Result{}, err
		}
		env.Error = f
	} else {
		raw, merr := json.Marshal(data)
		if merr != nil {
			return contract.Result{}, ErrUnavailable
		}
		env.Data = raw
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return contract.Result{}, ErrUnavailable
	}
	return contract.Result{Payload: payload}, nil
}
