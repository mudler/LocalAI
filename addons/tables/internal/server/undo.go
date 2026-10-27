// SPDX-License-Identifier: MIT
package server

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The two undo calls of the host (see contract.UndoAction). They are not in
// the manifest's tools, so no model, agent or typed dispatch can name them; the
// add-on still checks the action and the scope itself, so a caller that
// reaches the runtime transport with another action cannot undo anything.

func undoAuthority(env contract.Envelope, scope, action string) error {
	if env.Request.Action != action || env.Authority.Scope != scope || env.Authority.Actor.UserID == "" {
		return protocol.ErrForbidden
	}
	return nil
}

// readChange answers what one change set says now.
func (t *Tables) readChange(ctx context.Context, env contract.Envelope, a toolArgs) (contract.Result, error) {
	if err := undoAuthority(env, "read", readAction); err != nil {
		return contract.Result{}, err
	}
	if !contract.ValidChangeID(a.Change) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	info, err := t.Store.Change(ctx, env.Request.ResourceID, a.Change)
	if errors.Is(err, store.ErrChangeNotFound) {
		return contract.Result{Payload: payload(contract.ChangeInfo{ID: a.Change, State: contract.ChangeUnknown})}, nil
	}
	if err != nil {
		return contract.Result{}, mapErr(err)
	}
	return contract.Result{Payload: payload(info.ChangeInfo)}, nil
}

// applyUndo rolls one change set back. A refusal that is a fact about the
// change set is a rejected receipt with a typed code, so the owner can tell a
// person why; it records nothing and the operation ID stays free.
func (t *Tables) applyUndo(ctx context.Context, env contract.Envelope, a toolArgs, rev contract.Revision) (contract.Receipt, error) {
	if err := undoAuthority(env, "apply", contract.UndoAction); err != nil {
		return contract.Receipt{}, err
	}
	if !contract.ValidChangeID(a.Change) {
		return contract.Receipt{}, protocol.ErrBadRequest
	}
	op := env.Request.OperationID
	r, err := t.Store.UndoChange(ctx, env.Request.ResourceID, a.Change, op, rev)
	var stale *store.ChangeStaleError
	switch {
	case err == nil:
		return r, nil
	case errors.As(err, &stale):
		return reject(op, map[string]any{"error": contract.UndoStale, "message": stale.Detail, "revision": stale.Current}), nil
	case errors.Is(err, store.ErrChangeNotFound):
		return reject(op, map[string]any{"error": contract.UndoNotFound, "message": "no change set with this ID"}), nil
	case errors.Is(err, store.ErrChangeExpired):
		return reject(op, map[string]any{"error": contract.UndoExpired, "message": "the change set expired"}), nil
	case errors.Is(err, store.ErrChangeUndone):
		return reject(op, map[string]any{"error": contract.UndoAlreadyUndone, "message": "the change was undone already"}), nil
	case errors.Is(err, store.ErrConflict):
		return contract.Receipt{}, protocol.ErrConflict
	case errors.Is(err, store.ErrInvalidOperation):
		return contract.Receipt{}, protocol.ErrBadRequest
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrInvalidID), errors.Is(err, store.ErrUnsafePath):
		return contract.Receipt{}, mapErr(err)
	case errors.Is(err, store.ErrRowConstraint), errors.Is(err, store.ErrInvalidSchema), errors.Is(err, store.ErrCapacity):
		return reject(op, map[string]any{"error": "invalid", "message": err.Error()}), nil
	}
	return contract.Receipt{}, err
}
