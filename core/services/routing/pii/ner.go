package pii

import (
	"context"
	"fmt"
)

// NERDetector is the contract the redactor's encoder/NER tier expects.
// One detector wraps one loaded token-classification model. The
// implementation (e.g. via the transformers gRPC backend) is wired in
// from core/application; this package stays free of core/backend
// imports so the redactor remains unit-testable with a stub detector.
//
// Implementations must honour ctx cancellation — NER round-trips can
// take tens of milliseconds and a client-aborted request should not
// stall the redactor.
type NERDetector interface {
	Detect(ctx context.Context, text string) ([]NEREntity, error)
}

// NEREntity is one detected span. Start/End are byte offsets into the
// text passed to Detect — half-open, addressing text[Start:End]. The
// Group is the entity label (e.g. "PER", "LOC", "EMAIL"); the exact
// vocabulary depends on the model. The redactor's action map keys off
// Group, so admins configure per-label behaviour.
type NEREntity struct {
	Group string
	Start int
	End   int
	Score float32
	// Text is the matched substring as the detector saw it. Carried for
	// debug logging only (the persisted PIIEvent never stores the raw
	// value); the redactor re-slices the original text for masking.
	Text string
}

// NERConfig configures the encoder tier for one redactor invocation.
// Per-request so the same Redactor instance can serve multiple models
// (each with its own NER preferences) without per-model redactor
// instances.
type NERConfig struct {
	// Detector is the loaded model. nil disables the NER tier — the
	// redactor falls back to the regex-only path with no allocation
	// cost.
	Detector NERDetector

	// MinScore is the confidence floor; entities below this are dropped
	// before being merged into the hit list. 0 keeps every result the
	// detector returns.
	MinScore float32

	// EntityActions maps entity_group → Action. Unknown groups (groups
	// the detector returns but the admin didn't configure) use
	// DefaultAction. Empty map + DefaultAction empty = NER detections
	// recorded as audit rows but no redaction applied.
	EntityActions map[string]Action

	// DefaultAction is applied when a detected entity_group has no
	// explicit override. Empty (zero value) means "drop unmatched
	// entities silently" — useful when the model returns a broad
	// taxonomy but the admin only cares about a subset.
	DefaultAction Action

	// Source labels where this detector's hits come from. It becomes the
	// PatternID prefix on events and the [REDACTED:<id>] mask, so neural NER
	// detections (Source "ner") and deterministic pattern-matcher detections
	// (Source "pattern") are told apart in the events log and to the model.
	// Empty defaults to "ner" for backward compatibility.
	Source string
}

// Detector source labels (the PatternID prefix). Kept short and stable —
// they appear in the events log and the [REDACTED:...] mask.
const (
	SourceNER     = "ner"
	SourcePattern = "pattern"
)

// ResolveAction returns the action configured for a detected entity
// group, falling back to DefaultAction. Returns ("", false) when the
// entity should be ignored entirely (no override + no default).
func (c NERConfig) ResolveAction(group string) (Action, bool) {
	if a, ok := c.EntityActions[group]; ok {
		return a, true
	}
	if c.DefaultAction != "" {
		return c.DefaultAction, true
	}
	return "", false
}

// NERConfigFromRaw builds a typed NERConfig from a detector plus the raw
// policy strings carried on a detector model's pii_detection config. An
// empty or invalid default_action becomes ActionMask — the safe-by-default
// policy for a PII filter (a detected entity is masked unless an admin
// downgrades it). Unknown per-entity actions are dropped (and logged by
// validActions). This is the single conversion point the application-layer
// resolver uses, so the detector model's policy reaches the redactor in
// exactly one shape. source labels the detector kind (SourceNER /
// SourcePattern) and becomes the PatternID prefix; empty defaults to
// SourceNER.
func NERConfigFromRaw(detector NERDetector, minScore float32, defaultAction string, entityActions map[string]string, source string) NERConfig {
	if source == "" {
		source = SourceNER
	}
	return NERConfig{
		Detector:      detector,
		MinScore:      minScore,
		DefaultAction: validActionOr(defaultAction, ActionMask),
		EntityActions: validActions(entityActions),
		Source:        source,
	}
}

// patternID returns the synthetic pattern ID that audit rows and masks carry
// for this detector's hits, e.g. "ner:EMAIL" or "pattern:ANTHROPIC_KEY". The
// source prefix keeps neural and deterministic detections distinguishable in
// the events tab and in pattern_id filter queries.
func (c NERConfig) patternID(group string) string {
	source := c.Source
	if source == "" {
		source = SourceNER
	}
	return source + ":" + group
}

// errNERDetector is a NERDetector that always returns the wrapped
// error. Exported via NewErrNERDetector so the application wiring can
// surface "model not loaded" without taking on a fmt-only dependency
// just to format the error.
type errNERDetector struct{ err error }

func (e errNERDetector) Detect(context.Context, string) ([]NEREntity, error) {
	return nil, e.err
}

// NewErrNERDetector returns a detector whose Detect always fails with
// the supplied error. Used by the application-level adapter when the
// configured NER model can't be resolved — the redactor surfaces a
// clear runtime error rather than silently skipping the NER tier.
func NewErrNERDetector(msg string) NERDetector { return errNERDetector{err: fmt.Errorf("%s", msg)} }
