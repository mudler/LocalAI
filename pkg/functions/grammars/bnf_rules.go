package grammars

import (
	"encoding/json"
	"regexp"
)

var (
	PRIMITIVE_RULES = map[string]string{
		"boolean": `("true" | "false") space`,
		"number":  `("-"? ([0-9] | [1-9] [0-9]*)) ("." [0-9]+)? ([eE] [-+]? [0-9]+)? space`,
		"integer": `("-"? ([0-9] | [1-9] [0-9]*)) space`,
		"string": `"\"" (
			[^"\\] |
			"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
		  )* "\"" space`,
		// TODO: we shouldn't forbid \" and \\ or all unicode and have this branch here,
		// however, if we don't have it, the grammar will be ambiguous and
		// empirically results are way worse.
		"freestring": `(
			[^\x00] |
			"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
		  )* space`,
		"null": `"null" space`,
	}

	INVALID_RULE_CHARS_RE     = regexp.MustCompile(`[^a-zA-Z0-9-]+`)
	GRAMMAR_LITERAL_ESCAPE_RE = regexp.MustCompile(`[\r\n"]`)
	GRAMMAR_LITERAL_ESCAPES   = map[string]string{
		"\r": `\r`,
		"\n": `\n`,
		`"`:  `\"`,
	}
)

const (
	SPACE_RULE = `" "?`

	arrayNewLines = `arr  ::=
  "[\n"  (
		realvalue
    (",\n"  realvalue)*
  )? "]"`

	array = `arr  ::=
  "["  (
		realvalue
    (","  realvalue)*
  )? "]"`
)

func jsonString(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
