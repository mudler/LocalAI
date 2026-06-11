// Gemma4 (DiffusionGemma) streaming output parser: raw model text, fed in
// arbitrary fragments (per committed diffusion block; a fragment can split
// anywhere, including mid-marker and mid-payload), is turned into
// pb.ChatDelta events (content / reasoning_content / tool_calls).
//
// Normative sources:
//   - The chat template embedded at the top of gemma4_renderer.go ("tpl L<n>"
//     citations below refer to its numbered lines). The OUTPUT format mirrors
//     what the template renders for assistant history: thought channels
//     (<|channel>thought\n ... <channel|>, tpl L240), tool calls
//     (<|tool_call>call:name{...}<tool_call|>, tpl L246-L257) and turn ends
//     (<turn|>, tpl L351).
//   - vLLM PR #45163: vllm/tool_parsers/gemma4_tool_parser.py (marker
//     handling, the call:name{...} argument grammar and its decoder, ported
//     below) and vllm/reasoning/gemma4_reasoning_parser.py (channel markers,
//     the "thought\n" role label, is_reasoning_end semantics).
//
// Initial state (derived from the generation prompt, tpl L356-L362, see
// RenderGemma4):
//   - enable_thinking=false: the prompt ends with "<|turn>model\n" +
//     "<|channel>thought\n<channel|>" - an EMPTY thought channel, pre-opened
//     AND pre-closed by the template. The model's output therefore starts in
//     plain content. Use NewGemma4Parser(false).
//   - enable_thinking=true: the prompt ends at "<|turn>model\n" and the model
//     opens and closes its own thought channel in the OUTPUT
//     ("<|channel>thought\n...reasoning...<channel|>final answer", per the
//     vLLM Gemma4ReasoningParser docstring). The parser still starts in
//     content state - the channel markers in the output drive the switch.
//     Use NewGemma4Parser(false) here too.
//   - NewGemma4Parser(true) is for callers that pre-open the thought channel
//     in the prompt themselves (appending "<|channel>thought\n" after the
//     generation prompt to force thinking): the output then begins mid-thought
//     and everything is reasoning until the first <channel|>.
//
// State diagram (markers are consumed, never emitted):
//
//	             <|channel>                  \n (channel name dropped: the
//	[content] --------------> [chan-header] ----> [thought]   "thought\n" role
//	   ^ |  <channel|> (stray close: swallowed,                label, stripped
//	   +-+  strip_thinking semantics, tpl L148-L158)           like vLLM does)
//	   ^                  <channel|>
//	   +----------------------------------------- [thought]
//	   ^                  <tool_call|>                 | <|tool_call> (implicit
//	   +-------------- [tool-call] <-------------------+  reasoning end, vLLM
//	   |  <|tool_call>     ^                               is_reasoning_end)
//	   +-------------------+
//	[content]/[thought] --- <turn|> ---> [done]  (everything after is dropped)
//
// Buffering rules:
//   - content/thought states hold back at most len(longest marker)-1 bytes:
//     the longest tail that is still a proper prefix of a watched marker.
//     Content is otherwise emitted immediately (no unbounded buffering).
//   - the tool-call state buffers the whole payload until <tool_call|>. This
//     is unbounded in principle but bounded in practice by the model's
//     diffusion canvas, and is required because the call:name{...} payload
//     only becomes decodable (and trustworthy) once complete - the same
//     reason vLLM's parser accumulates before parsing.
//   - Close() flushes whatever is still held: partial markers come out as
//     content/reasoning (per the state that held them); an unterminated
//     channel header or tool-call payload is re-emitted RAW (including its
//     opening marker) as content - malformed output is never silently
//     dropped (mirrors vLLM extract_tool_calls returning the raw text as
//     content when its regex does not match).
//
// Streaming granularity DIVERGENCE from vLLM: vLLM re-parses the partial
// payload on every token and streams argument-JSON diffs (its `partial=True`
// decoder mode plus withholding logic exist only for that). Our fragments are
// whole committed diffusion blocks, so each completed tool call is emitted
// once, as a single ToolCallDelta carrying index + id + name + the full
// arguments JSON - exactly the shape backend/python/vllm/backend.py emits
// per call and pkg/functions.ToolCallsFromChatDeltas re-accumulates.
package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// gemma4CallRE is vLLM's tool_call_regex
// (`<\|tool_call>call:([\w\-\.]+)\{(.*?)\}<tool_call\|>`, DOTALL) anchored to
// a single already-extracted payload: name charset [\w\-.], braces mandatory.
var gemma4CallRE = regexp.MustCompile(`(?s)^call:([\w\-.]+)\{(.*)\}$`)

type g4State int

const (
	g4Content g4State = iota
	g4ChanHeader
	g4Thought
	g4ToolCall
	g4Done
)

// Markers watched per emitting state. A stray <tool_call|> outside a tool
// call is deliberately NOT watched: it passes through verbatim, consistent
// with the malformed-payload fallback re-emitting it as content.
var (
	gemma4ContentMarkers = []string{gemma4ChannelOpen, gemma4ChannelClose, gemma4ToolCallOpen, gemma4TurnEnd}
	gemma4ThoughtMarkers = []string{gemma4ChannelClose, gemma4ToolCallOpen, gemma4TurnEnd}
)

type Gemma4Parser struct {
	state g4State
	// held is the per-state carry-over between Feed calls: a partial marker
	// (content/thought), a partial channel header (chan-header) or the
	// payload accumulated so far (tool-call).
	held    string
	toolIdx int
}

// NewGemma4Parser returns a parser positioned per the initial-state rules in
// the header comment: startInThought=true only when the caller pre-opened a
// thought channel in the prompt.
func NewGemma4Parser(startInThought bool) *Gemma4Parser {
	state := g4Content
	if startInThought {
		state = g4Thought
	}
	return &Gemma4Parser{state: state}
}

// Feed consumes the next output fragment and returns the deltas it completes.
func (p *Gemma4Parser) Feed(text string) []*pb.ChatDelta {
	if text == "" || p.state == g4Done {
		return nil
	}
	pending := p.held + text
	p.held = ""
	var em g4Emitter
	for pending != "" {
		switch p.state {
		case g4Content, g4Thought:
			markers := gemma4ContentMarkers
			if p.state == g4Thought {
				markers = gemma4ThoughtMarkers
			}
			idx, marker := findEarliestGemma4Marker(pending, markers)
			if idx == -1 {
				hold := gemma4MarkerHoldback(pending, markers)
				p.emitText(&em, pending[:len(pending)-hold])
				p.held = pending[len(pending)-hold:]
				pending = ""
				continue
			}
			p.emitText(&em, pending[:idx])
			pending = pending[idx+len(marker):]
			switch marker {
			case gemma4ChannelOpen:
				p.state = g4ChanHeader
			case gemma4ChannelClose:
				// In thought: channel ends. In content: stray close,
				// swallowed (strip_thinking keeps both sides, tpl L148-L158).
				p.state = g4Content
			case gemma4ToolCallOpen:
				p.state = g4ToolCall
			case gemma4TurnEnd:
				p.state = g4Done
			}
		case g4ChanHeader:
			// The channel header is "<name>\n"; the template only ever writes
			// "thought" (tpl L240/L360) and the label is structural, so it is
			// dropped, not emitted (vLLM strips the same "thought\n" prefix).
			nl := strings.IndexByte(pending, '\n')
			if nl == -1 {
				p.held = pending
				pending = ""
				continue
			}
			pending = pending[nl+1:]
			p.state = g4Thought
		case g4ToolCall:
			end := strings.Index(pending, gemma4ToolCallClose)
			if end == -1 {
				p.held = pending
				pending = ""
				continue
			}
			p.emitToolCall(&em, pending[:end])
			pending = pending[end+len(gemma4ToolCallClose):]
			p.state = g4Content
		case g4Done:
			pending = ""
		}
	}
	return em.deltas
}

// Close flushes held-back partials. Incomplete structures (open channel
// header, unterminated tool payload) are re-emitted raw as content rather
// than dropped. The parser is finished afterwards.
func (p *Gemma4Parser) Close() []*pb.ChatDelta {
	var em g4Emitter
	switch p.state {
	case g4Content:
		em.content(p.held)
	case g4Thought:
		em.reasoning(p.held)
	case g4ChanHeader:
		em.content(gemma4ChannelOpen + p.held)
	case g4ToolCall:
		em.content(gemma4ToolCallOpen + p.held)
	case g4Done:
	}
	p.held = ""
	p.state = g4Done
	return em.deltas
}

func (p *Gemma4Parser) emitText(em *g4Emitter, s string) {
	if p.state == g4Thought {
		em.reasoning(s)
		return
	}
	em.content(s)
}

// emitToolCall decodes one complete <|tool_call>...<tool_call|> payload. On a
// payload that does not match call:name{...} the raw text (markers included)
// is emitted as content, mirroring vLLM's extract_tool_calls fallback.
func (p *Gemma4Parser) emitToolCall(em *g4Emitter, payload string) {
	m := gemma4CallRE.FindStringSubmatch(payload)
	if m == nil {
		em.content(gemma4ToolCallOpen + payload + gemma4ToolCallClose)
		return
	}
	// Index-based ids: deterministic (the split-invariance property relies
	// on it) and matching the call_<n> convention of pkg/grpc/rich_test.go;
	// core only needs ids to be non-empty and unique within the response.
	em.tool(p.toolIdx, "call_"+strconv.Itoa(p.toolIdx), m[1], decodeGemma4Args(m[2], 0))
	p.toolIdx++
}

// g4Emitter collects ChatDeltas; empty text events are dropped.
type g4Emitter struct {
	deltas []*pb.ChatDelta
}

func (e *g4Emitter) content(s string) {
	if s != "" {
		e.deltas = append(e.deltas, &pb.ChatDelta{Content: s})
	}
}

func (e *g4Emitter) reasoning(s string) {
	if s != "" {
		e.deltas = append(e.deltas, &pb.ChatDelta{ReasoningContent: s})
	}
}

func (e *g4Emitter) tool(index int, id, name, argsJSON string) {
	e.deltas = append(e.deltas, &pb.ChatDelta{ToolCalls: []*pb.ToolCallDelta{{
		Index:     int32(index),
		Id:        id,
		Name:      name,
		Arguments: argsJSON,
	}}})
}

// findEarliestGemma4Marker returns the position and value of the first
// complete marker occurrence, or (-1, "").
func findEarliestGemma4Marker(s string, markers []string) (int, string) {
	best, bestMarker := -1, ""
	for _, m := range markers {
		if idx := strings.Index(s, m); idx >= 0 && (best == -1 || idx < best) {
			best, bestMarker = idx, m
		}
	}
	return best, bestMarker
}

// gemma4MarkerHoldback returns the length of the longest suffix of s that is
// a proper prefix of a watched marker - the only bytes that may still grow
// into a marker and therefore must not be emitted yet (bounded by the
// longest marker, so content is never buffered unboundedly).
func gemma4MarkerHoldback(s string, markers []string) int {
	maxHold := 0
	for _, m := range markers {
		if len(m)-1 > maxHold {
			maxHold = len(m) - 1
		}
	}
	if len(s) < maxHold {
		maxHold = len(s)
	}
	for k := maxHold; k >= 1; k-- {
		tail := s[len(s)-k:]
		for _, m := range markers {
			if strings.HasPrefix(m, tail) {
				return k
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// call:name{...} argument decoder
//
// Port of vLLM's _parse_gemma4_args / _parse_gemma4_array /
// _parse_gemma4_value (gemma4_tool_parser.py) in non-partial mode only: this
// parser decodes exclusively COMPLETE payloads (incomplete ones fall back to
// raw content at Close), so vLLM's partial-withholding machinery
// (trailing-dot floats, withheld bare tails) is intentionally not ported.
//
// Grammar (inverse of the renderer's formatGemma4Argument, tpl L118-L147):
//
//	args    := pair (',' pair)*
//	pair    := key ':' value          (keys unquoted, up to the first ':')
//	value   := string | object | array | bare
//	string  := '<|"|>' ... '<|"|>'    (no escapes; unterminated -> rest)
//	object  := '{' args '}'           (delimited strings skipped when
//	array   := '[' value,* ']'         counting braces/brackets)
//	bare    := true | false | null/none/nil | number | bare-string
//
// Output is a JSON object/array string with keys in payload order (Python
// dict insertion order), built with HTML escaping off so payload text
// survives byte-for-byte.
// ---------------------------------------------------------------------------

func isGemma4Space(c byte) bool { return c == ' ' || c == '\n' || c == '\t' }

// gemma4MaxArgsDepth caps the mutual recursion between decodeGemma4Args and
// decodeGemma4Array. Defense against model-generated deep nesting: a Go stack
// overflow is a fatal process kill, not a recoverable error, so past the cap
// a nested body gracefully degrades to a JSON string of its raw text.
const gemma4MaxArgsDepth = 100

// decodeGemma4Args decodes one args body (the text between the outer braces
// of call:name{...}) into a JSON object string. depth is the current nesting
// level (0 at the payload root); see gemma4MaxArgsDepth.
func decodeGemma4Args(s string, depth int) string {
	if depth > gemma4MaxArgsDepth {
		return gemma4JSONString(s)
	}
	var b strings.Builder
	b.WriteString("{")
	first := true
	pair := func(key, val string) {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(gemma4JSONString(key))
		b.WriteString(":")
		b.WriteString(val)
	}
	i, n := 0, len(s)
	for i < n {
		for i < n && (isGemma4Space(s[i]) || s[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		keyStart := i
		for i < n && s[i] != ':' {
			i++
		}
		if i >= n {
			break // no ':' -> trailing junk, dropped (vLLM does the same)
		}
		key := strings.TrimSpace(s[keyStart:i])
		i++ // skip ':'
		for i < n && isGemma4Space(s[i]) {
			i++
		}
		if i >= n {
			pair(key, `""`) // "key:" with nothing after -> empty string
			break
		}
		switch {
		case strings.HasPrefix(s[i:], gemma4StringDelim):
			i += len(gemma4StringDelim)
			if end := strings.Index(s[i:], gemma4StringDelim); end == -1 {
				pair(key, gemma4JSONString(s[i:])) // unterminated -> take rest
				i = n
			} else {
				pair(key, gemma4JSONString(s[i:i+end]))
				i += end + len(gemma4StringDelim)
			}
		case s[i] == '{':
			inner, next := scanGemma4Balanced(s, i, '{', '}')
			pair(key, decodeGemma4Args(inner, depth+1))
			i = next
		case s[i] == '[':
			inner, next := scanGemma4Balanced(s, i, '[', ']')
			pair(key, decodeGemma4Array(inner, depth+1))
			i = next
		default:
			valStart := i
			for i < n && s[i] != ',' && s[i] != '}' && s[i] != ']' {
				i++
			}
			if i == valStart {
				// No progress (value starts on a stray '}'/']'): abort on
				// malformed input rather than loop, like vLLM.
				i = n
				continue
			}
			pair(key, decodeGemma4Bare(s[valStart:i]))
		}
	}
	b.WriteString("}")
	return b.String()
}

// decodeGemma4Array decodes one array body (the text between '[' and ']')
// into a JSON array string. depth is the current nesting level; see
// gemma4MaxArgsDepth.
func decodeGemma4Array(s string, depth int) string {
	if depth > gemma4MaxArgsDepth {
		return gemma4JSONString(s)
	}
	var b strings.Builder
	b.WriteString("[")
	first := true
	item := func(val string) {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(val)
	}
	i, n := 0, len(s)
	for i < n {
		for i < n && (isGemma4Space(s[i]) || s[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		switch {
		case strings.HasPrefix(s[i:], gemma4StringDelim):
			i += len(gemma4StringDelim)
			if end := strings.Index(s[i:], gemma4StringDelim); end == -1 {
				item(gemma4JSONString(s[i:]))
				i = n
			} else {
				item(gemma4JSONString(s[i : i+end]))
				i += end + len(gemma4StringDelim)
			}
		case s[i] == '{':
			inner, next := scanGemma4Balanced(s, i, '{', '}')
			item(decodeGemma4Args(inner, depth+1))
			i = next
		case s[i] == '[':
			inner, next := scanGemma4Balanced(s, i, '[', ']')
			item(decodeGemma4Array(inner, depth+1))
			i = next
		default:
			valStart := i
			for i < n && s[i] != ',' && s[i] != ']' {
				i++
			}
			if i == valStart {
				i = n // no progress: abort on malformed input, like vLLM
				continue
			}
			item(decodeGemma4Bare(s[valStart:i]))
		}
	}
	b.WriteString("]")
	return b.String()
}

// scanGemma4Balanced scans a brace/bracket-balanced span starting at the
// opener s[start], skipping over <|"|>-delimited strings so structural
// characters inside them do not count (vLLM's depth scan). Returns the inner
// text and the index just past the closer; an unterminated span yields the
// rest of the string (the inner decoder still extracts what is there - this
// path is only reachable from genuinely malformed complete payloads).
func scanGemma4Balanced(s string, start int, open, close byte) (string, int) {
	depth := 1
	i := start + 1
	innerStart := i
	n := len(s)
	for i < n && depth > 0 {
		if strings.HasPrefix(s[i:], gemma4StringDelim) {
			i += len(gemma4StringDelim)
			if nd := strings.Index(s[i:], gemma4StringDelim); nd == -1 {
				i = n
			} else {
				i += nd + len(gemma4StringDelim)
			}
			continue
		}
		switch s[i] {
		case open:
			depth++
		case close:
			depth--
		}
		i++
	}
	if depth > 0 {
		return s[innerStart:], n
	}
	return s[innerStart : i-1], i
}

// decodeGemma4Bare maps an undelimited value to its JSON form: booleans,
// null aliases (null/none/nil, case-insensitive - the renderer writes
// Python None as "None", tpl L144-L145 via format_argument's else branch),
// numbers (vLLM's rule: a '.' tries float, otherwise int; anything that
// fails parses as a bare string).
func decodeGemma4Bare(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return `""`
	}
	if v == "true" || v == "false" {
		return v
	}
	switch strings.ToLower(v) {
	case "null", "none", "nil":
		return "null"
	}
	if strings.Contains(v, ".") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return formatGemma4Float(f)
		}
	} else if iv, err := strconv.ParseInt(v, 10, 64); err == nil {
		return strconv.FormatInt(iv, 10)
	}
	return gemma4JSONString(v)
}

// formatGemma4Float renders like Python's json.dumps(float): integral floats
// keep a ".0" suffix ("108." decodes to 108.0, not 108), so the arguments
// JSON matches what vLLM would have produced for the same payload.
func formatGemma4Float(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// gemma4JSONString encodes a JSON string WITHOUT HTML escaping (json.Marshal
// would escape the angle brackets in "<div>" to \u003c / \u003e sequences;
// payload text should survive
// byte-for-byte, like Python's json.dumps(ensure_ascii=False)).
func gemma4JSONString(s string) string {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// Unreachable for plain strings; fall back to default escaping
		// rather than emitting invalid JSON.
		b, mErr := json.Marshal(s)
		if mErr != nil {
			return `""`
		}
		return string(b)
	}
	// Encode appends a trailing newline.
	return strings.TrimSuffix(sb.String(), "\n")
}
