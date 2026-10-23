// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Actions of the update management. The owner decides each of them: a caller
// who is not an administrator reads a redacted status and nothing else, and
// none of them is a tool.
const (
	// ActionUpdateStatus reads the update status of an add-on from the
	// owner's check, never from the network.
	ActionUpdateStatus = "update-status"
	// ActionUpdateCheck fetches the library again and returns the status. It
	// applies nothing.
	ActionUpdateCheck = "update-check"
	// ActionUpdateUpgrade is the explicit upgrade of an add-on to the
	// candidate the caller checked.
	ActionUpdateUpgrade = "update-upgrade"
)

// UpdateAction reports whether an action belongs to the update management.
func UpdateAction(a string) bool {
	return a == ActionUpdateStatus || a == ActionUpdateCheck || a == ActionUpdateUpgrade
}

// UpdateFailure is the typed refusal the owner puts in the result of an
// update action, so every frontend gives the same type and status for the same
// owner decision.
type UpdateFailure struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// UpdateEnvelope is the result payload of an update action: the data, or the
// typed failure.
type UpdateEnvelope struct {
	Error *UpdateFailure  `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

var updateFailures = []struct {
	err     error
	kind    string
	status  int
	message string
}{
	{addons.ErrStaleInstalled, "stale_installed", http.StatusConflict, "The installed version changed since the update was checked. Check again"},
	{addons.ErrCandidateChanged, "candidate_changed", http.StatusConflict, "The library no longer offers the version you checked. Check again"},
	{addons.ErrTransitionConflict, "upgrade_conflict", http.StatusConflict, "This upgrade is already recorded under another operation. Nothing was applied twice"},
	{addons.ErrIncompatible, "incompatible", http.StatusConflict, "The new version is not compatible with this host. Nothing was changed"},
	{addons.ErrIntegrity, "integrity_error", http.StatusConflict, "The library lists the installed version with different contents. Nothing was changed"},
	{addons.ErrOperationFailed, "upgrade_failed", http.StatusConflict, "The upgrade did not complete and the installed version is kept. Use a new operation to try again"},
	{addons.ErrConflict, "operation_conflict", http.StatusConflict, "The operation ID was used for another request, or the add-on is not in a state this change fits. Nothing was applied"},
	{addons.ErrCheckFailed, "check_failed", http.StatusServiceUnavailable, "The library could not be read, so the update was not started. Nothing was changed"},
}

func updateOutcome(e error) (*UpdateFailure, bool) {
	for _, f := range updateFailures {
		if errors.Is(e, f.err) {
			return &UpdateFailure{Type: f.kind, Message: f.message, Status: f.status}, true
		}
	}
	return nil, false
}

func (g *Gateway) updateAction(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if g.Manager == nil {
		return contract.Result{}, ErrUnavailable
	}
	id := request.AddonID
	var data any
	var err error
	switch request.Action {
	case ActionUpdateStatus:
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = g.Manager.UpdateStatus(ctx, actor, id)
	case ActionUpdateCheck:
		var args struct{}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		data, err = g.Manager.CheckUpdates(ctx, actor, id)
	case ActionUpdateUpgrade:
		var args struct {
			ExpectedDigest  string `json:"expectedDigest"`
			CandidateDigest string `json:"candidateDigest"`
		}
		if decode(bytes.NewReader(request.Arguments), &args) != nil {
			return contract.Result{}, ErrForbidden
		}
		cmd := addons.LifecycleCommand{OperationID: request.OperationID, AddonID: id, Action: "upgrade", Generation: request.Generation,
			ExpectedDigest: args.ExpectedDigest, CandidateDigest: args.CandidateDigest}
		if args.ExpectedDigest == "" || args.CandidateDigest == "" {
			err = addons.ErrConflict
			break
		}
		data, err = g.Manager.Submit(ctx, actor, cmd)
	default:
		return contract.Result{}, ErrForbidden
	}
	env := UpdateEnvelope{}
	if err != nil {
		f, typed := updateOutcome(err)
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
