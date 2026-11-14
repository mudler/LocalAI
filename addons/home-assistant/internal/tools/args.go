// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from Home Assistant, a token or
// an address.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
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

func checkEntity(id string) error {
	if !policy.EntityID.MatchString(id) {
		return bad("entityId must look like light.kitchen")
	}
	return nil
}

func checkText(what, s string, max int) error {
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

// runes shortens s to n characters and says whether it cut.
func runes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	r := []rune(s)
	return string(r[:n]), true
}

func short(s string, n int) string { o, _ := runes(s, n); return o }
