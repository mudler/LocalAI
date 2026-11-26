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
	suppressReasoning  bool

	accumulated strings.Builder
	// tokens holds the extraction of the accumulated tokens, with the thinking
	// start token prepended when the prompt opened the block. Nil until the
	// first token.
	tokens *incrementalExtraction
	// prefillEnd is where the prepended thinking start token ends in the text
	// of tokens, 0 when none is prepended. The fast path needs the bytes it
	// checks for markers to be accumulated bytes.
	prefillEnd int
	// prefillSettled tells whether more tokens can still change the decision
	// to prepend the start token in a way that the marker check does not see.
	prefillSettled bool

	// ChatDelta reasoning accumulator — used by ProcessChatDeltaReasoning
	// to strip reasoning tags (e.g. <|channel>thought, <channel|>) that
	// the C++ autoparser includes in reasoning_content deltas.
	chatDeltas              *incrementalExtraction
	cdLastStrippedReasoning string
	cdLastMode              strippedMode
}

// strippedMode says where the stripped ChatDelta reasoning comes from.
type strippedMode int

const (
	strippedNone      strippedMode = iota // nothing yet, or a tag is still arriving
	strippedReasoning                     // the reasoning inside tags
	strippedRaw                           // the raw text, which has no tags
)

// NewReasoningExtractor creates a new extractor for the given thinking token and config.
func NewReasoningExtractor(thinkingStartToken string, cfg Config) *ReasoningExtractor {
	return &ReasoningExtractor{
		thinkingStartToken: thinkingStartToken,
		config:             cfg,
	}
}

func (e *ReasoningExtractor) reasoningEnabled() bool {
	return e.config.DisableReasoning == nil || !*e.config.DisableReasoning
}

func (e *ReasoningExtractor) prefillEnabled() bool {
	return e.thinkingStartToken != "" &&
		(e.config.DisableReasoningTagPrefill == nil || !*e.config.DisableReasoningTagPrefill)
}

func (e *ReasoningExtractor) stripReasoningOnly() bool {
	return e.config.StripReasoningOnly != nil && *e.config.StripReasoningOnly
}

// ProcessToken processes a new streaming token and returns the reasoning
// and content deltas (the new portions not yet emitted).
//
// The result is the one of ExtractReasoningWithConfig on all the tokens so
// far, but the work per token does not grow with the output: see
// incrementalExtraction.
func (e *ReasoningExtractor) ProcessToken(token string) (reasoningDelta, contentDelta string) {
	if token == "" && (e.tokens != nil || !e.reasoningEnabled()) {
		// The same text gives the same result, so nothing is new. Before the
		// first parse an empty token still counts: a prepended start token
		// that is not a known tag is content.
		return "", ""
	}
	e.accumulated.WriteString(token)

	if !e.reasoningEnabled() {
		// The cleaned content is the accumulated text itself.
		return "", token
	}

	if e.tokens != nil && e.prefillSettled &&
		len(e.tokens.Text())-(e.tokens.maxMarker-1) >= e.prefillEnd {
		if r, c, ok := e.tokens.tryAppend(token); ok {
			if e.stripReasoningOnly() || e.suppressReasoning {
				r = ""
			}
			return r, c
		}
	}

	lastReasoning, lastCleaned := e.Reasoning(), e.CleanedContent()
	e.parseTokens()
	currentReasoning, cleanedContent := e.Reasoning(), e.CleanedContent()

	// Calculate reasoning delta
	if currentReasoning != lastReasoning {
		if len(currentReasoning) > len(lastReasoning) && strings.HasPrefix(currentReasoning, lastReasoning) {
			reasoningDelta = currentReasoning[len(lastReasoning):]
		} else if currentReasoning != "" {
			// Reasoning changed in a non-append way, emit the full current reasoning
			reasoningDelta = currentReasoning
		}
	}

	// Calculate content delta
	if len(cleanedContent) > len(lastCleaned) && strings.HasPrefix(cleanedContent, lastCleaned) {
		contentDelta = cleanedContent[len(lastCleaned):]
	} else if cleanedContent != lastCleaned {
		contentDelta = cleanedContent
	}

	if e.suppressReasoning {
		reasoningDelta = ""
	}

	return reasoningDelta, contentDelta
}

// parseTokens parses the accumulated tokens from the start, as
// ExtractReasoningWithConfig does.
func (e *ReasoningExtractor) parseTokens() {
	accumulated := e.accumulated.String()
	text := accumulated
	e.prefillEnd = 0
	e.prefillSettled = true
	if e.prefillEnabled() {
		text = PrependThinkingTokenIfNeeded(accumulated, e.thinkingStartToken)
		if len(text) != len(accumulated) {
			// The token goes after the leading whitespace.
			e.prefillEnd = len(text) - len(strings.TrimLeft(accumulated, " \t\n\r"))
		}
		// While the text is blank or still a prefix of the start token, one
		// more token can flip the decision. Once it is neither, only a new
		// occurrence of the start token can, and the marker check sees it.
		trimmed := strings.TrimLeft(accumulated, " \t\n\r")
		e.prefillSettled = trimmed != "" && !strings.HasPrefix(e.thinkingStartToken, trimmed)
	}
	if e.tokens == nil {
		e.tokens = newIncrementalExtraction(&e.config, e.thinkingStartToken)
	}
	e.tokens.reset(text)
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
	if e.chatDeltas == nil {
		e.chatDeltas = newIncrementalExtraction(&e.config)
	}
	reasoningGrowth, _, appended := e.chatDeltas.tryAppend(rawDelta)
	if !appended {
		e.chatDeltas.reset(e.chatDeltas.Text() + rawDelta)
	}
	accum := e.chatDeltas.Text()

	// Try to strip reasoning tags from accumulated ChatDelta reasoning.
	stripped := e.chatDeltas.Reasoning()
	mode := strippedReasoning
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
		trimmed := strings.TrimSpace(accum)
		if e.chatDeltas.Cleaned() == "" && trimmed != "" {
			// Case (a): tag found, unclosed, no content yet
			mode = strippedNone
		} else if e.thinkingStartToken != "" &&
			len(trimmed) < len(e.thinkingStartToken) &&
			strings.HasPrefix(e.thinkingStartToken, trimmed) {
			// Case (b): partial start tag prefix
			mode = strippedNone
		} else {
			// Case (c): no tags found — text is already clean from C++
			stripped = accum
			mode = strippedRaw
		}
	}

	// Compute delta from stripped reasoning. While the source stays the same
	// and the parse made no new decision, the stripped text grew by what was
	// appended, and comparing whole strings would cost the output length again.
	var delta string
	switch {
	case appended && mode == e.cdLastMode && mode == strippedReasoning:
		delta = reasoningGrowth
	case appended && mode == e.cdLastMode && mode == strippedRaw:
		delta = rawDelta
	case len(stripped) > len(e.cdLastStrippedReasoning) && strings.HasPrefix(stripped, e.cdLastStrippedReasoning):
		delta = stripped[len(e.cdLastStrippedReasoning):]
	case stripped != e.cdLastStrippedReasoning && stripped != "":
		delta = stripped
	}
	e.cdLastStrippedReasoning = stripped
	e.cdLastMode = mode

	if e.suppressReasoning {
		return ""
	}
	return delta
}

// Reasoning returns the total accumulated reasoning after streaming.
func (e *ReasoningExtractor) Reasoning() string {
	if e.tokens == nil || !e.reasoningEnabled() || e.stripReasoningOnly() {
		return ""
	}
	return e.tokens.Reasoning()
}

// CleanedContent returns the total accumulated content (reasoning stripped).
func (e *ReasoningExtractor) CleanedContent() string {
	if !e.reasoningEnabled() {
		return e.accumulated.String()
	}
	if e.tokens == nil {
		return ""
	}
	return e.tokens.Cleaned()
}

// Accumulated returns the total raw accumulated content.
func (e *ReasoningExtractor) Accumulated() string {
	return e.accumulated.String()
}

// Reset clears the extractor state for reuse.
func (e *ReasoningExtractor) Reset() {
	e.accumulated.Reset()
	e.tokens = nil
	e.prefillEnd = 0
	e.prefillSettled = false
	e.chatDeltas = nil
	e.cdLastStrippedReasoning = ""
	e.cdLastMode = strippedNone
}

// ResetAndSuppressReasoning clears state and suppresses future reasoning deltas.
// ProcessToken() still extracts reasoning internally (CleanedContent works),
// but returns empty reasoningDelta — reasoning is not surfaced to the caller.
// This is used on retry after streaming: reasoning from the first attempt was
// already sent to the client; re-streaming it would cause duplicates.
func (e *ReasoningExtractor) ResetAndSuppressReasoning() {
	e.Reset()
	e.suppressReasoning = true
}

// Suppressed returns whether reasoning delta suppression is active.
func (e *ReasoningExtractor) Suppressed() bool {
	return e.suppressReasoning
}
