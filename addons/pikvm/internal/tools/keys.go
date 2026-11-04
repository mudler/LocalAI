// SPDX-License-Identifier: MIT
package tools

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxKeys is the most keys one send-key call presses.
const MaxKeys = 8

// key is one allowed key or chord: the name a model writes and the name kvmd
// knows (web_name in its keymap, see https://docs.pikvm.org/api/).
type key struct {
	label string
	kvmd  []string
}

var allowedKeys = buildKeys()

func buildKeys() map[string]key {
	m := map[string]key{}
	add := func(label string, kvmd ...string) { m[label] = key{label: label, kvmd: kvmd} }
	for c := 'a'; c <= 'z'; c++ {
		add(string(c), "Key"+strings.ToUpper(string(c)))
	}
	for d := 0; d <= 9; d++ {
		add(strconv.Itoa(d), "Digit"+strconv.Itoa(d))
	}
	for n := 1; n <= 12; n++ {
		add("f"+strconv.Itoa(n), "F"+strconv.Itoa(n))
	}
	add("up", "ArrowUp")
	add("down", "ArrowDown")
	add("left", "ArrowLeft")
	add("right", "ArrowRight")
	add("enter", "Enter")
	add("escape", "Escape")
	add("tab", "Tab")
	// The only chords. Anything with the Windows or Meta key is not here.
	add("ctrl+alt+delete", "ControlLeft", "AltLeft", "Delete")
	add("ctrl+c", "ControlLeft", "KeyC")
	add("ctrl+z", "ControlLeft", "KeyZ")
	return m
}

var keyAliases = map[string]string{"esc": "escape", "return": "enter", "arrowup": "up", "arrowdown": "down", "arrowleft": "left", "arrowright": "right",
	"control+alt+delete": "ctrl+alt+delete", "ctrl+alt+del": "ctrl+alt+delete", "control+c": "ctrl+c", "control+z": "ctrl+z"}

// lookupKey finds an allowed key by the name a model wrote.
func lookupKey(name string) (key, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "")
	if a, ok := keyAliases[n]; ok {
		n = a
	}
	k, ok := allowedKeys[n]
	return k, ok
}

// AllowedKeys lists the names, for the tool description and the page.
func AllowedKeys() []string {
	return []string{"a-z", "0-9", "up", "down", "left", "right", "enter", "escape", "tab", "f1-f12", "ctrl+alt+delete", "ctrl+c", "ctrl+z"}
}

var (
	// A very long run of base64-like characters: a key, a token or a blob.
	longBlob = regexp.MustCompile(`[A-Za-z0-9+/=_-]{80,}`)
	// Well-known secret shapes.
	secretShape = regexp.MustCompile(`AKIA[0-9A-Z]{16}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|(ssh-(rsa|ed25519)|ecdsa-sha2-\S+) AAAA`)
	// A line of an exported password list: "password: ..." and the like.
	dumpLine = regexp.MustCompile(`(?im)^\s*"?(password|passwd|pwd|username|user name|login|url|totp|secret)"?\s*[:=,]`)
)

// checkTypedText refuses text that a model should not type into a machine.
// The reasons are fixed sentences: they never hold the text.
func checkTypedText(s string, limit int) error {
	if s == "" {
		return bad("text is empty")
	}
	if !utf8.ValidString(s) {
		return bad("text is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n > limit {
		return bad("text has %d characters; this device allows at most %d", n, limit)
	}
	for _, r := range s {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == ' ' || r == ' ') {
			return bad("text has a control character; only newline and tab are allowed")
		}
	}
	if strings.Contains(s, "-----BEGIN") || longBlob.MatchString(s) || secretShape.MatchString(s) || len(dumpLine.FindAllString(s, -1)) >= 3 {
		return bad("the text looks like a private key, a token or a list of passwords; this add-on does not type those")
	}
	return nil
}
