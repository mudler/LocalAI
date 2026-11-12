// SPDX-License-Identifier: MIT

// Package argutil holds the small checks the tools of an add-on make on the
// arguments a model gave, and the helpers that shape a result.
package argutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/protocol"
)

// Bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from the service or from the
// token.
func Bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

// cursorShape is the shape of a page token the service made.
var cursorShape = regexp.MustCompile(`^[A-Za-z0-9_=.~+/%-]{1,1000}$`)

// CheckCursor accepts an empty page token or one of the shape the service
// makes.
func CheckCursor(c string) error {
	if c != "" && !cursorShape.MatchString(c) {
		return Bad("cursor is not one this tool gave")
	}
	return nil
}

// Plain reports text with no control character other than the three white
// space ones, and valid UTF-8.
func Plain(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// CheckLine accepts one line of text of 1 to limit characters.
func CheckLine(what, s string, limit int, required bool) error {
	if strings.TrimSpace(s) == "" {
		if required {
			return Bad("%s is required", what)
		}
		return nil
	}
	if utf8.RuneCountInString(s) > limit || !Plain(s) || strings.ContainsAny(s, "\n\r\t") {
		return Bad("%s has 1 to %d characters on one line", what, limit)
	}
	return nil
}

// SplitArgs separates the name of the tool from its other arguments.
func SplitArgs(arguments json.RawMessage) (string, map[string]json.RawMessage, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(arguments, &args) != nil {
		return "", nil, Bad("the arguments are not an object")
	}
	var tool string
	if json.Unmarshal(args["tool"], &tool) != nil || tool == "" {
		return "", nil, Bad("no tool")
	}
	delete(args, "tool")
	return tool, args, nil
}

// DecodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal: the host checked the schema, so one here is a
// bug to surface and not input to guess at.
func DecodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Bad("the arguments do not match the tool")
	}
	return nil
}

// Cut shortens s to at most limit bytes at a character boundary and says how
// much was left out.
func Cut(s string, limit int) (out string, omitted int) {
	if len(s) <= limit {
		return s, 0
	}
	n := limit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}

// Runes shortens s to at most n characters and says whether it did.
func Runes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

// IDPath is an ID as one path segment.
func IDPath(id string) string { return url.PathEscape(id) }

// Sum64 is a digest of parts that never is zero. It stands for the data
// revision of what a write works on.
func Sum64(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		// The length is written as text, so each part is delimited and no
		// integer conversion is needed.
		h.Write([]byte(strconv.Itoa(len(p)) + ":"))
		h.Write([]byte(p))
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]) | 1
}

// PlainJSON encodes v without the HTML escapes of encoding/json.
func PlainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}
