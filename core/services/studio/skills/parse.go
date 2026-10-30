package skills

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Limits of one SKILL.md.
const (
	// MaxFileSize is the largest SKILL.md that is read, in bytes.
	MaxFileSize = 64 << 10
	// MaxDescription is the longest description, in bytes after cleaning.
	MaxDescription = 1024
)

// Reasons that Parse returns. Their text is what the product shows as the
// reason a skill is invalid.
var (
	ErrNoFrontmatter      = errors.New("no frontmatter")
	ErrNoName             = errors.New("no name")
	ErrBadName            = errors.New("bad name")
	ErrNoDescription      = errors.New("no description")
	ErrDescriptionTooLong = errors.New("description too long")
	ErrTooLarge           = errors.New("too large")
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidName reports whether s can be the name of a skill.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// Parse reads the frontmatter of a SKILL.md and returns the name and the
// cleaned description.
//
// The frontmatter is the text between a first line "---" and the next line
// "---". Parse accepts the subset of YAML that skills use: "key: value"
// lines, values in single or double quotes, and a block scalar ("|" or ">",
// with an optional "-" or "+") for any key. Indented lines that follow a
// plain value, comments and unknown keys are ignored. The last value of a
// repeated key wins.
//
// The description loses control characters and invisible format characters
// (Unicode category Cf, which includes bidi overrides), and every run of
// white space becomes one space. The error is one of the Err values of this
// package.
func Parse(data []byte) (name, description string, err error) {
	if len(data) > MaxFileSize {
		return "", "", ErrTooLarge
	}
	fm, ok := frontmatter(string(data))
	if !ok {
		return "", "", ErrNoFrontmatter
	}
	kv := keyValues(fm)
	name = strings.TrimSpace(kv["name"])
	if name == "" {
		return "", "", ErrNoName
	}
	if !nameRE.MatchString(name) {
		return "", "", ErrBadName
	}
	description = Clean(kv["description"])
	switch {
	case description == "":
		return "", "", ErrNoDescription
	case len(description) > MaxDescription:
		return "", "", ErrDescriptionTooLong
	}
	return name, description, nil
}

// Capabilities a skill can require. A product that cannot give one marks the
// skill unavailable instead of listing it as usable.
const (
	// RequireExec is a way to run commands or code (a sandbox).
	RequireExec = "exec"
)

var capabilityRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Requires reads the optional "requires" key of the frontmatter: the
// capabilities the procedure needs, as a flow list ("[exec]") or words split
// by commas or spaces. Values that are not capability words are dropped, so a
// typo never hides a skill. A file without frontmatter requires nothing.
func Requires(data []byte) []string {
	if len(data) > MaxFileSize {
		return nil
	}
	fm, ok := frontmatter(string(data))
	if !ok {
		return nil
	}
	v := strings.TrimSpace(keyValues(fm)["requires"])
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		f = strings.ToLower(strings.Trim(f, `"'`))
		if capabilityRE.MatchString(f) && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// Clean drops invalid UTF-8, control characters and format characters from s,
// turns every run of white space into one space and trims the ends.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	b.Grow(len(s))
	pending := false // a space is owed before the next visible rune
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			pending = true
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf):
			// dropped
		default:
			if pending && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pending = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

// frontmatter returns the lines between the first "---" line and the next one.
func frontmatter(s string) ([]string, bool) {
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return nil, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return lines[1:i], true
		}
	}
	return nil, false
}

var keyRE = regexp.MustCompile(`^([A-Za-z0-9_-]+):(?:[ \t]+(.*)|[ \t]*)$`)

// keyValues reads the top-level "key: value" lines.
func keyValues(lines []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		m := keyRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		if style, ok := blockStyle(val); ok {
			var block []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) != "" && next[0] != ' ' && next[0] != '\t' {
					break
				}
				block = append(block, strings.TrimSpace(next))
				i++
			}
			sep := "\n"
			if style == '>' {
				sep = " "
			}
			out[key] = strings.Join(block, sep)
			continue
		}
		out[key] = unquote(val)
	}
	return out
}

// blockStyle reports whether v starts a block scalar: "|" or ">" with an
// optional chomping sign.
func blockStyle(v string) (byte, bool) {
	switch v {
	case "|", "|-", "|+", ">", ">-", ">+":
		return v[0], true
	}
	return 0, false
}

// unquote removes one pair of matching quotes. A value with an unmatched
// quote is kept as written.
func unquote(v string) string {
	if len(v) < 2 || (v[0] != '"' && v[0] != '\'') || v[len(v)-1] != v[0] {
		return v
	}
	inner := v[1 : len(v)-1]
	if v[0] == '\'' {
		return strings.ReplaceAll(inner, "''", "'")
	}
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			switch inner[i+1] {
			case '"', '\\':
				b.WriteByte(inner[i+1])
				i++
				continue
			case 'n', 't':
				b.WriteByte(' ')
				i++
				continue
			}
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}
