package pii

import (
	"fmt"
	"regexp"
	"strings"
)

// regexpMatcher is a thin wrapper so tests can swap in a deterministic
// matcher without touching the regexp package. Real usage uses
// regexpMatcherFromPattern; tests can construct fakes.
type regexpMatcher interface {
	FindAllStringIndex(s string, n int) [][]int
}

type goRegexp struct{ r *regexp.Regexp }

func (g goRegexp) FindAllStringIndex(s string, n int) [][]int {
	return g.r.FindAllStringIndex(s, n)
}

// DefaultPatterns returns the built-in regex set. Each entry includes
// a conservative MaxMatchLength so the streaming filter can size its
// tail buffer without re-parsing the regex at runtime.
//
// Caveats by design:
//   - The phone pattern matches international and US formats but does
//     not validate area codes. False positives on numbers that look
//     phone-like (e.g., timestamps in some formats) are accepted in
//     return for reliable coverage.
//   - The credit card pattern requires the Luhn check (verifyLuhn) to
//     reduce false positives — random 16-digit strings won't match.
//   - The API-key pattern targets common provider prefixes (sk-, pk-,
//     xoxb-, ghp_, github_pat_) rather than guessing entropy. Adding
//     new providers should append a new Pattern, not extend an
//     existing alternation, so the admin UI can show one row per
//     provider with its own toggle.
func DefaultPatterns() []Pattern {
	return []Pattern{
		{
			ID:             "email",
			Description:    "Email address",
			Action:         ActionMask,
			MaxMatchLength: 254, // RFC 5321 max
		},
		{
			ID:             "phone",
			Description:    "Phone number (international or US format)",
			Action:         ActionMask,
			MaxMatchLength: 24,
		},
		{
			ID:             "ssn",
			Description:    "US Social Security Number (NNN-NN-NNNN)",
			Action:         ActionMask,
			MaxMatchLength: 11,
		},
		{
			ID:             "credit_card",
			Description:    "Credit card number (Luhn-verified)",
			Action:         ActionMask,
			MaxMatchLength: 19,
		},
		{
			ID:             "ipv4",
			Description:    "IPv4 address",
			Action:         ActionMask,
			MaxMatchLength: 15,
		},
		{
			ID:             "api_key_prefix",
			Description:    "Common API key prefixes (sk-, pk-, xoxb-, ghp_, github_pat_)",
			Action:         ActionBlock, // tighter default — leaked credentials are higher harm
			MaxMatchLength: 200,
		},
	}
}

// patternRegexps maps Pattern.ID to its compiled regex. Kept separate
// from the Pattern struct so DefaultPatterns can be data-only and
// tests can swap matchers via Compile().
var patternRegexps = map[string]*regexp.Regexp{
	// Pragmatic email — does not implement RFC 5322 in full (no one
	// sane does in a regex). Catches the common shape; the encoder
	// NER tier (future) catches edge cases.
	"email": regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`),
	// US: (123) 456-7890, 123-456-7890, 123.456.7890, 1234567890.
	// International: +<country>-<area>-<rest> with separators.
	"phone": regexp.MustCompile(`(?:\+?\d{1,3}[\s\-.]?)?(?:\(\d{3}\)|\d{3})[\s\-.]?\d{3}[\s\-.]?\d{4}`),
	"ssn":   regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	// 13-19 digit Luhn-eligible runs. The verifier in match() rejects
	// non-Luhn matches.
	"credit_card": regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),
	"ipv4":        regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	// Common provider prefixes; each alternative is a separate
	// well-known marker rather than a permissive entropy match.
	"api_key_prefix": regexp.MustCompile(`(?:sk-[A-Za-z0-9]{20,}|pk-[A-Za-z0-9]{20,}|xoxb-[A-Za-z0-9\-]{20,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
}

// Compile attaches matchers to each pattern. Patterns whose ID is not
// in patternRegexps are returned as a typed error so an admin who
// adds a custom pattern via config gets a clear "no regex registered"
// message instead of silent skip.
func Compile(patterns []Pattern) ([]Pattern, error) {
	out := make([]Pattern, len(patterns))
	for i, p := range patterns {
		r, ok := patternRegexps[p.ID]
		if !ok {
			return nil, fmt.Errorf("pii: no regex registered for pattern id %q", p.ID)
		}
		p.regex = goRegexp{r: r}
		out[i] = p
	}
	return out, nil
}

// VerifyMatch applies pattern-specific post-checks (e.g. Luhn for
// credit_card). Returns the original match or "" to discard it.
func VerifyMatch(patternID, candidate string) string {
	switch patternID {
	case "credit_card":
		digits := stripNonDigits(candidate)
		if len(digits) < 13 || len(digits) > 19 {
			return ""
		}
		if !verifyLuhn(digits) {
			return ""
		}
	case "ipv4":
		// Each octet must be 0..255. The regex allows 0..999 since
		// regex isn't great at numeric ranges; we tighten here.
		for oct := range strings.SplitSeq(candidate, ".") {
			n := 0
			for _, c := range oct {
				if c < '0' || c > '9' {
					return ""
				}
				n = n*10 + int(c-'0')
			}
			if n > 255 {
				return ""
			}
		}
	}
	return candidate
}

func stripNonDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// verifyLuhn implements the Luhn checksum used by credit-card numbers.
// Returns true iff the digits pass.
func verifyLuhn(digits string) bool {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// MaxPatternLength returns the longest MaxMatchLength across the input
// patterns. Used by the streaming filter to size its tail buffer.
func MaxPatternLength(patterns []Pattern) int {
	max := 0
	for _, p := range patterns {
		if p.MaxMatchLength > max {
			max = p.MaxMatchLength
		}
	}
	return max
}
