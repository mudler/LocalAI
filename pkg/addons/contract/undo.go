// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// Undo is the host's one way to roll back a change a chat made at once. An
// add-on that declares capabilities.undo keeps the change sets of its own
// writes in its own storage and answers two calls from the host's UI-only
// undo route. Neither call is a model tool: they are never in
// capabilities.tools, so no model, agent or typed dispatch can reach them.
//
//   - Read tool ToolChangesGet, with the argument ChangeArgument: what the
//     change set says now (ChangeInfo).
//   - Apply with the action UndoAction and tool ToolChangesUndo, with the
//     argument ChangeArgument: the rollback, as one more recorded change. The
//     operation ID makes the call idempotent. A rollback the add-on refuses
//     is a rejected receipt whose payload has {"error": code, "message": text}
//     with one of the Undo codes below.
const (
	// UndoAction is the envelope action of a rollback.
	UndoAction = "undo"
	// ToolChangesGet reads one change set.
	ToolChangesGet = "changes-get"
	// ToolChangesUndo rolls one change set back.
	ToolChangesUndo = "changes-undo"
	// ChangeArgument names the change set: the operation ID of the change.
	ChangeArgument = "change"
)

// Classes of a change set. The class says which host role the rollback needs:
// the role the change itself needed.
const (
	// ChangeClassWrite is a data change. Owners and editors undo it.
	ChangeClassWrite = "write"
	// ChangeClassSchema is a schema change or a creation. Only the owner role
	// undoes it.
	ChangeClassSchema = "schema"
)

// States of a change set, as ChangeInfo.State.
const (
	// ChangeAvailable can be undone now.
	ChangeAvailable = "available"
	// ChangeStale cannot be undone because the resource moved on.
	ChangeStale = "stale"
	// ChangeExpired is older than the retention of change sets.
	ChangeExpired = "expired"
	// ChangeUndone was undone already.
	ChangeUndone = "undone"
	// ChangeFinal cannot be undone and never could after this point: the work
	// it started has finished, for example.
	ChangeFinal = "cannot"
	// ChangeUnknown is the answer for a change the add-on never recorded.
	ChangeUnknown = "not_found"
)

// Codes of a refused rollback. They are the typed errors of the host route.
const (
	UndoStale         = "stale"
	UndoNotFound      = "not_found"
	UndoExpired       = "expired"
	UndoAlreadyUndone = "already_undone"
	// UndoCannot is a change that cannot be undone any more for a reason the
	// add-on states: the job it started has finished.
	UndoCannot = "cannot_undo"
)

// MaxUndoText bounds the sentences of a change set that reach a person and a
// model: the summary and the reason.
const MaxUndoText = 240

// ChangeInfo is the answer of ToolChangesGet.
type ChangeInfo struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	State string `json:"state"`
	// Summary says in one line what the rollback does.
	Summary string `json:"summary,omitempty"`
	// Detail says why the state is not available: what changed since, or why
	// the change cannot be undone.
	Detail string `json:"detail,omitempty"`
	// Current is the revision the resource has now. The rollback is applied
	// against it, so a change in between is refused as stale.
	Current Revision `json:"current"`
	// Deletes says that the rollback removes the resource itself: the change
	// was its creation and nothing touched it since. The host performs that
	// removal with its own delete, never the add-on.
	Deletes bool `json:"deletes,omitempty"`
}

// UndoNote is what the receipt of a change says about its rollback, under the
// key "undo" of the receipt payload. The host copies it to the tool result.
type UndoNote struct {
	// ID is the change ID to undo: the operation ID of the change.
	ID string `json:"id,omitempty"`
	// Available says that a change set was kept.
	Available bool `json:"available"`
	// Summary says in one line what the rollback does.
	Summary string `json:"summary,omitempty"`
	// Reason says why nothing was kept, for example that it was too large.
	Reason string `json:"reason,omitempty"`
}

var undoID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidChangeID reports whether id has the shape of an operation ID.
func ValidChangeID(id string) bool { return undoID.MatchString(id) }

// CleanUndoText makes text safe to show: printable characters only, one line,
// at most MaxUndoText runes. The text is data from the add-on, never an
// instruction.
func CleanUndoText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		if n >= MaxUndoText {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// ParseUndoNote reads the undo note of a receipt payload. It reports false
// when the payload has none.
func ParseUndoNote(payload json.RawMessage) (UndoNote, bool) {
	var p struct {
		Undo *UndoNote `json:"undo"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &p) != nil || p.Undo == nil {
		return UndoNote{}, false
	}
	n := *p.Undo
	n.Summary, n.Reason = CleanUndoText(n.Summary), CleanUndoText(n.Reason)
	if n.Available && !ValidChangeID(n.ID) {
		n.Available, n.ID = false, ""
		if n.Reason == "" {
			n.Reason = "no change set was kept"
		}
	}
	if !n.Available {
		n.ID = ""
	}
	return n, true
}
