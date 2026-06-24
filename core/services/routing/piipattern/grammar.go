// Package piipattern is a bounded, restricted-regex matcher for high-entropy,
// highly-regular secrets (API keys, tokens, private-key blocks) that the NER
// PII tier cannot catch — it has no credential class, so it fragments a key
// into the nearest-looking trained categories and may leave the secret part
// exposed.
//
// The language is a deliberately restricted subset of regular expressions
// compiled to Go's RE2 engine (regexp), which is linear-time with no
// backtracking — there is no ReDoS class of failure. On top of RE2 we cap the
// pattern source length, the {n,m} expansion bound, the pattern count, and the
// scanned input, and we require every pattern to carry a fixed literal
// "anchor". The anchor rule is what admits `sk-ant-…` / `ghp_…` style keys
// while rejecting open-ended shapes like an email address or a bare `\w+`
// (which would match almost anything) — those stay with the NER tier.
//
// This package is a leaf: it imports only the standard library, so both
// core/config (validation at load) and core/application (the resolver) can use
// it without an import cycle.
package piipattern

import (
	"fmt"
	"regexp/syntax"
)

const (
	// MaxPatternLen caps the source length of a single pattern. Generous for a
	// credential shape, small enough that the compiled program stays tiny.
	MaxPatternLen = 256
	// MaxQuantifier caps an explicit {n,m} upper bound. RE2 expands a bounded
	// repeat into that many copies, so a large bound inflates the compiled
	// program. Go's regexp/syntax independently rejects any bound above 1000
	// at Parse time, so this cap MUST stay strictly below 1000 to be a live
	// guard rather than dead code shadowed by the parser: a bound in
	// (MaxQuantifier, 1000] reaches walk and is rejected here with an
	// actionable error, while >1000 is caught earlier by Parse. 512 is far
	// larger than any real credential token yet keeps the guard meaningful and
	// is defence in depth should the stdlib cap ever rise. Unbounded {n,} (no
	// upper) is a loop, not an expansion, and is allowed.
	MaxQuantifier = 512
	// MaxAlternation caps the arms of a single `a|b|c` alternation.
	MaxAlternation = 64
	// MaxAST bounds recursion depth so a pathologically nested pattern can't
	// blow the stack during validation.
	MaxAST = 64
	// MinAnchorLen is the shortest fixed literal run a pattern must contain to
	// be considered "anchored" to a recognisable secret prefix/shape.
	MinAnchorLen = 3
)

// parseFlags enables Perl character classes (\w \d \s) and word boundaries,
// matching what regexp.Compile uses, so validation and compilation agree.
const parseFlags = syntax.Perl

// ValidatePattern reports whether src is an acceptable restricted-subset
// pattern. It returns a descriptive error naming the offending construct so an
// operator editing a model config gets actionable feedback (the error is
// surfaced by config Validate at load and by the resolver, which fails closed).
func ValidatePattern(src string) error {
	if src == "" {
		return fmt.Errorf("pattern is empty")
	}
	if len(src) > MaxPatternLen {
		return fmt.Errorf("pattern is too long (%d chars; max %d)", len(src), MaxPatternLen)
	}
	re, err := syntax.Parse(src, parseFlags)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}
	if err := walk(re, 0); err != nil {
		return err
	}
	if anchorLen(re) < MinAnchorLen {
		return fmt.Errorf("pattern must contain a fixed literal run of at least %d characters "+
			"(e.g. \"sk-ant-\", \"ghp_\", \"AKIA\") so it is anchored to a recognisable secret; "+
			"open-ended shapes like emails or bare \\w+ belong to the NER tier", MinAnchorLen)
	}
	return nil
}

// walk enforces the allow-list of regex constructs.
func walk(re *syntax.Regexp, depth int) error {
	if depth > MaxAST {
		return fmt.Errorf("pattern is too deeply nested")
	}
	switch re.Op {
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return fmt.Errorf("'.' (any character) is not allowed; use an explicit class like [A-Za-z0-9]")
	case syntax.OpCapture:
		return fmt.Errorf("capturing groups are not allowed; use a non-capturing group (?:…) if you need grouping")
	case syntax.OpRepeat:
		if re.Min > MaxQuantifier || (re.Max >= 0 && re.Max > MaxQuantifier) {
			return fmt.Errorf("{n,m} bound is too large (max %d)", MaxQuantifier)
		}
	case syntax.OpAlternate:
		if len(re.Sub) > MaxAlternation {
			return fmt.Errorf("too many alternation arms (%d; max %d)", len(re.Sub), MaxAlternation)
		}
	case syntax.OpLiteral, syntax.OpCharClass, syntax.OpConcat,
		syntax.OpStar, syntax.OpPlus, syntax.OpQuest,
		syntax.OpEmptyMatch,
		syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		// allowed
	default:
		return fmt.Errorf("unsupported construct in pattern")
	}
	for _, sub := range re.Sub {
		if err := walk(sub, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// anchorLen returns the number of fixed (non-space) literal characters every
// match of re is guaranteed to contain — the pattern's "anchor strength".
// Concatenation sums its parts; alternation takes the min (every arm must
// carry the anchor); a `+`/{n,} with n>=1 contributes its body's literal once;
// `*`, `?`, {0,m} and char classes/anchors contribute 0 (they may be absent).
//
// We sum rather than measure the longest contiguous run because RE2 factors
// common prefixes — `(?:ghp|gho|ghs)_…` parses to `gh[ops]_…`, whose longest
// contiguous literal is only "gh" (2) but whose guaranteed literals are
// "gh"+"_" (3). Summing keeps such real key prefixes admissible while still
// rejecting open-ended shapes: an email `[\w.]+@[\w.]+\.\w+` guarantees only
// `@` and `.` (2 < MinAnchorLen).
func anchorLen(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		n := 0
		for _, r := range re.Rune {
			if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
				n++
			}
		}
		return n
	case syntax.OpConcat:
		sum := 0
		for _, sub := range re.Sub {
			sum += anchorLen(sub)
		}
		return sum
	case syntax.OpAlternate:
		if len(re.Sub) == 0 {
			return 0
		}
		min := -1
		for _, sub := range re.Sub {
			if a := anchorLen(sub); min < 0 || a < min {
				min = a
			}
		}
		return min
	case syntax.OpPlus:
		if len(re.Sub) == 1 {
			return anchorLen(re.Sub[0])
		}
		return 0
	case syntax.OpRepeat:
		if re.Min >= 1 && len(re.Sub) == 1 {
			return anchorLen(re.Sub[0])
		}
		return 0
	default:
		// char classes, anchors, OpStar, OpQuest carry no guaranteed literal.
		return 0
	}
}
