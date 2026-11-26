package reasoning

import (
	"strings"
	"unicode/utf8"
)

// incrementalExtraction keeps the result of ExtractReasoning for a text that
// only grows, so that a stream does not parse its whole output again for every
// token. Parsing the whole output costs time in proportion to its length, so a
// stream of n tokens did work in proportion to n squared.
//
// The parse of ExtractReasoning is a sequence of searches for tag markers. When
// the appended text does not complete a marker, every search finds what it
// found before, so the parse makes the same decisions and only its last segment
// grows: the cleaned text when the text ends outside a reasoning block, or the
// reasoning of the block that is still open. append handles that case in time
// proportional to the token. Any other case parses the whole text again, so the
// result is always the one that ExtractReasoning gives.
type incrementalExtraction struct {
	config    *Config
	markers   []string
	maxMarker int

	text      strings.Builder
	reasoning strings.Builder
	cleaned   strings.Builder
	// openAt is where the body of the reasoning block that is still open
	// starts in text, or -1 when the text ends outside a block.
	openAt int
	// openTrimmed is the length of the trimmed body of the open block, which
	// is the part of reasoning that the block adds. Zero adds nothing.
	openTrimmed int
	// endsOnRune tells whether text ends with a complete UTF-8 sequence. Bytes
	// that complete an earlier rune could make it a space that TrimSpace drops.
	endsOnRune bool
}

func newIncrementalExtraction(config *Config, extraMarkers ...string) *incrementalExtraction {
	e := &incrementalExtraction{config: config, openAt: -1, endsOnRune: true}
	add := func(m string) {
		if m == "" {
			return
		}
		e.markers = append(e.markers, m)
		e.maxMarker = max(e.maxMarker, len(m))
	}
	for _, pair := range config.TagPairs {
		if pair.Start != "" && pair.End != "" {
			add(pair.Start)
			add(pair.End)
		}
	}
	for _, pair := range defaultReasoningTagPairs {
		add(pair.Start)
		add(pair.End)
	}
	for _, m := range extraMarkers {
		add(m)
	}
	return e
}

func (e *incrementalExtraction) Text() string      { return e.text.String() }
func (e *incrementalExtraction) Reasoning() string { return e.reasoning.String() }
func (e *incrementalExtraction) Cleaned() string   { return e.cleaned.String() }

// reset parses text from the start.
func (e *incrementalExtraction) reset(text string) {
	e.text.Reset()
	e.text.WriteString(text)
	reasoning, cleaned, openAt := extractReasoning(text, e.config)
	e.reasoning.Reset()
	e.reasoning.WriteString(reasoning)
	e.cleaned.Reset()
	e.cleaned.WriteString(cleaned)
	e.openAt = openAt
	e.openTrimmed = 0
	if openAt >= 0 {
		e.openTrimmed = len(strings.TrimSpace(text[openAt:]))
	}
	e.endsOnRune = endsOnRune(text)
}

// tryAppend appends token when that cannot change a decision of the parse and
// returns the growth of reasoning and of cleaned. It returns ok false, and
// changes nothing, when the text must be parsed again.
func (e *incrementalExtraction) tryAppend(token string) (reasoningDelta, cleanedDelta string, ok bool) {
	if token == "" {
		return "", "", true
	}
	if !e.endsOnRune || !utf8.ValidString(token) || e.completesMarker(token) {
		return "", "", false
	}
	e.text.WriteString(token)
	if e.openAt < 0 {
		e.cleaned.WriteString(token)
		return "", token, true
	}
	body := strings.TrimSpace(e.text.String()[e.openAt:])
	if len(body) == e.openTrimmed {
		return "", "", true
	}
	// The body only grew at its end and starts where it started, so the old
	// trimmed body is a prefix of the new one.
	if e.openTrimmed > 0 {
		reasoningDelta = body[e.openTrimmed:]
	} else if e.reasoning.Len() > 0 {
		reasoningDelta = "\n\n" + body
	} else {
		reasoningDelta = body
	}
	e.reasoning.WriteString(reasoningDelta)
	e.openTrimmed = len(body)
	return reasoningDelta, "", true
}

// completesMarker tells whether text plus token holds a marker that ends inside
// token. Only such an occurrence is new.
func (e *incrementalExtraction) completesMarker(token string) bool {
	text := e.text.String()
	if len(text) < e.maxMarker-1 {
		return true // short text: parsing it again is cheap
	}
	window := text[len(text)-(e.maxMarker-1):] + token
	for _, m := range e.markers {
		// Skip the window bytes that cannot start an occurrence ending in token.
		from := e.maxMarker - len(m)
		if strings.Contains(window[from:], m) {
			return true
		}
	}
	return false
}

func endsOnRune(s string) bool {
	if s == "" {
		return true
	}
	r, size := utf8.DecodeLastRuneInString(s)
	return r != utf8.RuneError || size > 1
}
