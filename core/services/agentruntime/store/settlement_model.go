// SPDX-License-Identifier: MIT
package store

import (
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"sort"
)

// This private decision table models one fenced transaction, NOT a capability,
// transaction implementation, runner permission or proof of database isolation.
// In particular a returned Next is data, not permission to change runner RR227.
// Trusted time and the observed outer transaction outcome are explicit inputs.
// FS001 malformed request; FS002 malformed snapshot; FS010 missing key;
// FS012 non-live authority; FS013 owner; FS014 epoch; FS015 persisted expiry;
// FS016 tombstone; FS017 revision; FS018 active turn; FS019 exhaustion;
// FS090 mutation/outer commit failure; FS101 committed Complete; FS102 Hold.
const fsMax = uint64(math.MaxInt64)

type fsMode uint8

const (
	fsComplete fsMode = iota + 1
	fsHold
)

type fsAuthority uint8

const (
	fsLive fsAuthority = iota + 1
	fsExpired
	fsReleased
	fsStolen
)

type fsTransaction uint8

const (
	fsCommit fsTransaction = iota + 1
	fsRollback
	fsMutationFailed
)

type fsKey struct{ Principal, Conversation string }
type fsRow struct {
	Key            fsKey
	Owner          string
	Epoch, Expires uint64
	Authority      fsAuthority
	Revision       uint64
	Deleted        bool
	Active         *Input
	Pending        []Input
	Settled        []string
	Checkpoint     Checkpoint
	Events         []Event
}
type fsRequest struct {
	Key                          fsKey
	Owner                        string
	Epoch, Now, ExpectedRevision uint64
	TurnID                       string
	Mode                         fsMode
	Transaction                  fsTransaction
	Checkpoint                   Checkpoint
	Events                       []Event
}
type fsOutcome struct {
	State  fsState
	Rule   string
	Result *Settlement
}

// Closures prevent fmt's reflection fallback (including nested invalid verbs)
// from walking private raw leaves. Direct JSON and formatting redact as well.
// Accessors clone; no mutable captured pointer is exposed. Not an unsafe or
// deliberate reflection security boundary, nor a heap-zeroization claim.
type fsRedacted struct{}

func (fsRedacted) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, "[settlement:redacted]") }
func (fsRedacted) MarshalJSON() ([]byte, error) { return []byte(`"[settlement:redacted]"`), nil }

type fsState struct {
	fsRedacted
	open func() []fsRow
}
type fsCommand struct {
	fsRedacted
	open func() fsRequest
}
type fsDecision struct {
	fsRedacted
	open func() fsOutcome
}

func fsOwnState(rows []fsRow) fsState {
	owned := fsCloneRows(rows)
	return fsState{open: func() []fsRow { return fsCloneRows(owned) }}
}
func (s fsState) fields() []fsRow {
	if s.open == nil {
		return nil
	}
	return s.open()
}
func (s fsState) clone() fsState { return fsOwnState(s.fields()) }
func fsOwnCommand(r fsRequest) fsCommand {
	r.Checkpoint = fsCloneCheckpoint(r.Checkpoint)
	r.Events = fsCloneEvents(r.Events)
	return fsCommand{open: func() fsRequest {
		f := r
		f.Checkpoint = fsCloneCheckpoint(f.Checkpoint)
		f.Events = fsCloneEvents(f.Events)
		return f
	}}
}
func (e fsCommand) fields() fsRequest {
	if e.open == nil {
		return fsRequest{}
	}
	return e.open()
}
func (e fsCommand) clone() fsCommand { return fsOwnCommand(e.fields()) }
func fsOwnDecision(f fsOutcome) fsDecision {
	f.State = f.State.clone()
	f.Result = fsCloneResult(f.Result)
	return fsDecision{open: func() fsOutcome { r := f; r.State = r.State.clone(); r.Result = fsCloneResult(r.Result); return r }}
}
func (d fsDecision) fields() fsOutcome {
	if d.open == nil {
		return fsOutcome{}
	}
	return d.open()
}
func fsCloneInput(p *Input) *Input {
	if p == nil {
		return nil
	}
	r := *p
	r.Payload = slices.Clone(p.Payload)
	r.PayloadHash = slices.Clone(p.PayloadHash)
	return &r
}
func fsCloneCheckpoint(c Checkpoint) Checkpoint {
	c.Context = slices.Clone(c.Context)
	c.Artifacts = slices.Clone(c.Artifacts)
	c.Pruning = slices.Clone(c.Pruning)
	c.TranscriptCursor = slices.Clone(c.TranscriptCursor)
	return c
}
func fsCloneEvents(v []Event) []Event {
	v = slices.Clone(v)
	for i := range v {
		v[i].Payload = slices.Clone(v[i].Payload)
	}
	return v
}
func fsCloneResult(r *Settlement) *Settlement {
	if r == nil {
		return nil
	}
	f := *r
	f.Next = fsCloneInput(r.Next)
	return &f
}
func fsCloneRows(rows []fsRow) []fsRow {
	rows = slices.Clone(rows)
	for i := range rows {
		r := &rows[i]
		r.Active = fsCloneInput(r.Active)
		r.Pending = slices.Clone(r.Pending)
		for j := range r.Pending {
			r.Pending[j] = *fsCloneInput(&r.Pending[j])
		}
		r.Settled = slices.Clone(r.Settled)
		r.Checkpoint = fsCloneCheckpoint(r.Checkpoint)
		r.Events = fsCloneEvents(r.Events)
	}
	return rows
}

// Canonicalization sorts only the key map, never the FIFO or event log. Deep
// equality preserves nil/empty distinctions; redacted JSON is NOT a state key.
func fsCanonicalEqual(a, b fsState) bool {
	x, y := a.fields(), b.fields()
	less := func(r []fsRow) func(int, int) bool {
		return func(i, j int) bool {
			if r[i].Key.Principal != r[j].Key.Principal {
				return r[i].Key.Principal < r[j].Key.Principal
			}
			return r[i].Key.Conversation < r[j].Key.Conversation
		}
	}
	sort.Slice(x, less(x))
	sort.Slice(y, less(y))
	return reflect.DeepEqual(x, y)
}
func fsKeyValid(k fsKey) bool { return validIDs(k.Principal) && validIDs(k.Conversation) }

var fsInvalid = errors.New("settlement model invariant or transition rejected")

// Snapshot invariants are independent from the reducer: unique scoped keys,
// disjoint active/pending/settled IDs, FIFO ordinals and contiguous safe events.
func fsCheckState(s fsState) error {
	keys := map[fsKey]bool{}
	for _, r := range s.fields() {
		if !fsKeyValid(r.Key) || keys[r.Key] || !validIDs(r.Owner) || r.Epoch == 0 || r.Epoch > fsMax || r.Revision > fsMax || r.Expires > fsMax || r.Authority < fsLive || r.Authority > fsStolen || r.Checkpoint.Version > fsMax {
			return fsInvalid
		}
		keys[r.Key] = true
		ids := map[string]bool{}
		ordinal := uint64(0)
		add := func(p Input) bool {
			if !validIDs(p.InputID) || ids[p.InputID] || p.Ordinal == 0 || p.Ordinal > fsMax || p.Ordinal <= ordinal {
				return false
			}
			ids[p.InputID] = true
			ordinal = p.Ordinal
			return true
		}
		if r.Active != nil && !add(*r.Active) {
			return fsInvalid
		}
		for _, p := range r.Pending {
			if !add(p) {
				return fsInvalid
			}
		}
		for _, id := range r.Settled {
			if !validIDs(id) || ids[id] {
				return fsInvalid
			}
			ids[id] = true
		}
		for i, e := range r.Events {
			if e.Sequence != uint64(i+1) || !validIDs(e.TurnID) || !validIDs(e.Kind) {
				return fsInvalid
			}
		}
	}
	return nil
}
func fsRequestValid(e fsRequest) bool {
	if !fsKeyValid(e.Key) || !validIDs(e.Owner) || e.Epoch == 0 || e.Epoch > fsMax || e.Now > fsMax || !validIDs(e.TurnID) || e.Checkpoint.Version > fsMax || (e.Mode != fsComplete && e.Mode != fsHold) || (e.Transaction != fsCommit && e.Transaction != fsRollback && e.Transaction != fsMutationFailed) {
		return false
	}
	for _, v := range e.Events {
		if !validIDs(v.Kind) || (v.TurnID != "" && v.TurnID != e.TurnID) {
			return false
		}
	}
	return true
}
func fsReduce(s fsState, c fsCommand) fsDecision {
	e := c.fields()
	reject := func(rule string) fsDecision { return fsOwnDecision(fsOutcome{State: s, Rule: rule}) }
	if !fsRequestValid(e) {
		return reject("FS001")
	}
	if fsCheckState(s) != nil {
		return reject("FS002")
	}
	rows := s.fields()
	index := -1
	for i := range rows {
		if rows[i].Key == e.Key {
			index = i
			break
		}
	}
	if index < 0 {
		return reject("FS010")
	}
	r := &rows[index]
	if r.Authority != fsLive {
		return reject("FS012")
	}
	if r.Owner != e.Owner {
		return reject("FS013")
	}
	if r.Epoch != e.Epoch {
		return reject("FS014")
	}
	if e.Now >= r.Expires {
		return reject("FS015")
	}
	if r.Deleted {
		return reject("FS016")
	}
	if r.Revision != e.ExpectedRevision {
		return reject("FS017")
	}
	if r.Active == nil || r.Active.InputID != e.TurnID {
		return reject("FS018")
	}
	if r.Revision == fsMax || uint64(len(e.Events)) > fsMax-uint64(len(r.Events)) {
		return reject("FS019")
	}
	if e.Transaction != fsCommit {
		return reject("FS090")
	}
	r.Checkpoint = fsCloneCheckpoint(e.Checkpoint)
	for _, v := range e.Events {
		v.Sequence = uint64(len(r.Events)) + 1
		v.TurnID = e.TurnID
		r.Events = append(r.Events, v)
	}
	r.Revision++
	result := &Settlement{Revision: r.Revision}
	rule := "FS102"
	if e.Mode == fsComplete {
		rule = "FS101"
		r.Settled = append(r.Settled, r.Active.InputID)
		r.Active = nil
		if len(r.Pending) > 0 {
			r.Active = fsCloneInput(&r.Pending[0])
			r.Pending = r.Pending[1:]
			result.Next = fsCloneInput(r.Active)
		}
	}
	return fsOwnDecision(fsOutcome{State: fsOwnState(rows), Rule: rule, Result: result})
}

// Independent transition guards verify supplied candidates; do not call Reduce
// or merely compare against its answer. Every field outside the selected key,
// authority and immutable input bytes remains unchanged, even on rejection.
func fsCheckTransition(s fsState, c fsCommand, d fsDecision) error {
	e, f := c.fields(), d.fields()
	before := s.fields()
	after := f.State.fields()
	// Classify the expected rule separately from mutation construction.
	expected := ""
	idx := -1
	if !fsRequestValid(e) {
		expected = "FS001"
	} else if fsCheckState(s) != nil {
		expected = "FS002"
	} else {
		for i, r := range before {
			if r.Key == e.Key {
				idx = i
				break
			}
		}
		if idx < 0 {
			expected = "FS010"
		} else {
			r := before[idx]
			switch {
			case r.Authority != fsLive:
				expected = "FS012"
			case r.Owner != e.Owner:
				expected = "FS013"
			case r.Epoch != e.Epoch:
				expected = "FS014"
			case e.Now >= r.Expires:
				expected = "FS015"
			case r.Deleted:
				expected = "FS016"
			case r.Revision != e.ExpectedRevision:
				expected = "FS017"
			case r.Active == nil || r.Active.InputID != e.TurnID:
				expected = "FS018"
			case r.Revision == fsMax || uint64(len(e.Events)) > fsMax-uint64(len(r.Events)):
				expected = "FS019"
			case e.Transaction != fsCommit:
				expected = "FS090"
			case e.Mode == fsComplete:
				expected = "FS101"
			case e.Mode == fsHold:
				expected = "FS102"
			}
		}
	}
	if f.Rule != expected {
		return fsInvalid
	}
	if expected != "FS101" && expected != "FS102" {
		if f.Result != nil || !fsCanonicalEqual(s, f.State) {
			return fsInvalid
		}
		return nil
	}
	if fsCheckState(f.State) != nil || len(before) != len(after) || f.Result == nil {
		return fsInvalid
	}
	// Permit canonical key ordering, but never FIFO reordering.
	var next *fsRow
	for i := range after {
		if after[i].Key == e.Key {
			next = &after[i]
		}
	}
	if next == nil {
		return fsInvalid
	}
	old := before[idx]
	newRow := *next
	if newRow.Revision != old.Revision+1 || f.Result.Revision != newRow.Revision || !reflect.DeepEqual(newRow.Checkpoint, e.Checkpoint) || len(newRow.Events) != len(old.Events)+len(e.Events) {
		return fsInvalid
	}
	for i, v := range old.Events {
		if !reflect.DeepEqual(newRow.Events[i], v) {
			return fsInvalid
		}
	}
	for i, v := range e.Events {
		v.Sequence = uint64(len(old.Events) + i + 1)
		v.TurnID = e.TurnID
		if !reflect.DeepEqual(newRow.Events[len(old.Events)+i], v) {
			return fsInvalid
		}
	}
	if e.Mode == fsHold {
		if f.Result.Next != nil || !reflect.DeepEqual(newRow.Active, old.Active) || !reflect.DeepEqual(newRow.Pending, old.Pending) || !reflect.DeepEqual(newRow.Settled, old.Settled) {
			return fsInvalid
		}
	} else {
		settled := append(slices.Clone(old.Settled), old.Active.InputID)
		var active *Input
		pending := old.Pending
		if len(pending) > 0 {
			active = &pending[0]
			pending = pending[1:]
		}
		if !reflect.DeepEqual(newRow.Settled, settled) || !reflect.DeepEqual(newRow.Active, active) || !reflect.DeepEqual(newRow.Pending, pending) || !reflect.DeepEqual(f.Result.Next, active) {
			return fsInvalid
		}
	}
	// Normalize only explicitly allowed fields, then compare the whole snapshot.
	newRow.Revision = old.Revision
	newRow.Checkpoint = old.Checkpoint
	newRow.Events = old.Events
	newRow.Active = old.Active
	newRow.Pending = old.Pending
	newRow.Settled = old.Settled
	for i := range after {
		if after[i].Key == e.Key {
			after[i] = newRow
		}
	}
	if !fsCanonicalEqual(s, fsOwnState(after)) {
		return fsInvalid
	}
	return nil
}
