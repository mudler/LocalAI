// SPDX-License-Identifier: MIT
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Undo rolls back one change a chat made at once, with the change set the
// add-on kept for it (see contract.UndoAction). It is the person's own UI
// action and never a tool: it runs under the rules of every change, and
// the role it needs is the role the change needed. The add-on says which: an
// editor undoes a data change, only the owner role undoes a schema change or a
// creation. The undo is applied against the revision the add-on has just
// reported, so a change in between is a stale refusal and nothing is
// overwritten. The operation ID makes it idempotent.
func (s *Service) Undo(ctx context.Context, actor contract.Actor, resourceID, changeID string, r gateway.UndoRequest) (gateway.UndoResult, error) {
	if !validOperation(r.OperationID) || r.Generation == 0 || !contract.ValidChangeID(changeID) {
		return gateway.UndoResult{}, gateway.ErrInvalidRequest
	}
	if _, known := s.keyOf(resourceID); !known && s.o.Policy != nil {
		// A removal that finished after its answer was lost is the same
		// success for the owner who asked, under the same operation.
		if done, err := s.o.Policy.DeletedBy(ctx, actor, resourceID, r.OperationID); err == nil && done {
			return gateway.UndoResult{ResourceID: resourceID, ChangeID: changeID, OperationID: r.OperationID, Deleted: true}, nil
		}
	}
	key, err := s.authorized(ctx, actor, resourceID, policy.ActionWrite)
	if err != nil {
		return gateway.UndoResult{}, err
	}
	inst, caps, err := s.session(ctx, key.AddonID, r.Generation)
	if err != nil {
		return gateway.UndoResult{}, err
	}
	if !caps.Undo {
		return gateway.UndoResult{}, &gateway.UndoRefused{Code: contract.UndoCannot, Detail: "this add-on keeps no change sets"}
	}
	wire := uiOperation(actor, "undo", r.OperationID)
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: contract.ToolChangesUndo, contract.ChangeArgument: changeID})
	want := binding{tool: contract.ToolChangesUndo, hash: requestHash(key.ResourceID, contract.ToolChangesUndo, args)}
	if have, ok := s.bound(wire); ok && have.hash != want.hash {
		return gateway.UndoResult{}, gateway.ErrOperationConflict
	}
	info, err := s.readChange(ctx, actor, key, inst, changeID)
	if err != nil {
		return gateway.UndoResult{}, err
	}
	// The role of the change. The first check above admitted the lowest role.
	action := policy.ActionWrite
	if info.Class == contract.ChangeClassSchema {
		action = policy.ActionSchema
	}
	if info.State != contract.ChangeUnknown {
		if _, err = s.authorized(ctx, actor, resourceID, action); err != nil {
			return gateway.UndoResult{}, err
		}
	}
	replay := false
	if info.State == contract.ChangeUndone {
		// The undo that was asked for may be the one that made it so.
		if rc, e := inst.Client.Receipt(ctx, wire); e == nil && rc.State == contract.ReceiptCommitted {
			replay = true
		}
	}
	if !replay {
		if e := refusalOf(info); e != nil {
			return gateway.UndoResult{}, e
		}
	}
	if info.Deletes {
		return s.undoCreation(ctx, actor, key, inst, caps, r, changeID, info)
	}
	var out gateway.UndoResult
	err = s.o.Policy.Admit(ctx, actor, key, action, func(rctx context.Context) error {
		if s.o.Broker != nil {
			if e := s.o.Broker.BlockedError(actor, key); e != nil {
				return e
			}
		}
		var e error
		out, e = s.applyUndo(rctx, actor, key, inst, string(action), wire, args, info, r.OperationID, want)
		return e
	})
	if err != nil {
		return gateway.UndoResult{}, classify(err)
	}
	out.ResourceID, out.ChangeID, out.OperationID = key.ResourceID, changeID, r.OperationID
	return out, nil
}

// refusalOf turns a state of the change set that is not available into the
// typed refusal.
func refusalOf(info contract.ChangeInfo) error {
	code := ""
	switch info.State {
	case contract.ChangeAvailable:
		return nil
	case contract.ChangeUnknown:
		code = contract.UndoNotFound
	case contract.ChangeUndone:
		code = contract.UndoAlreadyUndone
	case contract.ChangeExpired:
		code = contract.UndoExpired
	case contract.ChangeStale:
		code = contract.UndoStale
	case contract.ChangeFinal:
		code = contract.UndoCannot
	default:
		return gateway.ErrInvalidRequest
	}
	return &gateway.UndoRefused{Code: code, Detail: contract.CleanUndoText(info.Detail)}
}

// readChange asks the add-on what the change set says now, admitted for the
// actor like any read. The answer is data from the add-on: it is bounded and
// cleaned, and decides nothing the host does not check again.
func (s *Service) readChange(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, changeID string) (contract.ChangeInfo, error) {
	var out contract.ChangeInfo
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: contract.ToolChangesGet, contract.ChangeArgument: changeID})
	err := s.o.Policy.Admit(ctx, actor, key, policy.ActionRead, func(rctx context.Context) error {
		cctx, cancel := context.WithTimeout(rctx, s.o.Limits.ReadTimeout)
		defer cancel()
		env := contract.Envelope{
			Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: randomID("n"), Generation: inst.Generation, Action: "read", Arguments: args},
			Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: s.o.Now().Add(s.o.Limits.ReadTimeout)},
		}
		res, e := inst.Client.Read(cctx, env)
		if e != nil {
			if rctx.Err() != nil {
				return rctx.Err()
			}
			if errors.Is(e, contract.ErrRefused) {
				return gateway.Refused(e)
			}
			return unavailable(e)
		}
		if json.Unmarshal(res.Payload, &out) != nil {
			return unavailable(errors.New("change set answer is not an object"))
		}
		return nil
	})
	if err != nil {
		return out, classify(err)
	}
	out.Detail, out.Summary = contract.CleanUndoText(out.Detail), contract.CleanUndoText(out.Summary)
	if out.Class != contract.ChangeClassWrite && out.Class != contract.ChangeClassSchema {
		if out.State != contract.ChangeUnknown {
			return out, gateway.ErrInvalidRequest
		}
	}
	return out, nil
}

// applyUndo applies the rollback against the revision the add-on reported.
func (s *Service) applyUndo(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, _, wire string, args json.RawMessage, info contract.ChangeInfo, original string, want binding) (gateway.UndoResult, error) {
	cur, err := s.o.Runtime.Runtime(ctx, key.AddonID)
	if err != nil || cur.Client == nil {
		return gateway.UndoResult{}, unavailable(err)
	}
	if cur.Generation != inst.Generation {
		return gateway.UndoResult{}, gateway.ErrStale
	}
	if !s.bind(wire, want) {
		return gateway.UndoResult{}, gateway.ErrOperationConflict
	}
	env := contract.Envelope{
		Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: wire, Generation: cur.Generation, Action: contract.UndoAction, Arguments: args},
		Authority: contract.Authority{Actor: actor, Scope: "apply", ExpiresAt: s.o.Now().Add(s.o.Limits.ApplyTimeout + time.Minute)},
	}
	receipt, err := cur.Client.Apply(ctx, env, info.Current)
	if err != nil {
		if errors.Is(err, addonruntime.ErrConflict) {
			return gateway.UndoResult{}, gateway.ErrOperationConflict
		}
		if errors.Is(err, contract.ErrRefused) {
			s.unbind(wire, want)
			return gateway.UndoResult{}, gateway.Refused(err)
		}
		return gateway.UndoResult{}, &gateway.OutcomeUnknown{OperationID: original}
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		return gateway.UndoResult{Revision: receipt.Revision, Summary: info.Summary}, nil
	case contract.ReceiptRejected:
		s.unbind(wire, want)
		var why struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(receipt.Result.Payload, &why)
		if gateway.ValidUndoCode(why.Error) {
			return gateway.UndoResult{}, &gateway.UndoRefused{Code: why.Error, Detail: contract.CleanUndoText(why.Message)}
		}
		return gateway.UndoResult{}, gateway.ErrInvalidRequest
	}
	return gateway.UndoResult{}, &gateway.OutcomeUnknown{OperationID: original}
}

// undoCreation undoes the creation of a resource: the host deletes it, with its
// own deletion, which the owner role alone can start. The add-on has said the
// resource is untouched since it was created, and the deletion checks the
// revision again.
func (s *Service) undoCreation(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, caps contract.Capabilities, r gateway.UndoRequest, changeID string, info contract.ChangeInfo) (gateway.UndoResult, error) {
	if caps.Resources == nil || caps.Resources.Inspect == "" {
		return gateway.UndoResult{}, gateway.ErrInvalidRequest
	}
	seen, err := s.inspect(ctx, actor, key, inst, caps.Resources.Inspect)
	if err != nil {
		return gateway.UndoResult{}, classify(err)
	}
	res, err := s.Delete(ctx, actor, key.ResourceID, gateway.DeleteResourceRequest{OperationID: r.OperationID, Generation: r.Generation, Revision: info.Current, Confirm: seen.Name})
	if err != nil {
		if errors.Is(err, gateway.ErrStale) {
			return gateway.UndoResult{}, &gateway.UndoRefused{Code: contract.UndoStale, Detail: "the database changed since it was created"}
		}
		return gateway.UndoResult{}, err
	}
	return gateway.UndoResult{ResourceID: key.ResourceID, ChangeID: changeID, OperationID: r.OperationID, Deleted: res.Deleted, Summary: info.Summary}, nil
}
