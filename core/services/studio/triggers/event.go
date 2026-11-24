package triggers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// cutMarker ends a payload text that was cut.
const cutMarker = "…[cut]"

// Event is one thing that happened outside, as the hook received it.
type Event struct {
	Source    string
	Type      string
	DedupeKey string
	Payload   []byte
}

// MatchesEvent tells whether the event starts this trigger. Source and
// EventType compare without case; an empty EventType matches any type. Each
// field=value in Matches must equal the text of that JSON path in the payload.
// A payload that is not JSON matches only a trigger with no Matches.
func (t Trigger) MatchesEvent(ev Event) bool {
	if !strings.EqualFold(t.Source, ev.Source) {
		return false
	}
	if t.EventType != "" && !strings.EqualFold(t.EventType, ev.Type) {
		return false
	}
	if len(t.Matches) == 0 {
		return true
	}
	var doc any
	if err := json.Unmarshal(ev.Payload, &doc); err != nil {
		return false
	}
	for _, m := range t.Matches {
		field, want, ok := strings.Cut(m, "=")
		if !ok {
			return false
		}
		got, found := lookupPath(doc, field)
		if !found || got != want {
			return false
		}
	}
	return true
}

// lookupPath follows a dot path through decoded JSON. An array is indexed by
// its decimal index. It returns the scalar as text, or false when the path is
// missing or ends at an object, an array or null.
func lookupPath(doc any, path string) (string, bool) {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[seg]
			if !ok {
				return "", false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return "", false
			}
			cur = v[i]
		default:
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case json.Number:
		return v.String(), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

// TrimPayload makes the text of a payload for a run and its prompt. Valid JSON
// is compacted; other bodies are text. Control characters other than tab and
// newline are removed. The result is cut at max bytes on a rune boundary, and
// the cut marker is added after it.
func TrimPayload(raw []byte, max int) string {
	text := string(raw)
	var buf bytes.Buffer
	if json.Valid(raw) && json.Compact(&buf, raw) == nil {
		text = buf.String()
	}
	text = strings.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + cutMarker
}

// Sign returns the signature of a body sent at ts: "sha256=" and the hex HMAC
// of "<unix seconds>." followed by the body.
func Sign(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts.Unix())
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify tells whether header is the signature of body at ts, and ts is within
// window of now. The comparison takes the same time for any header.
func Verify(secret, header string, ts time.Time, body []byte, now time.Time, window time.Duration) bool {
	if d := now.Sub(ts); d > window || d < -window {
		return false
	}
	return hmac.Equal([]byte(Sign(secret, ts, body)), []byte(header))
}

// VerifyGitHub checks an X-Hub-Signature-256 header over the raw body. GitHub
// sends no timestamp; the dedupe key bounds a replay.
func VerifyGitHub(secret, header string, body []byte) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(header))
}
