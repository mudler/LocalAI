package enginesettings

import (
	"errors"
	"strings"
)

// Tokenize splits a command line into words with POSIX shell quoting rules:
// single quotes keep everything, double quotes keep everything except
// backslash before $ ` " \ and newline, an unquoted backslash escapes the
// next character, and a backslash before a newline joins lines. Nothing is
// expanded or executed. The second result lists tokens that contain unquoted
// shell operators, which this package treats as plain text. It is the default tokenizer of a CommandLine.
func Tokenize(s string) (tokens []string, operators []string, err error) {
	if strings.ContainsRune(s, 0) {
		return nil, nil, errors.New("the command contains a NUL byte")
	}
	rs := []rune(s)
	var cur strings.Builder
	inTok, meta := false, false
	flush := func() {
		if inTok {
			tokens = append(tokens, cur.String())
			if meta {
				operators = append(operators, cur.String())
			}
		}
		cur.Reset()
		inTok, meta = false, false
	}
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		case c == '\\':
			if i+1 >= len(rs) {
				return nil, nil, errors.New("the command ends with a backslash")
			}
			i++
			if rs[i] == '\n' {
				continue
			}
			if rs[i] == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
				continue
			}
			cur.WriteRune(rs[i])
			inTok = true
		case c == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return nil, nil, errors.New("unterminated single quote")
			}
			cur.WriteString(string(rs[i+1 : j]))
			inTok = true
			i = j
		case c == '"':
			inTok = true
			j := i + 1
			closed := false
			for ; j < len(rs); j++ {
				d := rs[j]
				if d == '"' {
					closed = true
					break
				}
				if d == '\\' && j+1 < len(rs) {
					switch rs[j+1] {
					case '$', '`', '"', '\\':
						cur.WriteRune(rs[j+1])
						j++
						continue
					case '\n':
						j++
						continue
					}
				}
				cur.WriteRune(d)
			}
			if !closed {
				return nil, nil, errors.New("unterminated double quote")
			}
			i = j
		default:
			if c == ';' || c == '|' || c == '&' || c == '<' || c == '>' || c == '`' ||
				(c == '$' && i+1 < len(rs) && (rs[i+1] == '(' || rs[i+1] == '{')) {
				meta = true
			}
			cur.WriteRune(c)
			inTok = true
		}
	}
	flush()
	return tokens, operators, nil
}

// Quote writes a word so that Tokenize returns it unchanged.
func Quote(w string) string {
	if w == "" {
		return `""`
	}
	plain := true
	for _, c := range w {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("-_./:=,+@%", c)) {
			plain = false
			break
		}
	}
	if plain {
		return w
	}
	var b strings.Builder
	b.WriteByte('"')
	rs := []rune(w)
	for i, c := range rs {
		switch c {
		case '"', '$', '`':
			b.WriteByte('\\')
		case '\\':
			// A backslash only needs escaping before a character that
			// would turn it into an escape, or at the end of the word.
			if i+1 >= len(rs) || strings.ContainsRune("$`\"\\\n", rs[i+1]) {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(c)
	}
	b.WriteByte('"')
	return b.String()
}
