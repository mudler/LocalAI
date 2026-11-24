package studio

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The model names a connection by a handle: the display name of the binding
// folded to lower case letters, digits and hyphens. The handle is what the
// `connection` argument of a tool accepts. The display name stays in the
// description of the argument, so the model can relate the two.

// ConnectionArgument is the name of the argument the host adds to every tool
// of an add-on that runs with a credential when the chat enables two or more
// connections for it. The add-on never sees it.
const ConnectionArgument = "connection"

// maxHandleRunes bounds a handle.
const maxHandleRunes = 40

// maxNameInSchema bounds a display name in the description of the argument.
const maxNameInSchema = 60

// ConnectionHandle folds a display name to its handle. Letters and digits of
// any script are kept, lower-cased; any run of other characters becomes one
// hyphen. The result is empty for a name with no letter or digit.
func ConnectionHandle(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(unicode.ToLower(r))
		default:
			dash = true
		}
		if utf8.RuneCountInString(b.String()) >= maxHandleRunes {
			break
		}
	}
	return b.String()
}

// assignHandles gives each connection of list its handle. A name whose handle
// is already taken by an earlier connection (data from before names had to be
// unique) gets a numeric suffix, so the handles of one list are always
// distinct. The order of list decides who keeps the plain handle.
func assignHandles(list []Connection) []Connection {
	out := make([]Connection, len(list))
	taken := make(map[string]bool, len(list))
	for i, c := range list {
		h := ConnectionHandle(c.Name)
		if h == "" {
			h = "connection"
		}
		base := h
		for n := 2; taken[h]; n++ {
			h = base + "-" + strconv.Itoa(n)
		}
		taken[h] = true
		c.Handle = h
		out[i] = c
	}
	return out
}

// plainName is a display name made safe for the description of the argument:
// one line, no control characters, bounded.
func plainName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > maxNameInSchema {
		name = string([]rune(name)[:maxNameInSchema]) + "..."
	}
	return name
}

// allowedNames lists the connections of a set for the model: handle and
// display name, as `work ("Work account")`.
func allowedNames(items []Connection) string {
	parts := make([]string, len(items))
	for i, c := range items {
		parts[i] = fmt.Sprintf("%s (%q)", c.Handle, plainName(c.Name))
	}
	return strings.Join(parts, ", ")
}

// withConnectionArgument adds the `connection` argument to the input schema of
// a tool. It is an enum of the handles of items, and required. The second
// result is false when the schema is not an object schema or already has an
// argument of that name: such a tool is not offered with several connections,
// since the host could not tell its own argument from the add-on's.
func withConnectionArgument(schema json.RawMessage, items []Connection) (json.RawMessage, bool) {
	var s map[string]any
	if len(schema) == 0 {
		s = map[string]any{"type": "object"}
	} else if json.Unmarshal(schema, &s) != nil || s == nil {
		return nil, false
	}
	if t, ok := s["type"].(string); ok && t != "object" {
		return nil, false
	}
	props, _ := s["properties"].(map[string]any)
	if _, taken := props[ConnectionArgument]; taken {
		return nil, false
	}
	if props == nil {
		props = map[string]any{}
	}
	enum := make([]string, len(items))
	for i, c := range items {
		enum[i] = c.Handle
	}
	props[ConnectionArgument] = map[string]any{
		"type": "string",
		"enum": enum,
		"description": "The connection to use, by name. The person enabled only these for this chat: " + allowedNames(items) +
			". Choose the one that matches what the person asked; if it is unclear which one, ask them.",
	}
	s["properties"] = props
	req, _ := s["required"].([]any)
	s["required"] = append(req, ConnectionArgument)
	out, err := json.Marshal(s)
	return out, err == nil
}

// pickConnection resolves the value of the `connection` argument against the
// connections the chat enabled. It matches a handle, or a display name when
// the match is unambiguous, ignoring case. Only members of items can be
// returned: nothing the model writes widens the set.
func pickConnection(items []Connection, v string) (Connection, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return Connection{}, false
	}
	for _, c := range items {
		if strings.EqualFold(c.Handle, v) {
			return c, true
		}
	}
	var hit []Connection
	for _, c := range items {
		if strings.EqualFold(strings.TrimSpace(c.Name), v) {
			hit = append(hit, c)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return Connection{}, false
}

// connectionSentence is what the model reads for a missing or wrong name.
func connectionSentence(items []Connection, given string, present bool) string {
	switch {
	case len(items) == 0:
		return "No connection is enabled for this add-on in this chat, so nothing was sent. Tell the person to enable one in the Tools menu of this chat. Do not report an outage."
	case !present:
		return "Nothing was sent: the connection argument is required. Use one of: " + allowedNames(items) + "."
	}
	return "Nothing was sent: this chat does not have a connection named " + strconv.Quote(plainName(given)) + " enabled for this add-on. Use one of: " + allowedNames(items) + "."
}
