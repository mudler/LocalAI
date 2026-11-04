// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from Google Drive or from the
// token.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

var (
	// idShape is the shape of a file or folder ID, and of the word "root".
	idShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	// mimeShape is the shape of a media type used in a search.
	mimeShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}/[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]{0,126}$`)
	// cursorShape is the shape of a page token the service made.
	cursorShape = regexp.MustCompile(`^[A-Za-z0-9_=.~+/%-]{1,1000}$`)
)

func checkID(what, id string) error {
	if !idShape.MatchString(id) {
		return bad("%s is not a file ID", what)
	}
	return nil
}

func checkCursor(c string) error {
	if c != "" && !cursorShape.MatchString(c) {
		return bad("cursor is not one this tool gave")
	}
	return nil
}

// plain reports text with no control character other than the three white
// space ones, and valid UTF-8.
func plain(s string) bool {
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

// checkLine accepts one line of text of 1 to max characters.
func checkLine(what, s string, max int, required bool) error {
	if strings.TrimSpace(s) == "" {
		if required {
			return bad("%s is required", what)
		}
		return nil
	}
	if utf8.RuneCountInString(s) > max || !plain(s) || strings.ContainsAny(s, "\n\r\t") {
		return bad("%s has 1 to %d characters on one line", what, max)
	}
	return nil
}

// decodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal: the host checked the schema, so one here is a
// bug to surface and not input to guess at.
func decodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("the arguments do not match the tool")
	}
	return nil
}

// cut shortens s to at most max bytes at a character boundary and says how
// much was left out.
func cut(s string, max int) (out string, omitted int) {
	if len(s) <= max {
		return s, 0
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}

func runes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

// idPath is an ID as one path segment.
func idPath(id string) string { return url.PathEscape(id) }
