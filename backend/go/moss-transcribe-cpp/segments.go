package main

import (
	"regexp"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// mossSegment is one parsed unit of the MOSS transcript: a speaker-labelled
// span with fractional-second start/end times, straight from the model's own
// output. Timestamps stay in seconds here; secondsToNanos converts them at the
// TranscriptSegment boundary.
type mossSegment struct {
	Start   float64
	End     float64
	Speaker string
	Text    string
}

// bracketRe matches one "[...]" token (timestamp or speaker tag). The MOSS
// transcript is a concatenation of "[start][Sxx]text[end]" segments, e.g.
//
//	[0.28][S01] And so, my fellow Americans,[7.71][8.12][S02] ask ...[10.59]
//
// so the bracketed tokens carry all the structure and the free text lives
// between a speaker tag and the following (end) timestamp.
var bracketRe = regexp.MustCompile(`\[([^\]]*)\]`)

// speakerRe matches a speaker tag: an 'S' (any case) followed by one or more
// digits, e.g. "S01", "S12". Anything else in brackets that isn't a speaker tag
// is treated as a timestamp candidate.
var speakerRe = regexp.MustCompile(`^[Ss][0-9]+$`)

// bracketToken is a "[...]" token plus the free text that follows it up to the
// next token (or end of string). For a speaker tag that trailing text is the
// segment's transcript.
type bracketToken struct {
	content   string
	textAfter string
}

// parseTranscript parses the compact "[start][Sxx]text[end]..." MOSS transcript
// into structured segments. It walks the bracket tokens looking for a
// start-timestamp, a speaker tag, and an end-timestamp, taking the text that
// follows the speaker tag as the segment transcript. Tokens that don't fit the
// grammar are skipped rather than aborting the parse, so a slightly malformed
// stream still yields the segments it can.
func parseTranscript(raw string) []mossSegment {
	locs := bracketRe.FindAllStringSubmatchIndex(raw, -1)
	toks := make([]bracketToken, len(locs))
	for i, m := range locs {
		// m[2]:m[3] is the capture group (the content inside the brackets);
		// m[1] is the byte just past the closing ']'.
		nextStart := len(raw)
		if i+1 < len(locs) {
			nextStart = locs[i+1][0]
		}
		toks[i] = bracketToken{
			content:   raw[m[2]:m[3]],
			textAfter: raw[m[1]:nextStart],
		}
	}

	var segs []mossSegment
	i := 0
	for i < len(toks) {
		start, ok := parseTimestamp(toks[i].content)
		if !ok {
			i++
			continue
		}
		// A start timestamp must be followed by a speaker tag; otherwise this
		// isn't a segment head, so skip it.
		if i+1 >= len(toks) || !isSpeakerTag(toks[i+1].content) {
			i++
			continue
		}
		speaker := strings.ToUpper(strings.TrimSpace(toks[i+1].content))
		text := strings.TrimSpace(toks[i+1].textAfter)

		// The end timestamp is the next token if present and numeric; a segment
		// that runs to the end of the stream without a closing timestamp falls
		// back to start==end.
		end := start
		consumed := 2
		if i+2 < len(toks) {
			if e, ok := parseTimestamp(toks[i+2].content); ok {
				end = e
				consumed = 3
			}
		}

		segs = append(segs, mossSegment{Start: start, End: end, Speaker: speaker, Text: text})
		i += consumed
	}
	return segs
}

// isSpeakerTag reports whether the bracket content is a MOSS speaker tag ("Sxx").
func isSpeakerTag(content string) bool {
	return speakerRe.MatchString(strings.TrimSpace(content))
}

// parseTimestamp parses a bracket content as a fractional-second timestamp. A
// speaker tag ("S01") never parses as a float, so this cleanly distinguishes
// the two token kinds.
func parseTimestamp(content string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(content), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// secondsToNanos converts the transcript's fractional-second timestamps into
// the int64 nanoseconds LocalAI carries on TranscriptSegment, the same
// nanosecond convention the whisper / parakeet-cpp backends use.
func secondsToNanos(sec float64) int64 {
	return int64(sec * 1e9)
}

// transcriptResultFromRaw parses the raw MOSS transcript and shapes it into a
// TranscriptResult. Each parsed segment becomes a TranscriptSegment with
// nanosecond start/end and the model's own speaker label; Text is the segments
// joined with single spaces. When nothing parses (no bracket structure) it
// falls back to a single whole-clip text segment so callers always get a
// transcript.
func transcriptResultFromRaw(raw string) pb.TranscriptResult {
	segs := parseTranscript(raw)
	if len(segs) == 0 {
		text := strings.TrimSpace(raw)
		return pb.TranscriptResult{
			Text:     text,
			Segments: []*pb.TranscriptSegment{{Id: 0, Text: text}},
		}
	}

	var full strings.Builder
	pbSegs := make([]*pb.TranscriptSegment, 0, len(segs))
	for id, s := range segs {
		if id > 0 && s.Text != "" {
			full.WriteString(" ")
		}
		full.WriteString(s.Text)
		pbSegs = append(pbSegs, &pb.TranscriptSegment{
			Id:      int32(id),
			Start:   secondsToNanos(s.Start),
			End:     secondsToNanos(s.End),
			Text:    s.Text,
			Speaker: s.Speaker,
		})
	}
	return pb.TranscriptResult{Text: strings.TrimSpace(full.String()), Segments: pbSegs}
}
