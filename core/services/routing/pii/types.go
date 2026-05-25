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
// route_local). Actions are evaluated in this order:
//   - block: short-circuits the request with an error (the middleware
//     returns 400 to the client).
//   - mask: replaces the matched span with ReplacementFor(pattern).
//   - route_local: leaves the text alone but sets a context flag the
//     router (subsystem 2) treats as "this request must stay on a local
//     model" — never crosses the boundary to a cloud proxy backend.
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

	// ActionRouteLocal leaves the text intact but flags the request so
	// the content router will refuse to dispatch it to a cloud proxy
	// backend. Useful when a deployment trusts local models with
	// sensitive data but not external providers.
	ActionRouteLocal Action = "route_local"
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
	Start     int
	End       int
	Pattern   string // matches Pattern.ID
	HashPrefix string // first 8 chars of sha256(matched value); audit-safe
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
// LocalOnly is true iff at least one matched pattern had
// Action=route_local. The router middleware reads this and constrains
// candidate selection.
type Result struct {
	Redacted  string
	Spans     []Span
	Blocked   bool
	LocalOnly bool
}

// Pattern is one configurable rule. Description is shown in the admin
// UI alongside the pattern; the regex itself stays an implementation
// detail (a leak-prone admin showing an SSN regex with a sample value
// in the field is a risk we deliberately design around).
type Pattern struct {
	ID          string
	Description string
	Action      Action
	// Disabled skips the pattern entirely when true — useful for
	// admins who want to keep a regex around (visible in the UI) but
	// turn it off without removing the YAML entry. Default-false so
	// every existing pattern stays active without touching its config.
	Disabled bool
	// MaxMatchLength is the longest possible match in characters. The
	// streaming filter (subsystem 3, follow-up commit) uses this to
	// size its tail buffer. For regex patterns we compute it at
	// compile time from the pattern's structure when possible, or set
	// a conservative upper bound otherwise.
	MaxMatchLength int

	// internal — populated by Compile().
	regex regexpMatcher
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
	CorrelationID string    `json:"correlation_id,omitempty"`
	UserID        string    `json:"user_id,omitempty"`
	Direction     Direction `json:"direction,omitempty"`
	PatternID     string    `json:"pattern_id,omitempty"`
	ByteOffset    int       `json:"byte_offset,omitempty"`
	Length        int       `json:"length,omitempty"`
	HashPrefix    string    `json:"hash_prefix,omitempty"`
	Action        Action    `json:"action,omitempty"`
	CreatedAt     time.Time `json:"created_at"`

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
