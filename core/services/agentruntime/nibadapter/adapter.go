// SPDX-License-Identifier: MIT
package nibadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/nib/chat"
	"github.com/mudler/nib/types"
)

// Capabilities describes this adapter's tested surface, not all nib features.
// Approvals means an optional approval callback, NOT mandatory interception.
// HelperEvents does not imply authorized subtask execution.
type Capabilities struct {
	Version                                                            string
	CheckpointRestore, HistoryRestore, ArtifactRestore, PruningRestore bool
	Streaming, EventProjection, MandatoryPolicyInterception            bool
	Approvals, Questions, CompactionEvents, HelperEvents               bool
	Subtasks, GoalHooks, LoopHooks                                     bool
}

func PinnedCapabilities() Capabilities {
	return Capabilities{Version: "v0.12.1", CheckpointRestore: true, HistoryRestore: true, ArtifactRestore: true, PruningRestore: true, Streaming: true, EventProjection: true, Approvals: true, Questions: true, CompactionEvents: true, HelperEvents: true}
}

const CheckpointVersion = "nib-v0.12.1/checkpoint-1"

// Checkpoint contains independent JSON buffers in nib's native wire types.
// These are sensitive raw runtime data, not an authorized visible transcript.
type Checkpoint struct {
	Version   string `json:"version"`
	Context   []byte `json:"context"`
	History   []byte `json:"history"`
	Artifacts []byte `json:"artifacts"`
	Pruning   []byte `json:"pruning"`
}

var (
	ErrNotQuiescent               = errors.New("checkpoint export requires a quiescent host boundary")
	ErrPolicyRequired             = errors.New("mandatory host tool policy is required")
	ErrMandatoryPolicyUnsupported = errors.New("nib v0.12.1 lacks mandatory host policy interception; secure tool integration is blocked")
)

func EncodeCheckpoint(cp Checkpoint) ([]byte, error) {
	if _, err := RestoreConfig(types.Config{}, cp); err != nil {
		return nil, err
	}
	return json.Marshal(cp)
}
func DecodeCheckpoint(data []byte) (Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, err
	}
	if _, err := RestoreConfig(types.Config{}, cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// RestoreConfig validates all sections before returning construction inputs.
// Unmarshalling creates deep independent ownership, including tool-call slices.
// Callers must validate their remaining config and must not connect tools while
// RequireSecureTools reports the upstream blocker.
func RestoreConfig(cfg types.Config, cp Checkpoint) (types.Config, error) {
	if cp.Version != CheckpointVersion {
		return types.Config{}, fmt.Errorf("unsupported checkpoint version %q", cp.Version)
	}
	// Decode into fresh fields: json.Unmarshal otherwise reuses slices and maps
	// from the caller's shallow-copied config and can mutate its backing data.
	cfg.InitialContext = nil
	cfg.InitialHistory = nil
	cfg.InitialArtifacts = nil
	cfg.InitialPruning = types.PruningState{}
	sections := []struct {
		name    string
		data    []byte
		target  any
		opening byte
	}{
		{"context", cp.Context, &cfg.InitialContext, '['}, {"history", cp.History, &cfg.InitialHistory, '['},
		{"artifacts", cp.Artifacts, &cfg.InitialArtifacts, '['}, {"pruning", cp.Pruning, &cfg.InitialPruning, '{'},
	}
	for _, s := range sections {
		data := bytes.TrimSpace(s.data)
		if len(data) == 0 || data[0] != s.opening {
			return types.Config{}, fmt.Errorf("invalid %s section", s.name)
		}
		if err := json.Unmarshal(data, s.target); err != nil {
			return types.Config{}, fmt.Errorf("invalid %s: %w", s.name, err)
		}
	}
	// nib falls back to display history and skips pruning restoration when
	// InitialContext is empty. Reject states it cannot restore faithfully.
	if len(cfg.InitialContext) == 0 && (len(cfg.InitialHistory) > 0 || len(cfg.InitialPruning.Pruned) > 0 || len(cfg.InitialPruning.Compressed) > 0 || cfg.InitialPruning.CompressBand != 0) {
		return types.Config{}, errors.New("unsupported checkpoint state: empty context with history or pruning")
	}
	return cfg, nil
}

// ExportCheckpoint must run only after the host has stopped all session mutation
// (including helpers). quiescent is a host attestation, not a lock or detection
// mechanism. The upstream exports are NOT atomic across the four sections.
func ExportCheckpoint(s *chat.Session, quiescent bool) (Checkpoint, error) {
	if !quiescent {
		return Checkpoint{}, ErrNotQuiescent
	}
	if s == nil {
		return Checkpoint{}, errors.New("nil nib session")
	}
	cp := Checkpoint{Version: CheckpointVersion}
	for _, part := range []struct {
		value any
		dest  *[]byte
	}{
		{s.ExportContext(), &cp.Context}, {s.ExportHistory(), &cp.History}, {s.ExportArtifacts(), &cp.Artifacts}, {s.ExportPruning(), &cp.Pruning},
	} {
		// nil upstream slices encode as null; canonical empty arrays keep the
		// checkpoint unambiguous rather than falling back to display history.
		data, err := json.Marshal(part.value)
		if err != nil {
			return Checkpoint{}, err
		}
		if bytes.Equal(data, []byte("null")) {
			data = []byte("[]")
		}
		*part.dest = data
	}
	if _, err := RestoreConfig(types.Config{}, cp); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

// Identity must come from the host, never model or browser authority. It is
// copied into a new callback set per attempt. nib callbacks are session-scoped:
// do not reuse this set for another turn or rebind a mutable "current turn".
// Host consumers must fence late events against their durable lease/attempt.
type Identity struct {
	Principal, Conversation, Turn string
	Attempt, Epoch                uint64
}
type Policy func(Identity, chat.ToolCallRequest) error

// RequireSecureTools always blocks on this pin, even with a non-nil policy.
// OnToolCall is an optional prompt hook; it cannot implement mandatory ACLs.
func RequireSecureTools(policy Policy) error {
	if policy == nil {
		return ErrPolicyRequired
	}
	return ErrMandatoryPolicyUnsupported
}

type EventKind string

const (
	Text          EventKind = "text"
	Reasoning     EventKind = "reasoning"
	Status        EventKind = "status"
	Failure       EventKind = "error"
	Stream        EventKind = "stream"
	ToolStarted   EventKind = "tool_start"
	ToolFinished  EventKind = "tool_result"
	Helper        EventKind = "helper"
	HelperTitle   EventKind = "helper_title"
	Compacted     EventKind = "compacted"
	CompactFailed EventKind = "compact_failed"
	Pruned        EventKind = "pruned"
	Parked        EventKind = "parked"
	Resumed       EventKind = "resumed"
)

type Event struct {
	Identity                           Identity
	Kind                               EventKind
	Text, ToolName, Arguments, AgentID string
	Err                                error
	Stream                             chat.StreamEvent
	Helper                             chat.AgentEvent
	Before, After                      int
}

// NewCallbacks builds a bounded projection/optional-prompt adapter. It does not
// construct or authorize a tool-enabled session. sink and policy must be safe
// for concurrent invocation; callbacks never hold a global mutable identity.
// Streaming and step callbacks can overlap: the host must not concatenate all
// projections into a second transcript or infer token usage from these events.
func NewCallbacks(id Identity, policy Policy, sink func(Event), ask func(Identity, chat.AskRequest) string) (chat.Callbacks, error) {
	if policy == nil {
		return chat.Callbacks{}, ErrPolicyRequired
	}
	if id.Principal == "" || id.Conversation == "" || id.Turn == "" || id.Attempt == 0 || id.Epoch == 0 || sink == nil {
		return chat.Callbacks{}, errors.New("invalid callback identity or sink")
	}
	emit := func(e Event) { e.Identity = id; sink(e) }
	text := func(k EventKind) func(string) { return func(s string) { emit(Event{Kind: k, Text: s}) } }
	return chat.Callbacks{
		OnStatus: text(Status), OnReasoning: text(Reasoning), OnResponse: text(Text), OnStepContent: text(Text),
		OnError:  func(err error) { emit(Event{Kind: Failure, Err: err}) },
		OnStream: func(ev chat.StreamEvent) { emit(Event{Kind: Stream, Stream: ev}) },
		OnToolCall: func(req chat.ToolCallRequest) chat.ToolCallResponse {
			req.ExternalSources = slices.Clone(req.ExternalSources)
			if err := policy(id, req); err != nil {
				return chat.ToolCallResponse{Adjustment: err.Error()}
			}
			// Never return AlwaysAllow, AlwaysPrefix, or AllowAllTurn grants.
			return chat.ToolCallResponse{Approved: true}
		},
		OnToolStart: func(ts chat.ToolStart) { emit(Event{Kind: ToolStarted, ToolName: ts.Name, Arguments: ts.Arguments}) },
		OnToolResult: func(res chat.ToolResult) {
			emit(Event{Kind: ToolFinished, ToolName: res.Name, Arguments: res.Arguments, Text: res.Result, AgentID: res.AgentID})
		},
		OnAgentEvent:    func(ev chat.AgentEvent) { emit(Event{Kind: Helper, Helper: ev}) },
		OnAgentTitle:    func(agent, title string) { emit(Event{Kind: HelperTitle, AgentID: agent, Text: title}) },
		OnCompactDone:   func(before, after int) { emit(Event{Kind: Compacted, Before: before, After: after}) },
		OnCompactFailed: func(err error) { emit(Event{Kind: CompactFailed, Err: err}) },
		OnPruneDone:     func(results, freed int) { emit(Event{Kind: Pruned, Before: results, After: freed}) },
		OnParked:        text(Parked), OnResumed: func() { emit(Event{Kind: Resumed}) },
		OnAskUser: func(req chat.AskRequest) string {
			if ask == nil {
				return "Question handler unavailable"
			}
			req.Options = slices.Clone(req.Options)
			return ask(id, req)
		},
	}, nil
}
