package reasoning

import "strings"

// ReasoningExtractor tracks streaming reasoning extraction state, computing
// incremental deltas so callers don't need to duplicate the ~30-line
// accumulated-content / last-emitted tracking logic.
//
// Usage:
//
//	extractor := NewReasoningExtractor(thinkingStartToken, cfg)
//	// In your streaming token callback:
//	reasoningDelta, contentDelta := extractor.ProcessToken(token)
//	// After streaming completes:
//	finalReasoning := extractor.Reasoning()
//	finalContent := extractor.CleanedContent()
type ReasoningExtractor struct {
	thinkingStartToken string
	config             Config
	accumulated        string
	lastReasoning      string
	lastCleaned        string
	suppressReasoning  bool

	// ChatDelta reasoning accumulator — used by ProcessChatDeltaReasoning
	// to strip reasoning tags (e.g. <|channel>thought, <channel|>) that
	// the C++ autoparser includes in reasoning_content deltas.
	cdReasoningAccum        string
	cdLastStrippedReasoning string
}

// NewReasoningExtractor creates a new extractor for the given thinking token and config.
func NewReasoningExtractor(thinkingStartToken string, cfg Config) *ReasoningExtractor {
	return &ReasoningExtractor{
		thinkingStartToken: thinkingStartToken,
		config:             cfg,
	}
}

// ProcessToken processes a new streaming token and returns the reasoning
// and content deltas (the new portions not yet emitted).
func (e *ReasoningExtractor) ProcessToken(token string) (reasoningDelta, contentDelta string) {
	e.accumulated += token
	currentReasoning, cleanedContent := ExtractReasoningWithConfig(e.accumulated, e.thinkingStartToken, e.config)

	// Calculate reasoning delta
	if currentReasoning != e.lastReasoning {
		if len(currentReasoning) > len(e.lastReasoning) && strings.HasPrefix(currentReasoning, e.lastReasoning) {
			reasoningDelta = currentReasoning[len(e.lastReasoning):]
		} else if currentReasoning != "" {
			// Reasoning changed in a non-append way, emit the full current reasoning
			reasoningDelta = currentReasoning
		}
		e.lastReasoning = currentReasoning
	}

	// Calculate content delta
	if len(cleanedContent) > len(e.lastCleaned) && strings.HasPrefix(cleanedContent, e.lastCleaned) {
		contentDelta = cleanedContent[len(e.lastCleaned):]
		e.lastCleaned = cleanedContent
	} else if cleanedContent != e.lastCleaned {
		contentDelta = cleanedContent
		e.lastCleaned = cleanedContent
	}

	if e.suppressReasoning {
		reasoningDelta = ""
	}

	return reasoningDelta, contentDelta
}

// ProcessChatDeltaReasoning accumulates raw reasoning text from C++ autoparser
// ChatDeltas, strips any embedded reasoning tags (e.g. <|channel>thought …
// <channel|> for Gemma 4), and returns only the new stripped delta.
// This prevents tag tokens from leaking into the reasoning field of SSE chunks.
//
// When the C++ autoparser already strips tags (e.g. <think> models), the text
// passes through unchanged — ExtractReasoning finds no tags so we use the raw text.
func (e *ReasoningExtractor) ProcessChatDeltaReasoning(rawDelta string) string {
	if rawDelta == "" {
		return ""
	}
	e.cdReasoningAccum += rawDelta

	// Try to strip reasoning tags from accumulated ChatDelta reasoning.
	stripped, cleaned := ExtractReasoning(e.cdReasoningAccum, &e.config)

	if stripped == "" {
		// ExtractReasoning found no reasoning content. This happens when:
		// a) A complete start tag was found but has no content after it yet
		//    (cleaned == "" because everything is inside the unclosed tag)
		//    → keep buffering
		// b) We're accumulating a partial multi-token start tag
		//    (e.g. "<|channel>" before "thought" arrives)
		//    → keep buffering
		// c) No tags at all — C++ already stripped them
		//    → pass through the raw text as-is
		if cleaned == "" && strings.TrimSpace(e.cdReasoningAccum) != "" {
			// Case (a): tag found, unclosed, no content yet
			stripped = ""
		} else if e.thinkingStartToken != "" &&
			len(strings.TrimSpace(e.cdReasoningAccum)) < len(e.thinkingStartToken) &&
			strings.HasPrefix(e.thinkingStartToken, strings.TrimSpace(e.cdReasoningAccum)) {
			// Case (b): partial start tag prefix
			stripped = ""
		} else {
			// Case (c): no tags found — text is already clean from C++
			stripped = e.cdReasoningAccum
		}
	}

	// Compute delta from stripped reasoning
	var delta string
	if len(stripped) > len(e.cdLastStrippedReasoning) && strings.HasPrefix(stripped, e.cdLastStrippedReasoning) {
		delta = stripped[len(e.cdLastStrippedReasoning):]
	} else if stripped != e.cdLastStrippedReasoning && stripped != "" {
		delta = stripped
	}
	e.cdLastStrippedReasoning = stripped

	if e.suppressReasoning {
		return ""
	}
	return delta
}

// Reasoning returns the total accumulated reasoning after streaming.
func (e *ReasoningExtractor) Reasoning() string {
	return e.lastReasoning
}

// CleanedContent returns the total accumulated content (reasoning stripped).
func (e *ReasoningExtractor) CleanedContent() string {
	return e.lastCleaned
}

// Accumulated returns the total raw accumulated content.
func (e *ReasoningExtractor) Accumulated() string {
	return e.accumulated
}

// Reset clears the extractor state for reuse.
func (e *ReasoningExtractor) Reset() {
	e.accumulated = ""
	e.lastReasoning = ""
	e.lastCleaned = ""
	e.cdReasoningAccum = ""
	e.cdLastStrippedReasoning = ""
}

// ResetAndSuppressReasoning clears state and suppresses future reasoning deltas.
// ProcessToken() still extracts reasoning internally (CleanedContent works),
// but returns empty reasoningDelta — reasoning is not surfaced to the caller.
// This is used on retry after streaming: reasoning from the first attempt was
// already sent to the client; re-streaming it would cause duplicates.
func (e *ReasoningExtractor) ResetAndSuppressReasoning() {
	e.accumulated = ""
	e.lastReasoning = ""
	e.lastCleaned = ""
	e.cdReasoningAccum = ""
	e.cdLastStrippedReasoning = ""
	e.suppressReasoning = true
}

// Suppressed returns whether reasoning delta suppression is active.
func (e *ReasoningExtractor) Suppressed() bool {
	return e.suppressReasoning
}
