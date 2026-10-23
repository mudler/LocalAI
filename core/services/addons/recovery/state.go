// SPDX-License-Identifier: MIT
package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Recovery states of an add-on's data.
const (
	StateReady         = "ready"
	StateNeedsRecovery = "needs-recovery"
	StateUpgrading     = "upgrading"
	StateRestoring     = "restoring"
	StatePurging       = "purging"
)

// State is the durable recovery state of one add-on. It is the only place
// that says the data cannot be trusted: an add-on in needs-recovery is never
// started, and the previous package alone does not make it startable.
type State struct {
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Since  time.Time `json:"since,omitempty"`
	// RecoveryBackup is the verified copy that a failed upgrade or restore
	// leaves for an explicit restore.
	RecoveryBackup string `json:"recoveryBackup,omitempty"`
	// PreviousGeneration is the generation that wrote the data.
	PreviousGeneration uint64 `json:"previousGeneration,omitempty"`
	// Pinned is the backup that retention keeps until a newer verified one
	// exists.
	Pinned string `json:"pinned,omitempty"`
	// LastRestore is the receipt of the last restore, with its warnings.
	LastRestore *RestoreReceipt `json:"lastRestore,omitempty"`
	// Ops are the last operations an administrator started, by operation
	// ID. A repeated ID with the same request returns the recorded result
	// and never runs again.
	Ops []OpRecord `json:"ops,omitempty"`
}

// OpRecord is the result of one administrator operation.
type OpRecord struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Params string          `json:"params"`
	Result json.RawMessage `json:"result"`
}

// maxOps bounds the operations a state remembers.
const maxOps = 16

func (s *State) record(op OpRecord) {
	if op.ID == "" {
		return
	}
	for i := range s.Ops {
		if s.Ops[i].ID == op.ID {
			s.Ops[i] = op
			return
		}
	}
	s.Ops = append(s.Ops, op)
	if len(s.Ops) > maxOps {
		s.Ops = append([]OpRecord(nil), s.Ops[len(s.Ops)-maxOps:]...)
	}
}

// replayed returns the recorded result of an operation ID. A repeat that
// names another kind or other parameters is a conflict, not a replay.
func (r *Recovery) replayed(addonID, opID, kind, params string) (json.RawMessage, bool, error) {
	if opID == "" {
		return nil, false, nil
	}
	st, err := r.loadState(addonID)
	if err != nil {
		return nil, false, err
	}
	for _, o := range st.Ops {
		if o.ID != opID {
			continue
		}
		if o.Kind != kind || o.Params != params {
			return nil, false, ErrOperationConflict
		}
		return o.Result, true, nil
	}
	return nil, false, nil
}

// remember records the result of an operation.
func (r *Recovery) remember(addonID, opID, kind, params string, result any) {
	if opID == "" {
		return
	}
	b, err := json.Marshal(result)
	if err != nil {
		return
	}
	st, err := r.loadState(addonID)
	if err != nil {
		return
	}
	st.record(OpRecord{ID: opID, Kind: kind, Params: params, Result: b})
	_ = r.saveState(addonID, st)
}

func (r *Recovery) loadState(addonID string) (State, error) {
	b, err := os.ReadFile(filepath.Join(r.stateDir(addonID), "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return State{State: StateReady}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err = json.Unmarshal(b, &s); err != nil || s.State == "" {
		// An unreadable state is never read as ready.
		return State{State: StateNeedsRecovery, Reason: "the recovery state of the add-on is unreadable"}, nil
	}
	return s, nil
}

func (r *Recovery) saveState(addonID string, s State) error {
	if s.State == StateReady && s.Pinned == "" && s.LastRestore == nil && len(s.Ops) == 0 {
		// Nothing to remember: the absence of the file is ready.
		err := os.Remove(filepath.Join(r.stateDir(addonID), "state.json"))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return replaceJSON(filepath.Join(r.stateDir(addonID), "state.json"), s)
}

// Blocked reports whether the add-on must not be started because its data is
// being changed or cannot be trusted.
func (r *Recovery) Blocked(addonID string) bool {
	if validID(addonID) != nil {
		return false
	}
	s, err := r.loadState(addonID)
	if err != nil {
		return true
	}
	return s.State != StateReady
}

// Status is the recovery view of one add-on for the management page.
type Status struct {
	AddonID string `json:"addonId"`
	// Mode is the ownership mode of the owner: named-user or local-owner.
	Mode   string `json:"mode"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Phase is the phase of an upgrade or restore in progress.
	Phase   string       `json:"phase,omitempty"`
	Backups []BackupInfo `json:"backups"`
	// RecoveryBackup names the copy to restore from.
	RecoveryBackup string          `json:"recoveryBackup,omitempty"`
	LastRestore    *RestoreReceipt `json:"lastRestore,omitempty"`
	// Retention is the policy applied to the backups.
	Retention RetentionView `json:"retention"`
	// PreviousCopies counts the data copies a restore set aside.
	PreviousCopies int `json:"previousCopies"`
	// Operations lists the operation IDs whose result is recorded, so a
	// caller that lost an answer can see that its request ran.
	Operations []string `json:"operations,omitempty"`
}

// RetentionView is the retention policy as shown.
type RetentionView struct {
	Keep          int   `json:"keep"`
	MaxAgeSeconds int64 `json:"maxAgeSeconds,omitempty"`
}

// Status reads the recovery state and the backup list. It changes nothing.
func (r *Recovery) Status(ctx context.Context, addonID string) (Status, error) {
	if err := validID(addonID); err != nil {
		return Status{}, err
	}
	st, err := r.loadState(addonID)
	if err != nil {
		return Status{}, err
	}
	list, err := r.List(ctx, addonID)
	if err != nil {
		return Status{}, err
	}
	out := Status{AddonID: addonID, Mode: r.mode(), State: st.State, Reason: st.Reason, Backups: list, RecoveryBackup: st.RecoveryBackup,
		LastRestore: st.LastRestore, Retention: RetentionView{Keep: r.cfg.Retention.Keep, MaxAgeSeconds: int64(r.cfg.Retention.MaxAge / time.Second)}}
	if j, ok := r.loadJournal(addonID); ok {
		out.Phase = j.Kind + ":" + j.Phase
	}
	out.PreviousCopies = len(r.previousCopies(addonID))
	for _, o := range st.Ops {
		out.Operations = append(out.Operations, o.ID)
	}
	return out, nil
}

func (r *Recovery) previousCopies(addonID string) []string {
	entries, _ := os.ReadDir(r.stateDir(addonID))
	var out []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > len("previous-") && e.Name()[:len("previous-")] == "previous-" {
			out = append(out, e.Name())
		}
	}
	return out
}

// journalRecord is the durable record of one operation that changes data in
// place. It is written before each phase starts, so a restart knows how far
// the operation got and finishes or rolls it back by a fixed rule.
type journalRecord struct {
	Kind          string            `json:"kind"`  // upgrade, restore, purge
	Phase         string            `json:"phase"` // kind specific
	ID            string            `json:"id"`    // operation ID
	Backup        string            `json:"backup,omitempty"`
	Staged        string            `json:"staged,omitempty"`
	Prev          string            `json:"previous,omitempty"`
	Digest        string            `json:"digest,omitempty"` // candidate package digest
	Number        uint64            `json:"number,omitempty"` // candidate generation
	FormatChanged bool              `json:"formatChanged,omitempty"`
	Mapping       map[string]string `json:"mapping,omitempty"`
	Actor         string            `json:"actor,omitempty"`
	Scope         *PurgeScope       `json:"scope,omitempty"`
	Started       time.Time         `json:"started"`
	// Warnings carries the restore receipt body until the restore finishes.
	Receipt *RestoreReceipt `json:"receipt,omitempty"`
	// Prior is the state before a restore began, put back if it is undone.
	Prior *State `json:"prior,omitempty"`
	// Op is the operation ID of the administrator's request.
	Op string `json:"op,omitempty"`
}

func (r *Recovery) journalPath(addonID string) string {
	return filepath.Join(r.stateDir(addonID), "journal.json")
}

func (r *Recovery) loadJournal(addonID string) (journalRecord, bool) {
	b, err := os.ReadFile(r.journalPath(addonID))
	if err != nil {
		return journalRecord{}, false
	}
	var j journalRecord
	if json.Unmarshal(b, &j) != nil || j.Kind == "" {
		return journalRecord{Kind: "unreadable", Phase: "unreadable"}, true
	}
	return j, true
}

func (r *Recovery) saveJournal(addonID string, j journalRecord) error {
	return replaceJSON(r.journalPath(addonID), j)
}

func (r *Recovery) clearJournal(addonID string) error {
	err := os.Remove(r.journalPath(addonID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncPath(r.stateDir(addonID))
}

// StateOf is the recovery state name of the add-on's data. A state that
// cannot be read is needs-recovery, never ready.
func (r *Recovery) StateOf(addonID string) string {
	if validID(addonID) != nil {
		return StateReady
	}
	s, err := r.loadState(addonID)
	if err != nil {
		return StateNeedsRecovery
	}
	return s.State
}

func (r *Recovery) mode() string {
	if r.cfg.Policy == nil {
		return ""
	}
	return r.cfg.Policy.Mode()
}
