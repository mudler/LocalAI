package piipattern

import (
	"fmt"
	"regexp"
)

const (
	// MaxPatternsPerMatcher bounds how many patterns one detector may hold.
	MaxPatternsPerMatcher = 128
	// MaxMatchesPerPattern bounds matches emitted per pattern per call, so a
	// pathological input can't produce an unbounded result set.
	MaxMatchesPerPattern = 1000
)

// Pattern is one compiled-ready rule: matches are reported under Group, and a
// match shorter than MinLen bytes is dropped (0 = no floor).
type Pattern struct {
	Group   string
	Pattern string
	MinLen  int
}

// Match is one detected span: a half-open byte range [Start,End) into the
// scanned text, the matched text, and the reporting Group.
type Match struct {
	Group string
	Start int
	End   int
	Text  string
}

type compiled struct {
	group  string
	re     *regexp.Regexp
	minLen int
}

// Matcher holds a set of compiled patterns and scans text for all of them.
type Matcher struct {
	pats []compiled
}

// NewMatcher compiles the named built-ins plus the custom patterns into a
// Matcher. Unknown built-in names and patterns that fail the restricted grammar
// are reported as errors (the caller fails closed). Built-in and custom counts
// together may not exceed MaxPatternsPerMatcher.
func NewMatcher(builtinNames []string, custom []Pattern) (*Matcher, error) {
	if len(builtinNames)+len(custom) > MaxPatternsPerMatcher {
		return nil, fmt.Errorf("too many patterns (%d; max %d)", len(builtinNames)+len(custom), MaxPatternsPerMatcher)
	}
	m := &Matcher{}
	for _, name := range builtinNames {
		b, ok := LookupBuiltin(name)
		if !ok {
			return nil, fmt.Errorf("unknown built-in pattern %q", name)
		}
		re, err := Compile(b.Pattern)
		if err != nil {
			return nil, fmt.Errorf("built-in %q: %w", name, err)
		}
		m.pats = append(m.pats, compiled{group: b.Group, re: re})
	}
	for _, p := range custom {
		if p.Group == "" {
			return nil, fmt.Errorf("custom pattern is missing a name/group")
		}
		re, err := Compile(p.Pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p.Group, err)
		}
		m.pats = append(m.pats, compiled{group: p.Group, re: re, minLen: p.MinLen})
	}
	return m, nil
}

// Find returns every match of every pattern over text. Spans from different
// patterns may overlap; the caller (the redactor) unions and resolves them.
func (m *Matcher) Find(text string) []Match {
	if m == nil || text == "" {
		return nil
	}
	var out []Match
	for _, p := range m.pats {
		locs := p.re.FindAllStringIndex(text, MaxMatchesPerPattern)
		for _, loc := range locs {
			start, end := loc[0], loc[1]
			if end-start < p.minLen {
				continue
			}
			out = append(out, Match{
				Group: p.group,
				Start: start,
				End:   end,
				Text:  text[start:end],
			})
		}
	}
	return out
}
