package schema

// PIIAnalyzeRequest is the body for POST /api/pii/analyze and
// POST /api/pii/redact. The two endpoints share a request shape; only the
// response differs (analyze never mutates text, redact applies policy).
//
// Detector selection is one of two ways:
//   - Detectors: explicit detector-model names (the primary path).
//   - Model: a consuming model name whose effective PII policy is used when
//     Detectors is empty — "what would this model do with this text?". The
//     policy resolves exactly as for the inline middleware: the model's own
//     pii.detectors, else the instance-wide pii_default_detectors, and
//     nothing when the model has PII disabled.
//
// One of the two must resolve to at least one detector, else the call is a
// 400 — including a PII-enabled model with no detectors anywhere: the
// middleware would scan nothing, and saying so loudly beats implying a clean
// scan. The detection policy (mask/block/allow per entity group, min score)
// lives on each detector model's own pii_detection block, exactly as for the
// inline chat middleware.
type PIIAnalyzeRequest struct {
	// Text is the string to scan. Bounded only by the server's global HTTP
	// body limit.
	Text string `json:"text"`
	// Detectors names the detector models to run (NER and/or pattern). Takes
	// precedence over Model.
	Detectors []string `json:"detectors,omitempty"`
	// Model is a consuming model whose effective PII policy (own
	// pii.detectors, else the instance default detectors; PII must be
	// enabled) is used when Detectors is empty.
	Model string `json:"model,omitempty"`
	// Reveal includes the per-entity hash_prefix in the response. Honoured
	// only for admin callers; ignored otherwise. The raw matched value is
	// never returned regardless.
	Reveal bool `json:"reveal,omitempty"`
}

// PIIEntity is one detected span. EntityType is the detector group (e.g.
// "EMAIL", "ANTHROPIC_KEY"); Source is the detector tier that produced it
// ("ner" or "pattern"). Start/End are half-open byte offsets into the request
// Text. Action is the policy action that fired after the overlap merge
// (mask | block | allow). HashPrefix is present only for admin + reveal.
type PIIEntity struct {
	EntityType string  `json:"entity_type"`
	Source     string  `json:"source"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float32 `json:"score"`
	Action     string  `json:"action"`
	HashPrefix string  `json:"hash_prefix,omitempty"`
}

// PIIAnalyzeResponse is returned by POST /api/pii/analyze (always 200). It
// reports detections without mutating the text. Blocked is true when at
// least one entity's action is block — i.e. the redact endpoint would reject
// this text.
type PIIAnalyzeResponse struct {
	Entities      []PIIEntity `json:"entities"`
	Blocked       bool        `json:"blocked"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}

// PIIRedactResponse is returned by POST /api/pii/redact when nothing blocks
// (200). RedactedText is the input with masked spans replaced; Masked is true
// when at least one span was replaced. When a block action fires the endpoint
// returns 400 instead (with an error of type "pii_blocked" and the offending
// entities), never a redacted body.
type PIIRedactResponse struct {
	RedactedText  string      `json:"redacted_text"`
	Entities      []PIIEntity `json:"entities"`
	Blocked       bool        `json:"blocked"`
	Masked        bool        `json:"masked"`
	CorrelationID string      `json:"correlation_id,omitempty"`
}
