// SPDX-License-Identifier: MIT
package ownership

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// These values contain identifiers and digests only. Closure leaves also redact
// fmt's private-container and invalid-verb fallback. They confer no authority.
type executionRedacted struct{}

func (executionRedacted) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, "[execution:redacted]")
}
func (executionRedacted) MarshalJSON() ([]byte, error) { return []byte(`"[execution:redacted]"`), nil }

type ExecutionMetadata struct {
	executionRedacted
	open func() executionFields
}
type ExecutionAdmission struct {
	executionRedacted
	open func() executionRow
}
type CompletedExecution struct {
	executionRedacted
	open func() string
}

func (c CompletedExecution) Bytes() []byte {
	if c.open == nil {
		return nil
	}
	return []byte(c.open())
}
func (a ExecutionAdmission) RequestID() string {
	if a.open == nil {
		return ""
	}
	return a.open().RequestID
}
func (a ExecutionAdmission) Digest() string {
	if a.open == nil {
		return ""
	}
	return a.open().Digest
}
func (a ExecutionAdmission) Call() ToolCall {
	if a.open == nil {
		return ToolCall{}
	}
	r := a.open()
	return ToolCall{r.Turn, r.RequestID, r.Binding, uint64(r.Attempt)}
}

type executionFields struct {
	Turn, Invocation, Protocol, Helper, Registration, Agent, Tool          string
	Attempt, Revision                                                      uint64
	Catalog, Config, Authorization, Resource, Proposal, ParentGrant, Taint string
	Mode                                                                   uint8
	TTL                                                                    int64
	Tainted                                                                bool
	Codec                                                                  uint64
	Arguments, Binding, Provenance                                         string
}
type executionRow struct {
	Principal                                                              string `gorm:"primaryKey;autoIncrement:false;uniqueIndex:execution_origin,priority:1"`
	Conversation                                                           string `gorm:"primaryKey;autoIncrement:false;uniqueIndex:execution_origin,priority:2"`
	Turn                                                                   string `gorm:"primaryKey;autoIncrement:false;uniqueIndex:execution_origin,priority:3"`
	RequestID                                                              string `gorm:"primaryKey;autoIncrement:false"`
	Attempt                                                                int64  `gorm:"uniqueIndex:execution_origin,priority:4"`
	Epoch                                                                  int64  `gorm:"uniqueIndex:execution_origin,priority:5"`
	Invocation                                                             string `gorm:"uniqueIndex:execution_origin,priority:6"`
	Protocol, Helper, Registration, Agent, Tool                            string
	Revision                                                               int64
	Catalog, Config, Authorization, Resource, Proposal, ParentGrant, Taint string
	Mode                                                                   uint8
	TTL                                                                    int64
	Tainted                                                                bool
	Codec                                                                  int64
	Arguments, Binding, Provenance, Digest                                 string
	Version                                                                int64
}

func (executionRow) TableName() string { return "runtime_execution_admissions" }

var ErrExecutionUnavailable = errors.New("ownership: completed execution unavailable")
var errExecution = errors.New("ownership: execution operation failed")

func executionID(s string) bool {
	return validID(s) && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func executionNumber(n uint64) bool { return n > 0 && n <= math.MaxInt64 }
func executionDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func validExecution(f executionFields) bool {
	for _, s := range []string{f.Turn, f.Invocation, f.Protocol, f.Helper, f.Registration, f.Agent, f.Tool, f.Catalog, f.Config, f.Authorization, f.Resource, f.Proposal, f.ParentGrant, f.Taint} {
		if !executionID(s) {
			return false
		}
	}
	return executionNumber(f.Attempt) && executionNumber(f.Revision) && f.Codec == 1 && f.Mode <= 3 && f.TTL > 0 && f.TTL <= int64(MaxTTL) && executionDigest(f.Arguments) && executionDigest(f.Binding) && executionDigest(f.Provenance)
}

// NewExecutionMetadata freezes host-validated registration and policy metadata.
// Policy uses toolbroker's fixed encoding 0..3; TTL uses nanoseconds, at most
// MaxTTL. Codec and admission digest version are exactly 1 in this phase.
// Digests are lowercase SHA256 hex. No arguments, credentials or results enter.
func NewExecutionMetadata(turn, invocation, protocol, helper, registration, agent, tool string, attempt, revision uint64, revisions [7]string, mode uint8, ttl time.Duration, tainted bool, codec uint64, arguments, binding, provenance string) (ExecutionMetadata, error) {
	f := executionFields{turn, invocation, protocol, helper, registration, agent, tool, attempt, revision, revisions[0], revisions[1], revisions[2], revisions[3], revisions[4], revisions[5], revisions[6], mode, int64(ttl), tainted, codec, arguments, binding, provenance}
	if !validExecution(f) {
		return ExecutionMetadata{}, ErrInvalidInput
	}
	return ExecutionMetadata{open: func() executionFields { return f }}, nil
}
func executionCandidate(l Lease, f executionFields) executionRow {
	b, _ := json.Marshal([]any{"host-execution-request/v1", l.Principal, l.Conversation, f.Turn, f.Attempt, l.Epoch, f.Invocation})
	id := sha256.Sum256(b)
	r := executionRow{Principal: l.Principal, Conversation: l.Conversation, Turn: f.Turn, RequestID: "he1_" + hex.EncodeToString(id[:]), Attempt: int64(f.Attempt), Epoch: int64(l.Epoch), Invocation: f.Invocation, Protocol: f.Protocol, Helper: f.Helper, Registration: f.Registration, Agent: f.Agent, Tool: f.Tool, Revision: int64(f.Revision), Catalog: f.Catalog, Config: f.Config, Authorization: f.Authorization, Resource: f.Resource, Proposal: f.Proposal, ParentGrant: f.ParentGrant, Taint: f.Taint, Mode: f.Mode, TTL: f.TTL, Tainted: f.Tainted, Codec: int64(f.Codec), Arguments: f.Arguments, Binding: f.Binding, Provenance: f.Provenance, Version: 1}
	r.Digest = executionRowDigest(r)
	return r
}
func executionRowDigest(r executionRow) string {
	r.Digest = ""
	b, _ := json.Marshal([]any{"host-execution-admission/v1", r})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func validExecutionRow(r executionRow) bool {
	if r.Epoch <= 0 || r.Version != 1 || !executionID(r.Principal) || !executionID(r.Conversation) {
		return false
	}
	f := executionFields{r.Turn, r.Invocation, r.Protocol, r.Helper, r.Registration, r.Agent, r.Tool, uint64(r.Attempt), uint64(r.Revision), r.Catalog, r.Config, r.Authorization, r.Resource, r.Proposal, r.ParentGrant, r.Taint, r.Mode, r.TTL, r.Tainted, uint64(r.Codec), r.Arguments, r.Binding, r.Provenance}
	return validExecution(f) && executionCandidate(Lease{Key: Key{r.Principal, r.Conversation}, Epoch: uint64(r.Epoch)}, f) == r
}

// ExactExecutionOrigin reconstructs an explicit origin reference after restart.
// Scope is derived only from the current lease; origin epoch and all immutable
// origin metadata must come from trusted host provenance, never ID inference.
// This pure reference grants no read, release or dispatch permission.
func ExactExecutionOrigin(current Lease, metadata ExecutionMetadata, originEpoch uint64) (ExecutionAdmission, error) {
	if !executionID(current.Principal) || !executionID(current.Conversation) || metadata.open == nil || !validExecution(metadata.open()) || !executionNumber(originEpoch) {
		return ExecutionAdmission{}, ErrInvalidInput
	}
	current.Epoch = originEpoch
	return ownExecution(executionCandidate(current, metadata.open())), nil
}

func ownExecution(r executionRow) ExecutionAdmission {
	return ExecutionAdmission{open: func() executionRow { return r }}
}

// Silent sessions prevent SQL diagnostics from disclosing identifiers or durable
// envelopes. Errors crossing this seam are normalized, never raw driver errors.
func executionError(ctx context.Context, e error) error {
	if e == nil {
		return nil
	}
	if ce := ctx.Err(); ce != nil && (errors.Is(e, sql.ErrTxDone) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded)) {
		return ce
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrLeaseLost, ErrInvalidInput, ErrConflict, ErrNotFound, store.ErrConflict, store.ErrNotFound, store.ErrDeleted, ErrExecutionUnavailable} {
		if errors.Is(e, known) {
			return known
		}
	}
	return errExecution
}
func (m *Manager) executionFence(ctx context.Context, l Lease, n time.Time, fn func(*gorm.DB) error) error {
	if !executionID(l.Principal) || !executionID(l.Conversation) || !executionID(l.Owner) {
		return ErrInvalidInput
	}
	quiet := &Manager{db: m.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})}
	return executionError(ctx, quiet.fenced(ctx, l, n, fn))
}

// Lock the live conversation row after the lease, in the same transaction. This
// serializes against store CAS writers on both dialects, not just other owners.
func executionTarget(tx *gorm.DB, l Lease, turn string, revision uint64) error {
	if !executionID(turn) || !executionNumber(revision) {
		return ErrInvalidInput
	}
	q := scope(tx, l.Key).Table("runtime_conversations").Where("deleted = ?", false).UpdateColumn("revision", gorm.Expr("revision"))
	if q.Error != nil {
		return q.Error
	}
	var r struct {
		Revision, LastOrdinal, LastSequence uint64
		RunningTurn                         string
		Deleted                             bool
	}
	e := scope(tx, l.Key).Table("runtime_conversations").Take(&r).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return store.ErrNotFound
	}
	if e != nil {
		return e
	}
	if r.Deleted {
		return store.ErrDeleted
	}
	if r.Revision == 0 || r.Revision > math.MaxInt64 || r.LastOrdinal == 0 || r.LastOrdinal > math.MaxInt64 || r.LastSequence > math.MaxInt64 || r.Revision != revision || r.RunningTurn != turn {
		return store.ErrConflict
	}
	var input struct {
		Ordinal uint64
		Settled bool
	}
	e = scope(tx, l.Key).Table("runtime_inputs").Where("input_id = ?", turn).Take(&input).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return store.ErrConflict
	}
	if e != nil {
		return e
	}
	if input.Settled || input.Ordinal == 0 || input.Ordinal > r.LastOrdinal {
		return store.ErrConflict
	}
	return nil
}

// AdmitExecution persists correlation and immutable metadata, never dispatch
// permission. Only the lease supplies principal/conversation and origin epoch.
func (m *Manager) AdmitExecution(ctx context.Context, l Lease, n time.Time, metadata ExecutionMetadata) (ExecutionAdmission, error) {
	if metadata.open == nil || !validExecution(metadata.open()) {
		return ExecutionAdmission{}, ErrInvalidInput
	}
	f := metadata.open()
	var out ExecutionAdmission
	e := m.executionFence(ctx, l, n, func(tx *gorm.DB) error {
		if e := executionTarget(tx, l, f.Turn, f.Revision); e != nil {
			return e
		}
		candidate := executionCandidate(l, f)
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; e != nil {
			return e
		}
		var r executionRow
		e := scope(tx, l.Key).Where("turn = ? AND request_id = ?", f.Turn, candidate.RequestID).Take(&r).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if !validExecutionRow(r) || r != candidate {
			return ErrConflict
		}
		out = ownExecution(r)
		return nil
	})
	if e != nil {
		return ExecutionAdmission{}, e
	}
	return out, nil
}
func executionLookup(tx *gorm.DB, l Lease, ref ExecutionAdmission) (executionRow, error) {
	if ref.open == nil {
		return executionRow{}, ErrConflict
	}
	wanted := ref.open()
	if !validExecutionRow(wanted) || wanted.Principal != l.Principal || wanted.Conversation != l.Conversation {
		return executionRow{}, ErrConflict
	}
	var r executionRow
	e := scope(tx, l.Key).Where("turn = ? AND request_id = ?", wanted.Turn, wanted.RequestID).Take(&r).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return executionRow{}, ErrConflict
	}
	if e != nil {
		return executionRow{}, e
	}
	if !validExecutionRow(r) || r != wanted {
		return executionRow{}, ErrConflict
	}
	return r, nil
}

// DispatchAdmittedExecution is a fresh Prepared transition only. Admission and
// recovery do not authorize execution; the bridge must still authenticate policy
// and supply its single-use continuation. Legacy Dispatch remains unchanged.
func (m *Manager) DispatchAdmittedExecution(ctx context.Context, l Lease, n time.Time, revision uint64, ref ExecutionAdmission) (ExecutionAdmission, error) {
	var out ExecutionAdmission
	e := m.executionFence(ctx, l, n, func(tx *gorm.DB) error {
		r, e := executionLookup(tx, l, ref)
		if e != nil {
			return e
		}
		if e = executionTarget(tx, l, r.Turn, revision); e != nil {
			return e
		}
		if uint64(r.Epoch) != l.Epoch || uint64(r.Revision) != revision {
			return ErrConflict
		}
		var count int64
		e = scope(tx, l.Key).Model(&toolRow{}).Where("turn_id = ? AND request_id <> ? AND status IN ?", r.Turn, r.RequestID, []ToolStatus{Dispatched, Unknown}).Count(&count).Error
		if e != nil {
			return e
		}
		if count != 0 {
			return ErrConflict
		}
		c := ref.Call()
		journal, e := lookup(tx, l.Key, c)
		if e != nil {
			return e
		}
		if !sameCall(journal, c) || journal.Status != Prepared {
			return ErrConflict
		}
		journal.Status = Dispatched
		journal.DispatchEpoch = int64(l.Epoch)
		if e = saveTool(tx, &journal); e != nil {
			return e
		}
		out = ownExecution(r)
		return nil
	})
	if e != nil {
		return ExecutionAdmission{}, e
	}
	return out, nil
}

// ReadCompletedExecution requires an exact explicit origin. The current target
// revision is independent of origin provenance. Host origin/release authorization
// is a separate seam. No codec validation or retention guarantee is provided here.
func (m *Manager) ReadCompletedExecution(ctx context.Context, l Lease, n time.Time, turn string, revision uint64, origin ExecutionAdmission) (CompletedExecution, error) {
	var out CompletedExecution
	e := m.executionFence(ctx, l, n, func(tx *gorm.DB) error {
		if e := executionTarget(tx, l, turn, revision); e != nil {
			return e
		}
		r, e := executionLookup(tx, l, origin)
		if errors.Is(e, ErrConflict) {
			return ErrExecutionUnavailable
		}
		if e != nil {
			return e
		}
		if r.Turn != turn {
			return ErrExecutionUnavailable
		}
		journal, e := lookup(tx, l.Key, origin.Call())
		if errors.Is(e, ErrNotFound) {
			return ErrExecutionUnavailable
		}
		if e != nil {
			return e
		}
		if !sameCall(journal, origin.Call()) || journal.Status != Completed || journal.DispatchEpoch != r.Epoch {
			return ErrExecutionUnavailable
		}
		result := string(journal.Result)
		out = CompletedExecution{open: func() string { return result }}
		return nil
	})
	if e != nil {
		return CompletedExecution{}, e
	}
	return out, nil
}
