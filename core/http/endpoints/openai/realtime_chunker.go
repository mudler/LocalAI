package openai

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Default clause-chunker bounds (in runes). minRunes gates only sub-sentence
// (clause-mark / Thai-space) cuts so we don't synthesize tiny choppy fragments;
// full sentences always flush regardless of length. maxRunes caps an
// unterminated run so a long punctuation-less span doesn't buffer unbounded.
const (
	defaultClauseMinRunes = 12
	defaultClauseMaxRunes = 200
)

// clauseChunker splits streamed LLM content into speakable clauses for
// incremental TTS, in a SCRIPT-AWARE way so it works for languages without
// whitespace word boundaries. It leans on UAX #29 sentence segmentation (which
// natively terminates on CJK 。！？ as well as Latin .!?), adds CJK clause
// punctuation (，、；：) and Thai/Lao spaces as finer boundaries, and caps an
// over-long unterminated run via UAX #14 line-break opportunities.
//
// Unlike the old ASCII .!?/newline segmenter (dropped in 076dcdbe), it does not
// degrade to whole-message buffering for CJK (handled natively) or Thai/Lao
// (handled via spaces, which Thai uses at clause/sentence boundaries). Scripts
// that genuinely need a dictionary (Khmer/Myanmar) simply stay buffered until a
// space or end-of-message — no worse than the buffered default.
//
// It is not safe for concurrent use; callers feed it from a single goroutine
// (the LLM token callback).
type clauseChunker struct {
	buf      strings.Builder
	minRunes int
	maxRunes int
}

func newClauseChunker(minRunes, maxRunes int) *clauseChunker {
	return &clauseChunker{minRunes: minRunes, maxRunes: maxRunes}
}

// push appends streamed content and returns any clauses that are now complete —
// "complete" meaning confirmed by following content, so we never speak a clause
// that the next token might extend. Incomplete trailing text stays buffered.
func (c *clauseChunker) push(text string) []string {
	c.buf.WriteString(text)
	return c.drain(false)
}

// flush returns the remaining buffered clauses, treating end-of-input as a hard
// boundary, and clears the buffer.
func (c *clauseChunker) flush() []string {
	return c.drain(true)
}

func (c *clauseChunker) drain(final bool) []string {
	s := c.buf.String()
	rest := s
	var out []string
	for rest != "" {
		end, ok := c.nextBoundary(rest, final)
		if !ok {
			break
		}
		if seg := strings.TrimSpace(rest[:end]); seg != "" {
			out = append(out, seg)
		}
		rest = rest[end:]
	}
	// Rewriting the builder reallocates and copies the whole buffer; skip it on
	// the common per-token call where no boundary was confirmed.
	if len(rest) != len(s) {
		c.buf.Reset()
		c.buf.WriteString(rest)
	}
	return out
}

// nextBoundary returns the byte offset just past the first emittable clause in
// s, or ok=false when more input is needed (final=false) and no boundary is
// confirmed yet.
func (c *clauseChunker) nextBoundary(s string, final bool) (int, bool) {
	if s == "" {
		return 0, false
	}

	// 1) UAX #29 sentence boundary. When the first sentence is followed by more
	//    text it is a confirmed complete sentence (handles Latin .!? with
	//    abbreviation/decimal guards, and CJK 。！？ with no whitespace).
	sentence, rest, _ := uniseg.FirstSentenceInString(s, -1)
	if rest != "" {
		// Optionally cut finer inside the sentence at a clause boundary.
		if cut, ok := c.firstClauseCut(sentence); ok {
			return cut, true
		}
		return len(sentence), true
	}

	// 2) Unterminated tail: look for a sub-sentence clause boundary (CJK
	//    punctuation or a Thai/Lao space) confirmed by following content.
	if cut, ok := c.firstClauseCut(s); ok {
		return cut, true
	}

	// 3) Over-long punctuation-less run: force a typographically legal break so
	//    we don't buffer unbounded (e.g. a long CJK run with no punctuation).
	if !final && c.maxRunes > 0 && utf8.RuneCountInString(s) > c.maxRunes {
		if cut, ok := lineBreakCut(s, c.maxRunes); ok {
			return cut, true
		}
	}

	// 4) End of input: emit whatever remains as the final clause.
	if final {
		return len(s), true
	}
	return 0, false
}

// firstClauseCut returns the byte offset just past the first sub-sentence clause
// boundary in s — a CJK clause punctuation mark, or a space following a Thai/Lao
// letter — provided the prefix is at least minRunes long and non-space content
// follows. The boundary mark (and any trailing spaces) stay with the left clause.
func (c *clauseChunker) firstClauseCut(s string) (int, bool) {
	var prev rune
	runes := 0
	for i, r := range s {
		boundary := isCJKClausePunct(r) || (unicode.IsSpace(r) && isThaiLao(prev))
		if boundary && runes+1 >= c.minRunes {
			end := i + utf8.RuneLen(r)
			for end < len(s) {
				nr, sz := utf8.DecodeRuneInString(s[end:])
				if !unicode.IsSpace(nr) {
					break
				}
				end += sz
			}
			if end < len(s) { // confirmed: real content follows the boundary
				return end, true
			}
			// Boundary sits at the end of the buffer with nothing after it yet —
			// wait for the next token to confirm it rather than emit early.
			return 0, false
		}
		prev = r
		runes++
	}
	return 0, false
}

// lineBreakCut walks UAX #14 line segments and returns the byte offset of the
// last legal break opportunity at or before maxRunes. Returns ok=false when the
// run has no internal break opportunity (e.g. a space-less Thai run), leaving it
// buffered.
func lineBreakCut(s string, maxRunes int) (int, bool) {
	state := -1
	rest := s
	consumed := 0
	runes := 0
	for rest != "" {
		seg, rem, _, st := uniseg.FirstLineSegmentInString(rest, state)
		state = st
		runes += utf8.RuneCountInString(seg)
		consumed += len(seg)
		rest = rem
		if runes >= maxRunes {
			if consumed < len(s) {
				return consumed, true
			}
			return 0, false
		}
	}
	return 0, false
}

// isCJKClausePunct reports whether r is a CJK clause-level separator worth a
// soft TTS break. Sentence terminators (。！？) are intentionally excluded — UAX
// #29 sentence segmentation already handles those.
func isCJKClausePunct(r rune) bool {
	switch r {
	case '，', // ， fullwidth comma
		'、', // 、 ideographic comma
		'；', // ； fullwidth semicolon
		'：', // ： fullwidth colon
		'・', // ・ katakana middle dot
		'･': // ・ halfwidth katakana middle dot
		return true
	}
	return false
}

// isThaiLao reports whether r is a Thai or Lao letter. Those scripts have no
// inter-word spaces; an ASCII space inside such a run marks a clause/sentence
// boundary, which is the only no-dictionary segmentation signal available.
func isThaiLao(r rune) bool {
	return unicode.Is(unicode.Thai, r) || unicode.Is(unicode.Lao, r)
}
