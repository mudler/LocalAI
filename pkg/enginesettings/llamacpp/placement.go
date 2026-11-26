package llamacpp

import (
	"errors"

	"fmt"
	es "github.com/mudler/LocalAI/pkg/enginesettings"
	"regexp"
	"strconv"
	"strings"
)

const (
	allExpertsPattern = `\.ffn_.*_exps.*=CPU`
	layerPrefix       = `blk\.(`
	layerSuffix       = `)\.ffn_.*_exps.*=CPU`
)

// Pattern returns the "regex=CPU" text of a placement.
func Pattern(p es.Placement) (string, error) {
	switch p.Mode {
	case es.ExpertsCPUAll:
		return allExpertsPattern, nil
	case es.ExpertsCPULayers:
		rs, err := es.NormalizeLayerRanges(p.Layers)
		if err != nil {
			return "", err
		}
		if len(rs) == 0 {
			return "", errors.New("list at least one layer")
		}
		return layerPrefix + rangesRegex(rs) + layerSuffix, nil
	case es.FirstLayersGPU:
		n := p.FirstGPULayers
		if n < 0 || n > es.MaxLayer {
			return "", fmt.Errorf("first_gpu_layers must be between 0 and %d", es.MaxLayer)
		}
		if p.TotalLayers != 0 {
			if p.TotalLayers < 0 || p.TotalLayers > es.MaxLayer+1 {
				return "", fmt.Errorf("total_layers must be between 1 and %d", es.MaxLayer+1)
			}
			if n >= p.TotalLayers {
				return "", errors.New("first_gpu_layers must be lower than total_layers: no layer is left for the CPU")
			}
			if n == 0 {
				return allExpertsPattern, nil
			}
			return layerPrefix + rangesRegex([]es.LayerRange{{From: n, To: p.TotalLayers - 1}}) + layerSuffix, nil
		}
		if n == 0 {
			return allExpertsPattern, nil
		}
		return layerPrefix + atLeastRegex(n) + layerSuffix, nil
	}
	return "", fmt.Errorf("unknown placement mode %q", p.Mode)
}

// Option returns the "-ot:pattern" entry of a placement for the model
// "options" list.
func Option(p es.Placement) (string, error) {
	pat, err := Pattern(p)
	if err != nil {
		return "", err
	}
	return "-ot:" + pat, nil
}

// ParseOption recognises an "-ot" options entry that this package generated.
// It returns false for any other pattern, which the caller shows as raw text.
func ParseOption(opt string) (es.Placement, bool) {
	name, pat, ok := strings.Cut(opt, ":")
	if !ok || (name != "-ot" && name != "--override-tensor") {
		return es.Placement{}, false
	}
	return ParsePattern(pat)
}

var layerGroupChars = regexp.MustCompile(`^[0-9\[\]\-|+]+$`)

// scanLimit is how many layer numbers ParsePattern tests a group against.
// It is above the highest layer, so an open-ended pattern reaches it.
const scanLimit = 4 * (es.MaxLayer + 1)

// ParsePattern recognises a pattern built by Placement.Pattern.
func ParsePattern(pat string) (es.Placement, bool) {
	if pat == allExpertsPattern {
		return es.Placement{Mode: es.ExpertsCPUAll}, true
	}
	if !strings.HasPrefix(pat, layerPrefix) || !strings.HasSuffix(pat, layerSuffix) ||
		len(pat) <= len(layerPrefix)+len(layerSuffix) {
		return es.Placement{}, false
	}
	group := pat[len(layerPrefix) : len(pat)-len(layerSuffix)]
	if len(group) > 2000 || !layerGroupChars.MatchString(group) {
		return es.Placement{}, false
	}
	re, err := regexp.Compile("^(?:" + group + ")$")
	if err != nil {
		return es.Placement{}, false
	}
	var set []int
	for n := 0; n < scanLimit; n++ {
		if re.MatchString(strconv.Itoa(n)) {
			set = append(set, n)
		}
	}
	if len(set) == 0 {
		return es.Placement{}, false
	}
	if set[len(set)-1] == scanLimit-1 {
		// Open-ended: all layers from set[0] up.
		p := es.Placement{Mode: es.FirstLayersGPU, FirstGPULayers: set[0]}
		if got, err := Pattern(p); err == nil && got == pat {
			return p, true
		}
		return es.Placement{}, false
	}
	var rs []es.LayerRange
	for _, n := range set {
		if k := len(rs); k > 0 && rs[k-1].To == n-1 {
			rs[k-1].To = n
		} else {
			rs = append(rs, es.LayerRange{From: n, To: n})
		}
	}
	p := es.Placement{Mode: es.ExpertsCPULayers, Layers: rs}
	if got, err := Pattern(p); err == nil && got == pat {
		return p, true
	}
	return es.Placement{}, false
}

func class(lo, hi byte) string {
	switch {
	case lo == hi:
		return string(lo)
	case hi == lo+1:
		return "[" + string(lo) + string(hi) + "]"
	}
	return "[" + string(lo) + "-" + string(hi) + "]"
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }

func allOf(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// sameLength returns alternatives that match exactly the numbers a..b, which
// have the same number of digits.
func sameLength(a, b string) []string {
	if len(a) == 1 {
		return []string{class(a[0], b[0])}
	}
	if a[0] == b[0] {
		var out []string
		for _, s := range sameLength(a[1:], b[1:]) {
			out = append(out, string(a[0])+s)
		}
		return out
	}
	var out, tail []string
	lo, hi := a[0], b[0]
	rest := len(a) - 1
	if !allOf(a[1:], '0') {
		for _, s := range sameLength(a[1:], repeat("9", rest)) {
			out = append(out, string(lo)+s)
		}
		lo++
	}
	if !allOf(b[1:], '9') {
		for _, s := range sameLength(repeat("0", rest), b[1:]) {
			tail = append(tail, string(hi)+s)
		}
		hi--
	}
	if lo <= hi {
		out = append(out, class(lo, hi)+repeat("[0-9]", rest))
	}
	return append(out, tail...)
}

func rangesRegex(rs []es.LayerRange) string {
	var alts []string
	for _, r := range rs {
		for l := len(strconv.Itoa(r.From)); l <= len(strconv.Itoa(r.To)); l++ {
			lo, hi := r.From, r.To
			if min := pow10(l - 1); l > 1 && lo < min {
				lo = min
			}
			if max := pow10(l) - 1; hi > max {
				hi = max
			}
			alts = append(alts, sameLength(strconv.Itoa(lo), strconv.Itoa(hi))...)
		}
	}
	return strings.Join(alts, "|")
}

func pow10(n int) int {
	r := 1
	for ; n > 0; n-- {
		r *= 10
	}
	return r
}

// atLeastRegex matches every layer number from n up, with no upper bound and
// without a comma (the "-ot" value is split on commas).
func atLeastRegex(n int) string {
	s := strconv.Itoa(n)
	var alts []string
	for i := 0; i < len(s); i++ {
		d := s[i]
		if i == len(s)-1 {
			alts = append(alts, s[:i]+class(d, '9'))
		} else if d < '9' {
			alts = append(alts, s[:i]+class(d+1, '9')+repeat("[0-9]", len(s)-i-1))
		}
	}
	// Longer numbers: a non-zero digit, then at least as many digits as n has.
	alts = append(alts, "[1-9]"+repeat("[0-9]", len(s)-1)+"[0-9]+")
	// Shorter-prefix alternatives were built from the last digit back; keep
	// them in ascending order of the numbers they match.
	for i, j := 0, len(alts)-2; i < j; i, j = i+1, j-1 {
		alts[i], alts[j] = alts[j], alts[i]
	}
	return strings.Join(alts, "|")
}
