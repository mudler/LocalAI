// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// UndoRequest is the body of the undo of one change a chat made at once.
type UndoRequest struct {
	// OperationID names the undo. Repeating it with the same change returns
	// the same result and undoes once; after outcome_unknown it is what to
	// repeat.
	OperationID string `json:"operationId" example:"undo-1"`
	// Generation is the installed generation the caller listed.
	Generation uint64 `json:"generation" example:"3"`
}

// UndoResult is the answer of a committed undo.
type UndoResult struct {
	ResourceID  string `json:"resourceId"`
	ChangeID    string `json:"changeId"`
	OperationID string `json:"operationId"`
	// Revision is the revision after the undo.
	Revision contract.Revision `json:"revision"`
	// Summary says in one line what the undo did.
	Summary string `json:"summary,omitempty"`
	// Deleted is true when the undo removed the resource itself: the change
	// was its creation.
	Deleted bool `json:"deleted,omitempty"`
}

// UndoRefused is the typed refusal of an undo: the change set is a fact the
// person can be told about. Nothing was changed.
type UndoRefused struct {
	// Code is one of the contract's Undo codes: stale, not_found, expired,
	// already_undone or cannot_undo.
	Code string
	// Detail says what changed since (stale) or why the change cannot be
	// undone. It is bounded plain text from the add-on.
	Detail string
}

func (e *UndoRefused) Error() string { return "addon undo refused: " + e.Code }

// Unwrap makes a stale refusal match ErrStale, so a caller that only knows the
// view moved still reads it right.
func (e *UndoRefused) Unwrap() error {
	if e.Code == contract.UndoStale {
		return ErrStale
	}
	return ErrForbidden
}

// ValidUndoCode says whether code is one the host knows.
func ValidUndoCode(code string) bool {
	switch code {
	case contract.UndoStale, contract.UndoNotFound, contract.UndoExpired, contract.UndoAlreadyUndone, contract.UndoCannot:
		return true
	}
	return false
}

// UndoResource rolls back one change a chat made at once, as the person's
// explicit UI action. It is not a tool: no model, agent or API key reaches it.
func (g *Gateway) UndoResource(ctx context.Context, a contract.Actor, resourceID, changeID string, r UndoRequest) (UndoResult, error) {
	return decodeResource[UndoResult](g.resourceCall(ctx, a, r.OperationID, ResourceCall{Op: resourceUndo, ResourceID: resourceID, ChangeID: changeID, Generation: r.Generation}))
}

// asUndoRefused finds an UndoRefused in err.
func asUndoRefused(err error) (*UndoRefused, bool) {
	var u *UndoRefused
	ok := errors.As(err, &u)
	return u, ok
}

func isUndoRefused(err error) bool { _, ok := asUndoRefused(err); return ok }
