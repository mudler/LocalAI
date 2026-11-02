// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/internal/dberr"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNoCapture = errors.New("studio store: no capture ready")
var ErrCaptureConflict = errors.New("studio store: stale capture fence or conflicting input")

type CaptureMode string

const (
	CaptureOff      CaptureMode = "off"
	CaptureIncoming CaptureMode = "incoming_only"
	CaptureReplies  CaptureMode = "incoming_and_replies"
	CaptureSummary  CaptureMode = "summary"
)

// CapturePolicy is frozen routing data, NOT an authorization attestation. Workers
// must reload current policy and principal access before every backend mutation.
type CapturePolicy struct {
	AgentID, Destination string
	Mode                 CaptureMode
}

// Origin deliberately has no arbitrary metadata, URL, credentials or attachment fields.
type CaptureOrigin struct{ Kind, ConnectorID string }
type MemoryAcceptance struct {
	Principal, ChatID, SourceID, Text string
	Policy                            CapturePolicy
	Origin                            CaptureOrigin
}
type AcceptedInput struct {
	ID      string
	Pending Pending
	Text    string
	Policy  CapturePolicy
	Origin  CaptureOrigin
}
type MemoryTurnCompletion struct {
	Principal, ChatID, RunID, ReplyID, ReplyText string
	Status                                       TurnStatus
	IncomingIDs                                  []string
}

// CaptureRecord is a storage projection of the Task 1 tagged reducer state.
// State/InputType discriminate concrete values; StateJSON never decodes an
// untrusted interface or substitutes a second lifecycle implementation.
type CaptureRecord struct {
	BackendFence     *CaptureBackendFence `json:",omitempty"`
	ID               string
	Identity         memory.Identity
	RawText          string
	Origin           CaptureOrigin
	State, InputType string
	StateJSON        json.RawMessage
	Owner            string
	Generation       int
	Deadline         time.Time
	Lease            time.Duration
	Revision         uint64
	RuleID           string
}

// Now must be refreshed from the worker clock before ApplyCapture. This queue
// fence does not fence backend writes: the worker must install the same owner /
// generation in the idempotent backend before executing mutation effects.
type CaptureClaim struct {
	Record CaptureRecord
	Now    time.Time
	// Transition is the durable handoff slot. Effects may run only after ClaimCapture
	// succeeds; its work carries exactly the persisted owner, generation and deadline.
	Transition memory.Transition
}
type captureData struct {
	DeadSources map[string]bool
	DeadKeys    map[string]bool
	Accepted    map[string]AcceptedInput
	Outcomes    map[string]MemoryTurnCompletion
	Records     map[string]CaptureRecord
}

func cloneCapture(d captureData) captureData {
	b, _ := json.Marshal(d)
	var n captureData
	_ = json.Unmarshal(b, &n)
	if n.DeadSources == nil {
		n.DeadSources = map[string]bool{}
	}
	if n.DeadKeys == nil {
		n.DeadKeys = map[string]bool{}
	}
	if n.Accepted == nil {
		n.Accepted = map[string]AcceptedInput{}
	}
	if n.Outcomes == nil {
		n.Outcomes = map[string]MemoryTurnCompletion{}
	}
	if n.Records == nil {
		n.Records = map[string]CaptureRecord{}
	}
	return n
}
func CaptureKey(i memory.Identity) string {
	// Origin is provenance, not identity: redelivery via a different transport is
	// still the same capture unit. Include chat to namespace caller source IDs.
	b, _ := json.Marshal([]string{"localai-capture-v1", i.Principal, i.Destination, i.Agent, i.Chat, i.Source, i.Kind})
	h := sha256.Sum256(b)
	return "v1:" + hex.EncodeToString(h[:])
}
func stateJob(s memory.State) memory.Job {
	switch v := s.(type) {
	case memory.Queued:
		return v.Job
	case memory.Authorizing:
		return v.Work.Job
	case memory.Transforming:
		return v.Work.Job
	case memory.PersistingPayload:
		return v.Work.Job
	case memory.Writing:
		return v.Work.Job
	case memory.RetryWaiting:
		return v.Job
	case memory.Saved:
		return v.Job
	case memory.Failed:
		return v.Job
	case memory.Skipped:
		return v.Job
	}
	panic("unregistered capture state")
}
func stateWork(s memory.State) (memory.Work, bool) {
	switch v := s.(type) {
	case memory.Authorizing:
		return v.Work, true
	case memory.Transforming:
		return v.Work, true
	case memory.PersistingPayload:
		return v.Work, true
	case memory.Writing:
		return v.Work, true
	}
	return memory.Work{}, false
}
func (r CaptureRecord) ReducerState() (memory.State, error) {
	var input memory.Input
	switch r.InputType {
	case "ContentInput":
		input = &memory.ContentInput{}
	case "SummaryInput":
		input = &memory.SummaryInput{}
	case "PendingPayload":
		input = &memory.PendingPayload{}
	case "FrozenPayload":
		input = &memory.FrozenPayload{}
	default:
		return nil, fmt.Errorf("unknown capture input %q", r.InputType)
	}
	// Decode the input separately so null/absent interfaces cannot erase a
	// preinitialized concrete pointer (or silently retain its zero value).
	var fields struct {
		Job  *struct{ Input json.RawMessage }
		Work *struct {
			Job *struct{ Input json.RawMessage }
		}
	}
	if err := json.Unmarshal(r.StateJSON, &fields); err != nil {
		return nil, err
	}
	job := fields.Job
	switch r.State {
	case "Authorizing", "Transforming", "PersistingPayload", "Writing":
		job = nil
		if fields.Work != nil {
			job = fields.Work.Job
		}
	}
	if job == nil || len(job.Input) == 0 || string(job.Input) == "null" {
		return nil, errors.New("capture state requires a job with concrete input")
	}
	if err := json.Unmarshal(job.Input, input); err != nil {
		return nil, err
	}
	concrete := input
	// Reducer constructors use values, never pointers or typed nil inputs.
	switch v := input.(type) {
	case *memory.ContentInput:
		input = *v
	case *memory.SummaryInput:
		input = *v
	case *memory.PendingPayload:
		input = *v
	case *memory.FrozenPayload:
		input = *v
	}
	// Decode the remaining state fields with a concrete pointer in the interface,
	// then explicitly install the validated value constructor.
	var s memory.State
	switch r.State {
	case "Queued":
		v := memory.Queued{}
		v.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Job.Input = input
		s = v
	case "Authorizing":
		v := memory.Authorizing{}
		v.Work.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Work.Job.Input = input
		s = v
	case "Transforming":
		v := memory.Transforming{}
		v.Work.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Work.Job.Input = input
		s = v
	case "PersistingPayload":
		v := memory.PersistingPayload{}
		v.Work.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Work.Job.Input = input
		s = v
	case "Writing":
		v := memory.Writing{}
		v.Work.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Work.Job.Input = input
		s = v
	case "RetryWaiting":
		v := memory.RetryWaiting{}
		v.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Job.Input = input
		s = v
	case "Saved":
		v := memory.Saved{}
		v.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Job.Input = input
		s = v
	case "Failed":
		v := memory.Failed{}
		v.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Job.Input = input
		s = v
	case "Skipped":
		v := memory.Skipped{}
		v.Job.Input = concrete
		if err := json.Unmarshal(r.StateJSON, &v); err != nil {
			return nil, err
		}
		v.Job.Input = input
		s = v
	default:
		return nil, fmt.Errorf("unknown capture state %q", r.State)
	}
	if err := memory.CheckInvariants(s); err != nil {
		return nil, err
	}
	if stateJob(s).Identity != r.Identity {
		return nil, ErrCaptureConflict
	}
	return s, nil
}

func project(r CaptureRecord, s memory.State, rule string) (CaptureRecord, error) {
	if err := memory.CheckInvariants(s); err != nil {
		return r, err
	}
	j := stateJob(s)
	r.State = reflect.TypeOf(s).Name()
	r.InputType = reflect.TypeOf(j.Input).Name()
	r.StateJSON, _ = json.Marshal(s)
	r.Generation = j.Generation
	r.RuleID = rule
	r.Revision++
	if w, ok := stateWork(s); ok {
		r.Owner = w.Fence.Owner
		r.Deadline = time.Unix(w.Deadline, 0)
	} else {
		r.Owner = ""
		r.Deadline = time.Time{}
	}
	return r, nil
}
func addCapture(d *captureData, a AcceptedInput, principal, chat, source, kind, text string, now time.Time) error {
	i := memory.Identity{Principal: principal, Destination: a.Policy.Destination, Agent: a.Policy.AgentID, Chat: chat, Source: source, Kind: kind}
	id := CaptureKey(i)
	if d.DeadKeys[id] {
		return ErrCaptureConflict
	}
	if _, ok := d.Records[id]; ok {
		return nil
	}
	var input memory.Input = memory.ContentInput{Text: text}
	if kind == "summary" {
		input = memory.SummaryInput{Text: text}
	}
	r, err := project(CaptureRecord{ID: id, Identity: i, RawText: text, Origin: a.Origin}, memory.Queued{Job: memory.Job{Identity: i, Input: input, Expires: now.Add(24 * time.Hour).Unix()}}, "R01-accepted")
	if err == nil {
		d.Records[id] = r
	}
	return err
}
func validateAcceptance(in MemoryAcceptance) error {
	if in.Principal == "" || in.ChatID == "" || in.SourceID == "" {
		return errors.New("missing acceptance identity")
	}
	switch in.Policy.Mode {
	case "", CaptureOff:
	case CaptureIncoming, CaptureReplies, CaptureSummary:
		if in.Policy.AgentID == "" || in.Policy.Destination == "" {
			return errors.New("missing capture destination or agent")
		}
	default:
		return errors.New("unknown capture policy")
	}
	return nil
}

type captureEdit func(context.Context, string, string, func(*captureData, *chatState) error) error

func acceptMemory(ctx context.Context, in MemoryAcceptance, edit captureEdit) (AcceptedInput, error) {
	var out AcceptedInput
	if err := validateAcceptance(in); err != nil {
		return out, err
	}
	err := edit(ctx, in.Principal, in.ChatID, func(d *captureData, c *chatState) error {
		if d.DeadSources[in.SourceID] {
			return ErrCaptureConflict
		}
		if old, ok := d.Accepted[in.SourceID]; ok {
			out = old
			return nil
		}
		if in.Policy.Mode != "" && in.Policy.Mode != CaptureOff && c.Rec.AgentID != in.Policy.AgentID {
			return ErrCaptureConflict
		}
		c.NextPending++
		p := Pending{ID: in.SourceID, Text: in.Text, Seq: c.NextPending, At: time.Now().UTC()}
		c.Pending = append(c.Pending, p)
		out = AcceptedInput{ID: in.SourceID, Pending: p, Text: in.Text, Policy: in.Policy, Origin: in.Origin}
		d.Accepted[in.SourceID] = out
		if strings.TrimSpace(in.Text) != "" && (in.Policy.Mode == CaptureIncoming || in.Policy.Mode == CaptureReplies) {
			return addCapture(d, out, in.Principal, in.ChatID, in.SourceID, "incoming", in.Text, p.At)
		}
		return nil
	})
	return out, err
}
func completeMemory(ctx context.Context, in MemoryTurnCompletion, edit captureEdit) error {
	if in.RunID == "" || in.ReplyID == "" {
		return errors.New("missing completion identity")
	}
	return edit(ctx, in.Principal, in.ChatID, func(d *captureData, c *chatState) error {
		if old, ok := d.Outcomes[in.ReplyID]; ok {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(in)
			if string(a) != string(b) {
				return ErrCaptureConflict
			}
			return nil
		}
		if c.Run != in.RunID {
			return ErrNotClaimed
		}
		// Validate all membership before mutating anything, including non-success outcomes.
		var accepted []AcceptedInput
		seen := map[string]bool{}
		for _, id := range in.IncomingIDs {
			a, ok := d.Accepted[id]
			if !ok || seen[id] {
				return ErrCaptureConflict
			}
			seen[id] = true
			accepted = append(accepted, a)
		}
		d.Outcomes[in.ReplyID] = in
		c.Run = ""
		c.Last = in.Status
		if in.Status != TurnDone || strings.TrimSpace(in.ReplyText) == "" {
			return nil
		}
		// Mixed policy exchanges capture only the members accepted under that frozen
		// policy; never combine another destination's text into a summary.
		groups := map[CapturePolicy][]AcceptedInput{}
		for _, a := range accepted {
			groups[a.Policy] = append(groups[a.Policy], a)
		}
		for policy, members := range groups {
			a := members[0]
			switch policy.Mode {
			case CaptureReplies:
				if err := addCapture(d, a, in.Principal, in.ChatID, in.ReplyID, "reply", in.ReplyText, time.Now()); err != nil {
					return err
				}
			case CaptureSummary:
				texts := []string{}
				for _, m := range members {
					if strings.TrimSpace(m.Text) != "" {
						texts = append(texts, m.Text)
					}
				}
				if len(texts) == 0 {
					continue
				}
				texts = append(texts, in.ReplyText)
				if err := addCapture(d, a, in.Principal, in.ChatID, in.ReplyID, "summary", strings.Join(texts, "\n\n"), time.Now()); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type captureStateRow struct {
	ChatID string `gorm:"primaryKey;size:128"`
	Data   []byte
}

func (captureStateRow) TableName() string { return "studio_capture_states" }

// Keys survive chat deletion. The unique primary key is the SQL idempotency
// boundary, in addition to serialization of each chat snapshot under its row lock.
type captureKeyRow struct {
	ID     string `gorm:"primaryKey;size:128"`
	ChatID string `gorm:"index;size:128"`
}

func (captureKeyRow) TableName() string { return "studio_capture_keys" }
func tombstoneCapture(d captureData) captureData {
	d = cloneCapture(d)
	for id := range d.Accepted {
		d.DeadSources[id] = true
	}
	for id := range d.Records {
		d.DeadKeys[id] = true
	}
	d.Accepted = map[string]AcceptedInput{}
	d.Outcomes = map[string]MemoryTurnCompletion{}
	d.Records = map[string]CaptureRecord{}
	return d
}

// A shorter queue lease must also shorten the reducer operation deadline. Never
// advance the supplied clock to trick recovery into accepting an expired owner.
func leaseState(s memory.State, now time.Time, lease time.Duration) memory.State {
	w, ok := stateWork(s)
	if !ok {
		return s
	}
	deadline := now.Add(lease).Unix()
	if deadline < w.Deadline {
		w.Deadline = deadline
	}
	v := reflect.New(reflect.TypeOf(s)).Elem()
	v.Set(reflect.ValueOf(s))
	v.FieldByName("Work").Set(reflect.ValueOf(w))
	return v.Interface().(memory.State)
}
func (s *jsonStore) editCapture(ctx context.Context, p, c string, f func(*captureData, *chatState) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.edit(p, c, func(next *chatState) error {
		d := cloneCapture(next.Capture)
		next.Pending = append([]Pending(nil), next.Pending...)
		next.captureContext = withCaptureChatRead(ctx, s, next.Rec)
		defer func() { next.captureContext = nil }()
		if err := f(&d, next); err != nil {
			return err
		}
		next.Capture = d
		return nil
	})
}
func (s *gormStore) editCapture(ctx context.Context, p, c string, f func(*captureData, *chatState) error) error {
	return s.withChatLock(ctx, p, c, func(tx *gorm.DB) error {
		var row captureStateRow
		if err := tx.Where("chat_id = ?", c).Limit(1).Find(&row).Error; err != nil {
			return err
		}
		var d captureData
		if len(row.Data) > 0 {
			if err := json.Unmarshal(row.Data, &d); err != nil {
				return err
			}
		}
		d = cloneCapture(d)
		var cr chatRow
		if err := tx.Where("id = ?", c).First(&cr).Error; err != nil {
			return err
		}
		rec, err := cr.record()
		if err != nil {
			return err
		}
		state := chatState{Rec: rec, NextPending: cr.NextPending, Run: cr.RunID, Last: TurnStatus(cr.LastTurn)}
		state.captureContext = dberr.WithReadTransaction(ctx, s.db, tx)
		if err = f(&d, &state); err != nil {
			return err
		}
		// Insert only new accepted messages; edits/cancellation/restores remain the
		// existing pending APIs and cannot alter the immutable acceptance payload.
		for _, a := range d.Accepted {
			var n int64
			if err = tx.Model(&pendingRow{}).Where("chat_id = ? AND id = ?", c, a.ID).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 { // only insert when this acceptance was created in this transaction
				var old captureData
				_ = json.Unmarshal(row.Data, &old)
				if _, exists := old.Accepted[a.ID]; exists {
					continue
				}
				p := a.Pending
				if err = tx.Create(&pendingRow{ChatID: c, ID: p.ID, Seq: p.Seq, Text: p.Text, At: p.At}).Error; err != nil {
					return err
				}
			}
		}
		if err = tx.Model(&chatRow{}).Where("id = ?", c).Updates(map[string]any{"next_pending": state.NextPending, "run_id": state.Run, "last_turn": state.Last}).Error; err != nil {
			return err
		}
		row.ChatID = c
		row.Data, err = json.Marshal(d)
		if err != nil {
			return err
		}
		for id := range d.Records {
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&captureKeyRow{ID: id, ChatID: c}).Error; err != nil {
				return err
			}
		}
		return tx.Save(&row).Error
	})
}
func (s *jsonStore) AcceptMemoryInput(ctx context.Context, in MemoryAcceptance) (AcceptedInput, error) {
	return acceptMemory(ctx, in, s.editCapture)
}
func (s *gormStore) AcceptMemoryInput(ctx context.Context, in MemoryAcceptance) (AcceptedInput, error) {
	return acceptMemory(ctx, in, s.editCapture)
}
func (s *jsonStore) CompleteMemoryTurn(ctx context.Context, in MemoryTurnCompletion) error {
	return completeMemory(ctx, in, s.editCapture)
}
func (s *gormStore) CompleteMemoryTurn(ctx context.Context, in MemoryTurnCompletion) error {
	return completeMemory(ctx, in, s.editCapture)
}

func listCaptures(ctx context.Context, p, c string, edit captureEdit) ([]CaptureRecord, error) {
	out := []CaptureRecord{}
	err := edit(ctx, p, c, func(d *captureData, _ *chatState) error {
		for _, r := range d.Records {
			out = append(out, r)
		}
		return nil
	})
	return out, err
}
func (s *jsonStore) ListCaptures(ctx context.Context, p, c string) ([]CaptureRecord, error) {
	return listCaptures(ctx, p, c, s.editCapture)
}
func (s *gormStore) ListCaptures(ctx context.Context, p, c string) ([]CaptureRecord, error) {
	return listCaptures(ctx, p, c, s.editCapture)
}
func claimRecord(d *captureData, owner string, now time.Time, lease time.Duration) (CaptureClaim, error) {
	if owner == "" || lease < time.Second || lease > 60*time.Second {
		return CaptureClaim{}, errors.New("capture lease must be 1s..60s with nonempty owner")
	}
	for id, r := range d.Records {
		s, err := r.ReducerState()
		if err != nil {
			return CaptureClaim{}, err
		}
		switch s.(type) {
		case memory.Saved, memory.Skipped, memory.Failed:
			continue
		}
		if r.Owner != "" && now.Before(r.Deadline) {
			continue
		}
		if wait, ok := s.(memory.RetryWaiting); ok && now.Unix() < wait.Due {
			continue
		}
		j := stateJob(s)
		env := memory.Envelope{Key: j.Identity.Key(), Owner: owner, Generation: j.Generation + 1, Now: now.Unix()}
		var tr memory.Transition
		if _, ok := s.(memory.Queued); ok {
			tr = memory.Reduce(s, memory.ClaimGranted{Envelope: env})
		} else {
			tr = memory.Reduce(s, memory.RestartRecovered{Envelope: env})
		}
		if tr.IgnoredReason != "" {
			continue
		}
		tr.State = leaseState(tr.State, now, lease)
		r.BackendFence = nil
		r.Lease = lease
		r, err = project(r, tr.State, tr.RuleID)
		if err != nil {
			return CaptureClaim{}, err
		}
		if w, ok := stateWork(tr.State); ok {
			for i, effect := range tr.Effects {
				if _, ok := effect.(memory.CheckAuthorization); ok {
					tr.Effects[i] = memory.CheckAuthorization{Work: w}
				}
			}
		}
		d.Records[id] = r
		return CaptureClaim{Record: r, Now: now, Transition: tr}, nil
	}
	return CaptureClaim{}, ErrNoCapture
}
func (s *jsonStore) ClaimCapture(ctx context.Context, owner string, now time.Time, lease time.Duration) (CaptureClaim, error) {
	for _, slot := range s.slots() {
		slot.mu.Lock()
		p, c, deleted := slot.Rec.Principal, slot.Rec.ID, slot.Deleted
		slot.mu.Unlock()
		if deleted {
			continue
		}
		var out CaptureClaim
		err := s.editCapture(ctx, p, c, func(d *captureData, _ *chatState) error {
			var err error
			out, err = claimRecord(d, owner, now, lease)
			return err
		})
		if errors.Is(err, ErrNoCapture) {
			continue
		}
		return out, err
	}
	return CaptureClaim{}, ErrNoCapture
}
func (s *gormStore) ClaimCapture(ctx context.Context, owner string, now time.Time, lease time.Duration) (CaptureClaim, error) {
	var chats []chatRow
	if err := s.db.WithContext(ctx).Select("id", "principal").Order("id").Find(&chats).Error; err != nil {
		return CaptureClaim{}, err
	}
	for _, c := range chats {
		var out CaptureClaim
		err := s.editCapture(ctx, c.Principal, c.ID, func(d *captureData, _ *chatState) error {
			var err error
			out, err = claimRecord(d, owner, now, lease)
			return err
		})
		if errors.Is(err, ErrNoCapture) {
			continue
		}
		return out, err
	}
	return CaptureClaim{}, ErrNoCapture
}
func applyCapture(ctx context.Context, claim CaptureClaim, tr memory.Transition, edit captureEdit) error {
	r := claim.Record
	return edit(ctx, r.Identity.Principal, r.Identity.Chat, func(d *captureData, _ *chatState) error {
		current, ok := d.Records[r.ID]
		if !ok || current.Revision != r.Revision || current.Owner != r.Owner || current.Generation != r.Generation || r.Owner == "" || !claim.Now.Before(current.Deadline) {
			return ErrCaptureConflict
		}
		// Callers must provide current effect completion time in claim.Now. Fence
		// the state too: key/destination, generation and owner cannot be substituted.
		j := stateJob(tr.State)
		if j.Identity != current.Identity || j.Generation != current.Generation {
			return ErrCaptureConflict
		}
		if w, active := stateWork(tr.State); active && w.Fence.Owner != current.Owner {
			return ErrCaptureConflict
		}
		next, err := project(current, leaseState(tr.State, claim.Now, current.Lease), tr.RuleID)
		if err != nil {
			return err
		}
		d.Records[r.ID] = next
		return nil
	})
}
func (s *jsonStore) ApplyCapture(ctx context.Context, c CaptureClaim, tr memory.Transition) error {
	return applyCapture(ctx, c, tr, s.editCapture)
}
func (s *gormStore) ApplyCapture(ctx context.Context, c CaptureClaim, tr memory.Transition) error {
	return applyCapture(ctx, c, tr, s.editCapture)
}
func retryCapture(ctx context.Context, p, c, id string, edit captureEdit) error {
	return edit(ctx, p, c, func(d *captureData, _ *chatState) error {
		r, ok := d.Records[id]
		if !ok {
			return ErrNoCapture
		}
		s, err := r.ReducerState()
		if err != nil {
			return err
		}
		f, ok := s.(memory.Failed)
		if !ok {
			return ErrCaptureConflict
		}
		// Retry is scheduling only, never an authorization grant. The next claim
		// starts at Authorizing and must check current rights before any effect.
		j := f.Job
		j.Attempts = 0
		j.Generation++
		j.Expires = time.Now().Add(24 * time.Hour).Unix()
		next, err := project(r, memory.Queued{Job: j}, "R08-retry-queued")
		if err == nil {
			d.Records[id] = next
		}
		return err
	})
}
func (s *jsonStore) RetryCapture(ctx context.Context, p, c, id string) error {
	return retryCapture(ctx, p, c, id, s.editCapture)
}
func (s *gormStore) RetryCapture(ctx context.Context, p, c, id string) error {
	return retryCapture(ctx, p, c, id, s.editCapture)
}
