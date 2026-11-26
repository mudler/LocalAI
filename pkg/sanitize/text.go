package sanitize

import (
	"regexp"
	"strings"
	"unicode"
)

// Text makes an error or status text safe to store and to show to anyone who
// can read it: a person in the UI or the inbox, a browser push service, or a
// model through a tool. It drops control characters, terminal escapes and
// markup, puts the text on one line, shortens absolute paths to their file
// name (WithoutPaths), removes what looks like a credential (Redact), and
// caps the result at max runes, the ellipsis included. Applying it again to
// its own output returns the same text in the usual case.
func Text(s string, max int) string {
	s = stripControl(s)
	s = stripMarkup(s)
	s = strings.Join(strings.Fields(s), " ")
	return Cap(Redact(WithoutPaths(s)), max)
}

// Cap cuts s to at most max runes, the ellipsis included. A max of zero or
// less leaves s as it is.
func Cap(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// OneLine puts s on one line (every run of white space becomes one space,
// control characters are dropped) and caps it at max runes.
func OneLine(s string, max int) string {
	return Cap(strings.Join(strings.Fields(stripControl(s)), " "), max)
}

// JoinLines puts the output of a process on one line: terminal escapes,
// control characters other than a tab, format characters (bidirectional
// overrides, zero width characters) and invalid UTF-8 are dropped, each line
// is trimmed, and the lines that are not empty are joined with "; ". It
// neither caps nor redacts: the caller decides how much to keep and cleans it
// further.
func JoinLines(s string) string {
	s = ansiEscape.ReplaceAllString(strings.ToValidUTF8(s, ""), "")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Map(func(r rune) rune {
			if r == unicode.ReplacementChar || unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && r != '\t') {
				return -1
			}
			return r
		}, line)
		if line = strings.TrimSpace(line); line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "; ")
}

var (
	ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[@-Z\\-_])`)
	markupTag  = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(?:\s[^<>]*)?/?>`)
)

// stripControl drops terminal escapes, invalid UTF-8, every control character
// but white space, and format characters: a bidirectional override or a zero
// width character can make a text read differently from what it says.
func stripControl(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = ansiEscape.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && !unicode.IsSpace(r)) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// stripMarkup drops HTML-like tags. "<nil>", which Go prints for a nil value,
// is kept.
func stripMarkup(s string) string {
	return markupTag.ReplaceAllStringFunc(s, func(tag string) string {
		if tag == "<nil>" {
			return tag
		}
		return ""
	})
}

// absPath matches an absolute path (POSIX, Windows with a drive letter, or a
// home directory "~/..." or "~user/...") and keeps its last element.
var absPath = regexp.MustCompile(`(?:[A-Za-z]:|~[A-Za-z0-9._-]*)?(?:[\\/][^\s\\/:"'()]+)+[\\/]([^\s\\/:"'()]+)`)

// WithoutPaths replaces each absolute path in s by its file name, so a text
// does not tell the layout of the server. A path counts only at the start of
// the text or after white space, a quote, a parenthesis or an equals sign, so
// a URL, a scheme-less URL and a relative path are left alone.
func WithoutPaths(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range absPath.FindAllStringSubmatchIndex(s, -1) {
		start := m[0]
		if start > 0 && !strings.ContainsRune(" \t\r\n\"'(=", rune(s[start-1])) {
			// The tail of a longer token: the path of a URL, a relative path
			// ("models/sub/x.gguf") or a scheme-less URL. Only a path that
			// starts a word is absolute.
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(s[m[2]:m[3]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

var (
	userinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
	// secretKV is a key that names a credential and its value. The key starts
	// the text or follows a character that is not a letter or a digit, and it
	// may carry a prefix joined by "_" or "-" (HF_TOKEN, OPENAI_API_KEY,
	// db_password): "_" is a word character, so a word boundary would miss
	// those. A quote may close the key, as in JSON.
	secretKV = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])((?:[A-Za-z0-9]+[_-])*(?:token|secret|password|passwd|api[_-]?key|apikey|access[_-]?key|authorization|credential)s?)(["']?)(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|[^\s,;}]+)`)
	// A bearer credential of any length; "basic" only before something that
	// looks like base64, since it is also a common word.
	bearer = regexp.MustCompile(`(?i)\b(bearer)\s+[^\s"',;]+|\b(basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// tokenShape is a credential by its shape alone: GitHub, Hugging Face,
	// OpenAI style, Slack and GitLab tokens, an AWS access key ID and a JWT.
	tokenShape = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}|hf_[A-Za-z0-9]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|glpat-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`)
	digitsOnly = regexp.MustCompile(`^[0-9]+$`)
)

// Redact removes what looks like a credential from s: the userinfo of a URL,
// a bearer or basic credential, the value after a key such as token=,
// password:, HF_TOKEN= or "api_key": in JSON, and a token known by its shape.
// A value that is only digits is kept: it is a count (max_tokens=512), not a
// credential.
func Redact(s string) string {
	s = userinfo.ReplaceAllString(s, "${1}")
	s = bearer.ReplaceAllString(s, "${1}${2} [removed]")
	s = secretKV.ReplaceAllStringFunc(s, func(m string) string {
		g := secretKV.FindStringSubmatch(m)
		if digitsOnly.MatchString(g[5]) {
			return m
		}
		return g[1] + g[2] + g[3] + "=[removed]"
	})
	return tokenShape.ReplaceAllString(s, "[removed]")
}
