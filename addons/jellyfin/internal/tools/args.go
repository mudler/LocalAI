// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from the server or the key.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

var (
	itemIDShape    = regexp.MustCompile(`^[0-9A-Fa-f]{32}$|^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	sessionIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	typeShape      = regexp.MustCompile(`^[A-Za-z]{1,32}$`)
)

func checkItemID(what, id string) error {
	if !itemIDShape.MatchString(id) {
		return bad("%s is an item ID from an earlier result", what)
	}
	return nil
}

// allowedTypes are the item types a search or a browse may ask for.
var allowedTypes = map[string]bool{
	"Movie": true, "Series": true, "Season": true, "Episode": true, "Audio": true, "MusicAlbum": true,
	"MusicArtist": true, "MusicVideo": true, "Playlist": true, "BoxSet": true, "Book": true, "AudioBook": true,
	"Video": true, "Photo": true, "Folder": true,
}

func checkTypes(ts []string) error {
	if len(ts) > 8 {
		return bad("types has at most 8 names")
	}
	for _, t := range ts {
		if !typeShape.MatchString(t) || !allowedTypes[t] {
			return bad("types has a name that is not an item type")
		}
	}
	return nil
}

// checkText accepts a plain one-line text of 1 to max characters.
func checkText(what, s string, max int, required bool) error {
	if strings.TrimSpace(s) == "" {
		if required {
			return bad("%s is required", what)
		}
		return nil
	}
	if utf8.RuneCountInString(s) > max || !utf8.ValidString(s) {
		return bad("%s has at most %d characters", what, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return bad("%s has a control character", what)
		}
	}
	return nil
}

// decodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal.
func decodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("the arguments do not match the tool")
	}
	return nil
}

// runes cuts s to n characters and says whether it cut. Control characters
// become spaces first, so text from the server stays on plain lines.
func runes(s string, n int) (string, bool) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return strings.TrimSpace(string([]rune(s)[:n])) + "...", true
}

// oneLine is runes for a name: a single line.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	out, _ := runes(s, n)
	return out
}

func pageOr(p, def int) int {
	if p < 1 {
		return def
	}
	return min(p, MaxEntries)
}
