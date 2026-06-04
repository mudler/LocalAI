package openai

import "strings"

// streamSegmenter accumulates streamed LLM text and emits complete utterance
// segments (sentence/clause boundaries) so the realtime pipeline can hand each
// segment to TTS as soon as it's complete, overlapping generation, synthesis
// and playback instead of waiting for the whole reply.
//
// A segment is committed when a sentence terminator (. ! ?) is followed by
// whitespace, or at a newline. Terminators not followed by whitespace (e.g.
// decimals like "3.14" mid-stream) stay buffered until more text arrives or the
// stream is flushed.
type streamSegmenter struct {
	buf strings.Builder
}

func isSentenceTerminator(b byte) bool {
	return b == '.' || b == '!' || b == '?'
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// Push appends text to the buffer and returns any newly-completed segments,
// trimmed of surrounding whitespace. Incomplete trailing text stays buffered.
func (s *streamSegmenter) Push(text string) []string {
	s.buf.WriteString(text)
	cur := s.buf.String()

	var segments []string
	start := 0
	for i := 0; i < len(cur); i++ {
		cut := -1
		switch {
		case cur[i] == '\n':
			cut = i // segment excludes the newline
		case isSentenceTerminator(cur[i]) && i+1 < len(cur) && isSpace(cur[i+1]):
			cut = i + 1 // segment includes the terminator
		}
		if cut >= 0 {
			if seg := strings.TrimSpace(cur[start:cut]); seg != "" {
				segments = append(segments, seg)
			}
			start = cut
		}
	}

	rem := cur[start:]
	s.buf.Reset()
	s.buf.WriteString(rem)
	return segments
}

// Flush returns the remaining buffered text (trimmed) and clears the buffer.
func (s *streamSegmenter) Flush() string {
	seg := strings.TrimSpace(s.buf.String())
	s.buf.Reset()
	return seg
}
