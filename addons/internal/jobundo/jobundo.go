// SPDX-License-Identifier: MIT

// Package jobundo is what the job add-ons (fine-tuning and quantization) share
// of the undo of a change a chat made at once. The only change of theirs that
// can be rolled back is the start of a job: undoing it stops the job while it
// still runs. A job that finished cannot be un-run, and the engine has no
// operation that removes its output, so the answer is then that it cannot be
// undone. A stop, an export, an import and a dataset have no undo.
package jobundo

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Retention is how long the start of a job can be undone.
const Retention = 7 * 24 * time.Hour

// Reasons a change keeps no change set, in the receipts of the add-ons.
const (
	ReasonStop    = "cannot be undone: a stopped job cannot be started again; start a new one"
	ReasonExport  = "cannot be undone: the exported model stays; remove it from the models page"
	ReasonImport  = "cannot be undone: the imported model stays; remove it from the models page"
	ReasonDataset = "cannot be undone: the stored dataset stays; the host offers no way to remove it from a tool"
)

// Merge adds the undo note to a receipt payload. A payload that is not a JSON
// object is kept under "result".
func Merge(payload json.RawMessage, n contract.UndoNote) json.RawMessage {
	m := map[string]any{}
	if len(payload) > 0 && json.Unmarshal(payload, &m) != nil {
		m = map[string]any{"result": payload}
	}
	m["undo"] = n
	b, _ := json.Marshal(m)
	return b
}

// Finished reports an engine job status that is last.
func Finished(status string) bool {
	return status == "completed" || status == "failed" || status == "stopped"
}

// StatusOf reads the status of an engine job answer.
func StatusOf(raw json.RawMessage) string {
	var v struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Status
}

// Authority is the add-on's own check of an undo call: the action and the
// scope go together, and the host named an actor. A caller that reaches the
// transport with another action cannot undo anything.
func Authority(env contract.Envelope, scope, action string) error {
	if env.Request.Action != action || env.Authority.Scope != scope || env.Authority.Actor.UserID == "" {
		return protocol.ErrForbidden
	}
	return nil
}

// Reject is the rejected receipt of an undo the add-on refuses, with a typed
// code from the contract.
func Reject(op, code, message string) contract.Receipt {
	b, _ := json.Marshal(map[string]any{"error": code, "message": message})
	return contract.Receipt{OperationID: op, State: contract.ReceiptRejected, Result: contract.Result{Payload: b}}
}

// Change is the state of the start of one job, as the add-on's journal knows
// it. The engine's own status is read separately.
type Change struct {
	// Known says the journal has this change set: the start of this job.
	Known    bool
	Undone   bool
	Started  time.Time
	Revision uint64
}

// Classify says what the change set is now, without the engine: the answer is
// final unless it is available, and then the engine status decides.
func Classify(c Change, now time.Time) (state, detail string) {
	switch {
	case !c.Known:
		return contract.ChangeUnknown, ""
	case c.Undone:
		return contract.ChangeUndone, ""
	case !c.Started.IsZero() && c.Started.Add(Retention).Before(now):
		return contract.ChangeExpired, ""
	case c.Revision != 1:
		return contract.ChangeFinal, "a later change was made to the job"
	}
	return contract.ChangeAvailable, ""
}

// CodeOf is the rejection code of a state that is not available.
func CodeOf(state string) string {
	switch state {
	case contract.ChangeUndone:
		return contract.UndoAlreadyUndone
	case contract.ChangeExpired:
		return contract.UndoExpired
	case contract.ChangeFinal, contract.ChangeStale:
		if state == contract.ChangeStale {
			return contract.UndoStale
		}
		return contract.UndoCannot
	}
	return contract.UndoNotFound
}
