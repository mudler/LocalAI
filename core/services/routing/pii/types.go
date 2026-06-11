// Package pii implements the routing-module PII / sensitive-data filter.
//
// Two tiers are planned (per the routing plan):
//
//  1. Regex tier: cheap, deterministic patterns (email, phone, SSN, credit
//     card with Luhn, IPs, API-key prefixes). Always on by default.
//  2. Encoder NER tier: a HF token-classification model exposed via a new
//     gRPC TokenClassify RPC. Out of scope for this slice — added later.
//
// This file ships tier 1 only. The Pipeline interface is shaped so tier 2
// drops in without changing call sites.
//
// Configuration model: each pattern has an Action (block | mask |
// allow). Actions are evaluated in this order:
//   - block: short-circuits the request with an error (the middleware
//     returns 400 to the client).
//   - mask: replaces the matched span with ReplacementFor(pattern).
//   - allow: detect-and-log only — the span is left intact and a
//     PIIEvent is still recorded, but the text passes through
//     unchanged. Useful to downgrade a pattern's default while keeping
//     it visible in the audit log.
package pii

import "time"

// Action describes what to do when a pattern matches.
type Action string

const (
	// ActionMask replaces the matched span with a placeholder. The
	// default. Lets the request proceed to the backend with the
	// sensitive token removed.
	ActionMask Action = "mask"

	// ActionBlock rejects the entire request. The middleware returns
	// 400 with an error referencing the matched pattern_id (but never
	// the matched value).
	ActionBlock Action = "block"

	// ActionAllow detects and logs the match but leaves the text
	// intact — no masking, no blocking. A PIIEvent is still recorded,
	// so the detection is auditable and forms the basis for surfacing
	// detected-PII labels to the router (a future router-model
	// feature). Use it to downgrade a pattern's default action for a
	// model while keeping the pattern visible.
	ActionAllow Action = "allow"
)

// Direction tags whether a PIIEvent fired on input (request body before
// dispatch) or output (response stream after generation). Stored in the
// PIIEvent record so admins can see which direction PII appeared in.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Span is a half-open byte range [Start, End) within a scanned string.
// Pattern is the rule that matched. Text never holds the matched value
// itself — call sites that need the value (for masking) do their own
// substring slicing; call sites that need to log it strip it via
// HashPrefix.
type Span struct {
	Start      int
	End        int
	Pattern    string  // synthetic detector id, "<source>:<GROUP>" (e.g. "ner:EMAIL", "pattern:ANTHROPIC_KEY")
	HashPrefix string  // first 8 chars of sha256(matched value); audit-safe
	Action     Action  // the action that fired for this span (after merge)
	Score      float32 // detector confidence for the (winning) hit, 0..1
}

// Result is what Redact returns. Redacted is the input string after
// all configured masks were applied. Spans are the original positions
// of every match (in the original input — not the redacted output —
// so admins can see where things were).
//
// Blocked is true iff at least one matched pattern had Action=block;
// the call site must enforce this by returning a 400 / refusing to
// dispatch.
//
// Masked is true iff at least one matched span was replaced with a
// placeholder (Action=mask). Spans with Action=allow are recorded but
// leave Masked false. Lets callers (e.g. the decision oracle)
// distinguish "matched and redacted" from "matched but passed through".
type Result struct {
	Redacted string
	Spans    []Span
	Blocked  bool
	Masked   bool
}

// EventKind classifies a stored audit event. The store is shared by the
// PII filter (its original use), the MITM proxy (connect decisions and
// per-request traffic counters), and — when subsystem 2 lands — the
// content router. Filtering by Kind keeps unrelated event types out of
// each other's UI tabs without splitting storage.
//
// An empty Kind is treated as KindPII so rows written before this field
// existed still classify correctly.
type EventKind string

const (
	KindPII          EventKind = "pii"
	KindProxyConnect EventKind = "proxy_connect"
	KindProxyTraffic EventKind = "proxy_traffic"
	// KindAdmission rows are written by the admission middleware
	// (routing subsystem 5) when a request is rejected because a
	// model's MaxConcurrent ceiling is full. The Host field carries
	// the model name (overloading the existing column rather than
	// adding a new one — admins read it as "the thing that was
	// busy"); StatusCode is 503.
	KindAdmission EventKind = "admission"
)

// Origin labels which surface produced a redaction event, so the events
// log distinguishes an inline chat redaction from a MITM-proxy one and
// from an explicit /api/pii/{analyze,redact} call. It is set on PII
// redaction events only (Kind KindPII); connection/admission events leave
// it empty. An empty Origin on an older row reads as "unknown".
type Origin = string

const (
	OriginMiddleware Origin = "middleware"  // in-band chat/completions PII middleware
	OriginProxy      Origin = "proxy"       // cloud-proxy MITM input path
	OriginAnalyzeAPI Origin = "pii_analyze" // POST /api/pii/analyze
	OriginRedactAPI  Origin = "pii_redact"  // POST /api/pii/redact
)

// PIIEvent is the persisted record. The Hash field is the first 8 chars
// of sha256(matched value) — enough to deduplicate "is this the same
// thing as last time" without ever storing the value itself.
//
// Proxy-event fields (Host, Intercepted, Bytes*, StatusCode, DurationMS)
// are only set when Kind is KindProxyConnect or KindProxyTraffic. They
// hold connection-level metadata for audit and basic diagnostics — never
// request bodies. Use the API/backend traces to inspect contents.
type PIIEvent struct {
	ID            string    `json:"id"`
	Kind          EventKind `json:"kind,omitempty"`
	Origin        Origin    `json:"origin,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	UserID        string    `json:"user_id,omitempty"`
	Direction     Direction `json:"direction,omitempty"`
	PatternID     string    `json:"pattern_id,omitempty"`
	ByteOffset    int       `json:"byte_offset,omitempty"`
	Length        int       `json:"length,omitempty"`
	HashPrefix    string    `json:"hash_prefix,omitempty"`
	Action        Action    `json:"action,omitempty"`
	// Score is the detector confidence (0..1) for an NER PII hit. Metadata
	// only — never the matched value. Lets admins see how sure the model was
	// about a (possibly false-positive) detection without re-running it.
	Score     float32   `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	Host          string `json:"host,omitempty"`
	Intercepted   *bool  `json:"intercepted,omitempty"`
	BytesSent     int64  `json:"bytes_sent,omitempty"`
	BytesReceived int64  `json:"bytes_received,omitempty"`
	StatusCode    int    `json:"status_code,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
}

// ResolvedKind returns the event's Kind, treating an empty value as
// KindPII for rows written before Kind existed.
func (e PIIEvent) ResolvedKind() EventKind {
	if e.Kind == "" {
		return KindPII
	}
	return e.Kind
}
