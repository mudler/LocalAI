// Package vrambudget parses an operator-set cap on how much VRAM LocalAI may
// use for model allocation on a node, and applies it as a hard ceiling.
//
// A budget is expressed as either a percentage of detected VRAM ("80%", "0.8")
// or an absolute amount ("12GB", "12GiB", raw bytes). The zero value is "no
// cap" so every existing deployment is unaffected until a budget is set.
package vrambudget

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Budget is an optional cap on allocatable VRAM. The zero value means no cap.
type Budget struct {
	fraction float64 // (0,1] when percentage form; 0 otherwise
	absolute uint64  // bytes when absolute form; 0 otherwise
}

// decimal (1000-based) and binary (1024-based) size suffixes, longest first so
// "GiB" is matched before "GB"/"B".
var sizeSuffixes = []struct {
	suffix string
	mult   uint64
}{
	{"KIB", 1 << 10}, {"MIB", 1 << 20}, {"GIB", 1 << 30}, {"TIB", 1 << 40},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"TB", 1000 * 1000 * 1000 * 1000},
	{"B", 1},
}

// Parse accepts "", "80%", "0.8", "1.0", "12GB", "12GiB", "12000MB", raw bytes.
// Empty / zero forms return an unset Budget with no error. A percentage above
// 100% (a cap that would raise VRAM) is a config error; an absolute value above
// physical VRAM is harmless and clamped later in Apply.
func Parse(s string) (Budget, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Budget{}, nil
	}
	upper := strings.ToUpper(s)

	// Percentage form: trailing '%'.
	if strings.HasSuffix(upper, "%") {
		num := strings.TrimSpace(strings.TrimSuffix(upper, "%"))
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return Budget{}, fmt.Errorf("invalid vram budget percentage %q: %w", s, err)
		}
		return fromFraction(v/100.0, s)
	}

	// Absolute form: any known size suffix.
	for _, sfx := range sizeSuffixes {
		if strings.HasSuffix(upper, sfx.suffix) {
			num := strings.TrimSpace(strings.TrimSuffix(upper, sfx.suffix))
			v, err := strconv.ParseFloat(num, 64)
			if err != nil || v < 0 {
				return Budget{}, fmt.Errorf("invalid vram budget %q", s)
			}
			return Budget{absolute: uint64(v * float64(sfx.mult))}, nil
		}
	}

	// Bare number: (0,1] is a fraction, anything else is absolute bytes.
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Budget{}, fmt.Errorf("invalid vram budget %q", s)
	}
	if v < 0 {
		return Budget{}, fmt.Errorf("invalid vram budget %q: negative", s)
	}
	if v > 0 && v <= 1 {
		return fromFraction(v, s)
	}
	// A bare value above 1 is a byte count and must be whole: a fractional
	// value >1 is neither a valid fraction (>100%) nor a sensible byte amount.
	if v != math.Trunc(v) {
		return Budget{}, fmt.Errorf("invalid vram budget %q: out of range", s)
	}
	return Budget{absolute: uint64(v)}, nil
}

func fromFraction(f float64, orig string) (Budget, error) {
	if f <= 0 {
		return Budget{}, nil // 0% == no cap
	}
	if f > 1 {
		return Budget{}, fmt.Errorf("vram budget %q exceeds 100%%", orig)
	}
	return Budget{fraction: f}, nil
}

// IsSet reports whether a cap is configured.
func (b Budget) IsSet() bool { return b.fraction > 0 || b.absolute > 0 }

// Ceiling resolves the budget to an absolute byte ceiling against detectedTotal,
// clamped to detectedTotal so an over-large absolute budget can't fabricate
// VRAM. Returns 0 when unset.
func (b Budget) Ceiling(detectedTotal uint64) uint64 {
	if !b.IsSet() {
		return 0
	}
	var ceil uint64
	if b.fraction > 0 {
		ceil = uint64(float64(detectedTotal) * b.fraction)
	} else {
		ceil = b.absolute
	}
	if ceil > detectedTotal {
		ceil = detectedTotal
	}
	return ceil
}

// Apply caps detected total/free against the budget. Returns the inputs
// unchanged when the budget is unset.
func (b Budget) Apply(detectedTotal, detectedFree uint64) (total, free uint64) {
	if !b.IsSet() {
		return detectedTotal, detectedFree
	}
	ceil := b.Ceiling(detectedTotal)
	total = min(detectedTotal, ceil)
	free = min(detectedFree, ceil)
	return total, free
}

// String returns the canonical config form ("80%" or "12GB"), or "" when unset.
func (b Budget) String() string {
	switch {
	case b.fraction > 0:
		return strconv.FormatFloat(b.fraction*100, 'f', -1, 64) + "%"
	case b.absolute > 0:
		return canonicalBytes(b.absolute)
	default:
		return ""
	}
}

// canonicalBytes renders bytes using the largest decimal suffix that divides
// evenly, falling back to a raw byte count.
func canonicalBytes(v uint64) string {
	for _, sfx := range []struct {
		suffix string
		mult   uint64
	}{
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
	} {
		if v%sfx.mult == 0 {
			return strconv.FormatUint(v/sfx.mult, 10) + sfx.suffix
		}
	}
	return strconv.FormatUint(v, 10) + "B"
}
