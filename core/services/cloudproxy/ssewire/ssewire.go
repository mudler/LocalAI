// Package ssewire holds the SSE-format helpers shared between
// the request-shape cloud proxy (core/services/cloudproxy) and the
// TLS-terminating MITM proxy (core/services/cloudproxy/mitm). Both
// run a pii.StreamFilter over per-token text extracted from
// provider-specific JSON chunks; this package owns the JSON shapes
// so a future provider addition is one edit, not two.
package ssewire

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/routing/pii"
)

// Provider is the upstream wire format an SSE stream conforms to.
type Provider string

const (
	OpenAI    Provider = "openai"
	Anthropic Provider = "anthropic"
)

// Event is one SSE event with its exact wire bytes preserved in
// Raw (so unmodified events round-trip byte-for-byte) and the
// extracted JSON payload from the data: line in DataLine.
type Event struct {
	Raw      string
	DataLine string
}

// Scanner reads SSE events one at a time from an upstream body.
type Scanner struct {
	r   *bufio.Reader
	ev  Event
	err error
}

func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReaderSize(r, 64*1024)}
}

func (s *Scanner) Scan() bool {
	var raw strings.Builder
	var dataLine string
	for {
		line, err := s.r.ReadString('\n')
		if line != "" {
			raw.WriteString(line)
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				if raw.Len() == len(line) {
					raw.Reset()
					continue
				}
				s.ev = Event{Raw: raw.String(), DataLine: dataLine}
				return true
			}
			if strings.HasPrefix(trimmed, "data:") && dataLine == "" {
				payload := strings.TrimPrefix(trimmed, "data:")
				payload = strings.TrimPrefix(payload, " ")
				dataLine = payload
			}
		}
		if err != nil {
			s.err = err
			if raw.Len() > 0 {
				s.ev = Event{Raw: raw.String(), DataLine: dataLine}
				return true
			}
			return false
		}
	}
}

func (s *Scanner) Event() Event { return s.ev }
func (s *Scanner) Err() error   { return s.err }

// IsTerminalMarker reports whether the data line is the per-provider
// end-of-stream sentinel. The streaming PII filter must drain its
// residue before the caller forwards a terminal marker — clients
// stop reading after it.
func IsTerminalMarker(dataLine string, provider Provider) bool {
	if dataLine == "" {
		return false
	}
	if strings.TrimSpace(dataLine) == "[DONE]" {
		return true
	}
	if provider == Anthropic {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(dataLine), &probe); err == nil {
			return probe.Type == "message_stop"
		}
	}
	return false
}

// RewritePayload runs the data line's content-bearing field through
// the streaming filter. drop=true tells the caller to suppress the
// SSE event entirely (the filter buffered the whole token while
// disambiguating a pattern boundary).
func RewritePayload(dataLine string, provider Provider, filter *pii.StreamFilter) (rewritten string, drop bool) {
	if strings.TrimSpace(dataLine) == "[DONE]" {
		return dataLine, false
	}
	switch provider {
	case Anthropic:
		return rewriteAnthropic(dataLine, filter)
	default:
		return rewriteOpenAI(dataLine, filter)
	}
}

func rewriteOpenAI(dataLine string, filter *pii.StreamFilter) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(dataLine), &m); err != nil {
		return dataLine, false
	}
	choices, ok := m["choices"].([]any)
	if !ok || len(choices) == 0 {
		return dataLine, false
	}
	first, ok := choices[0].(map[string]any)
	if !ok {
		return dataLine, false
	}
	delta, ok := first["delta"].(map[string]any)
	if !ok {
		return dataLine, false
	}
	content, ok := delta["content"].(string)
	if !ok || content == "" {
		return dataLine, false
	}
	rewritten := filter.Push(content)
	if rewritten == "" {
		return "", true
	}
	if rewritten == content {
		return dataLine, false
	}
	delta["content"] = rewritten
	out, err := json.Marshal(m)
	if err != nil {
		return dataLine, false
	}
	return string(out), false
}

func rewriteAnthropic(dataLine string, filter *pii.StreamFilter) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(dataLine), &m); err != nil {
		return dataLine, false
	}
	if t, _ := m["type"].(string); t != "content_block_delta" {
		return dataLine, false
	}
	delta, ok := m["delta"].(map[string]any)
	if !ok {
		return dataLine, false
	}
	if dt, _ := delta["type"].(string); dt != "text_delta" {
		return dataLine, false
	}
	text, ok := delta["text"].(string)
	if !ok || text == "" {
		return dataLine, false
	}
	rewritten := filter.Push(text)
	if rewritten == "" {
		return "", true
	}
	if rewritten == text {
		return dataLine, false
	}
	delta["text"] = rewritten
	out, err := json.Marshal(m)
	if err != nil {
		return dataLine, false
	}
	return string(out), false
}

// SynthResidualEvent builds a provider-shaped SSE event carrying
// the streaming filter's drained tail so the response body remains
// a valid event stream after the proxy splices in held-back text.
func SynthResidualEvent(provider Provider, text string) string {
	switch provider {
	case Anthropic:
		payload := map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]string{"type": "text_delta", "text": text},
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return ""
		}
		return "event: content_block_delta\ndata: " + string(b) + "\n\n"
	default:
		payload := map[string]any{
			"object": "chat.completion.chunk",
			"choices": []map[string]any{
				{"index": 0, "delta": map[string]string{"content": text}},
			},
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return ""
		}
		return "data: " + string(b) + "\n\n"
	}
}
