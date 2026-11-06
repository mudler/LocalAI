// SPDX-License-Identifier: MIT
package music

import (
	"context"
	"errors"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/protobuf/proto"
)

type Operation uint8

const (
	Transcription Operation = iota + 1
	Generation
	Cover
)

type Stage uint8

const (
	TranscriptionStage Stage = iota + 1
	GenerationStage
)

type GenerationInput struct {
	Style, Lyrics string
	ABC           *string
	Params        *pb.MusicGenerationParams
}

func (g GenerationInput) snapshot() GenerationInput {
	if g.ABC != nil {
		a := *g.ABC
		g.ABC = &a
	}
	if g.Params != nil {
		g.Params = proto.Clone(g.Params).(*pb.MusicGenerationParams)
	}
	return g
}

// Selection is evidence from a trusted resolver, never HTTP request fields.
type Selection struct {
	CanonicalModel, LoadedIdentity                    string
	Authorized, Capable, IdentityMatches, ContextFits bool
}

func (s Selection) facts() facts {
	return facts{s.Authorized, s.Capable, s.IdentityMatches && s.LoadedIdentity == s.CanonicalModel, s.ContextFits, s.CanonicalModel}
}

// CapabilityIssuer is held by the trusted Storage adapter, never the request.
// Creating another issuer confers no authority over the configured adapter.
// Resource keys are adapter-private lookups; token identity, not key text,
// authorizes lifecycle operations. Issuance must follow owned allocation.
type CapabilityIssuer struct {
	mu      sync.Mutex
	records map[*capability]capabilityRecord
	claims  map[string]bool
}
type capability struct{ identity byte }
type capabilityRecord struct {
	request, key string
	stage        Stage
	kind         uint8
}
type OwnedInput struct{ token *capability }
type Candidate struct{ token *capability }
type PreparedOutput struct{ token *capability }

func NewCapabilityIssuer() *CapabilityIssuer {
	return &CapabilityIssuer{records: map[*capability]capabilityRecord{}}
}
func (i *CapabilityIssuer) issue(request, key string, stage Stage, kind uint8) *capability {
	if i == nil || request == "" || key == "" {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	t := &capability{}
	i.records[t] = capabilityRecord{request, key, stage, kind}
	return t
}
func (i *CapabilityIssuer) Input(request, key string) OwnedInput {
	return OwnedInput{i.issue(request, key, 0, 1)}
}
func (i *CapabilityIssuer) Candidate(request string, stage Stage, key string) Candidate {
	return Candidate{i.issue(request, key, stage, 2)}
}
func (i *CapabilityIssuer) Prepared(request, key string) PreparedOutput {
	return PreparedOutput{i.issue(request, key, GenerationStage, 3)}
}
func (i *CapabilityIssuer) record(t *capability) (capabilityRecord, bool) {
	if i == nil || t == nil {
		return capabilityRecord{}, false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	v, ok := i.records[t]
	return v, ok
}
func (i *CapabilityIssuer) owns(t *capability, request string, stage Stage, kind uint8) bool {
	v, ok := i.record(t)
	return ok && v.request == request && v.kind == kind && v.stage == stage
}

// claim validates the source and reserves the request in one critical section.
// Failed claims never consume or retire another owner's capabilities.
func (i *CapabilityIssuer) claim(request string, source OwnedInput, required bool) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.claims[request] {
		return false
	}
	if required || source.token != nil {
		v, ok := i.records[source.token]
		if !ok || v.request != request || v.kind != 1 {
			return false
		}
	}
	if i.claims == nil {
		i.claims = map[string]bool{}
	}
	i.claims[request] = true
	return true
}
func (i *CapabilityIssuer) unclaim(request string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.claims, request)
}
func (i *CapabilityIssuer) retire(t *capability) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.records, t)
}

// Lookup methods are usable only with the issuing storage adapter's issuer.
func (i *CapabilityIssuer) InputKey(h OwnedInput) (string, bool) {
	v, ok := i.record(h.token)
	return v.key, ok && v.kind == 1
}
func (i *CapabilityIssuer) CandidateKey(h Candidate) (string, bool) {
	v, ok := i.record(h.token)
	return v.key, ok && v.kind == 2
}
func (i *CapabilityIssuer) PreparedKey(h PreparedOutput) (string, bool) {
	v, ok := i.record(h.token)
	return v.key, ok && v.kind == 3
}

// CleanupTask separates an attempt diagnostic from definitive resource release.
// Only an explicit nil channel value acknowledges release. Close, cancellation,
// or errors retain the owner; the dependency can send a later definitive ack.
type CleanupTask struct {
	AttemptError error
	Released     <-chan error
}

func (t CleanupTask) Wait() error {
	err := t.AttemptError
	for ack := range t.Released {
		if ack == nil {
			return err
		}
		err = errors.Join(err, ack)
	}
	select {}
}

// ValidatedRequest must be constructed by an authorization/preflight service.
// Runner is private infrastructure; this is not a user-deserializable DTO.
type ValidatedRequest struct {
	ID, Principal                             string
	Operation                                 Operation
	Transcription, Generation                 Selection
	GenerationInput                           GenerationInput
	Source                                    OwnedInput
	Deadline                                  time.Time
	Feature, Quota, InputsValid, StorageReady bool
}
type StageRequest struct {
	RequestID, EffectID string
	Stage               Stage
	Selection           Selection
	Source              OwnedInput
	Generation          GenerationInput
}
type StageOutcome struct {
	ABC       *string
	Candidate Candidate
	Valid     bool
	Err       error
}

type StageExecution interface {
	// Outcome delivers exactly one final outcome; Settled guarantees that no
	// further worker-owned resources can be created. Neither channel close is ack.
	Outcome() <-chan StageOutcome
	// A nil value explicitly acknowledges native quiescence AND lane release.
	// Closing the channel, returning an outcome, or cancelling ctx is not an ack.
	Settled() <-chan error
	Stop()
}
type StageLease interface {
	Start(context.Context, StageRequest) (StageExecution, error)
	// Release is used only before Start (or when Start fails without execution).
	// It returns nil only after actual lane release, never on transport failure.
	Release(context.Context) CleanupTask
}
type Backend interface {
	// Admit is an immediate, non-queueing capacity decision.
	Admit(context.Context, StageRequest) (StageLease, error)
	CheckGeneration(context.Context, ValidatedRequest) (Selection, error)
}
type Storage interface {
	Issuer() *CapabilityIssuer
	CleanInput(context.Context, string, OwnedInput) CleanupTask
	// FetchValidate performs LOCAL validation/finalization only. The staging
	// adapter already fetched exactly once into the frontend private candidate.
	FetchValidate(context.Context, string, Candidate) (Candidate, error)
	Prepare(context.Context, string, Candidate) (PreparedOutput, error)
	// Revoke installs a persistent tombstone, including against late Prepare.
	Revoke(context.Context, string) CleanupTask
	CleanArtifact(context.Context, string, Candidate, PreparedOutput) CleanupTask
}
type Publisher interface {
	// Commit runs synchronously under Runner's serialized terminal authority.
	Commit(context.Context, string, PreparedOutput) (string, error)
}
type Result struct {
	RequestID                       string
	ResolvedModels                  map[Stage]string
	ABC                             *string
	ABCSource, OutputID, StageError string
}

var ErrPreflight = errors.New("music preflight denied")
var ErrSettlementUnsupported = errors.New("music remote settlement acknowledgement unsupported")

// Runner's collaborators must obey the explicit ownership/ack contracts above.
// Trace runs on the event loop and must not block or reenter Runner.
type Runner struct {
	Backend   Backend
	Storage   Storage
	Publisher Publisher
	Trace     func(Transition)
}
