package llamacppsettings

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PlacementMode names a tensor placement preset.
type PlacementMode string

const (
	// ExpertsCPUAll keeps the MoE expert tensors of every layer on the CPU.
	ExpertsCPUAll PlacementMode = "experts_cpu_all"
	// ExpertsCPULayers keeps the expert tensors of the listed layers on the CPU.
	ExpertsCPULayers PlacementMode = "experts_cpu_layers"
	// FirstLayersGPU keeps the first N layers entirely on the GPU and the
	// expert tensors of all later layers on the CPU.
	FirstLayersGPU PlacementMode = "first_layers_gpu"
)

// MaxLayer is the highest layer number a placement may name.
const MaxLayer = 999

// LayerRange is an inclusive range of layer numbers.
type LayerRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Placement is the structured form of an "-ot" pattern.
type Placement struct {
	Mode PlacementMode `json:"mode"`
	// Layers is used by experts_cpu_layers.
	Layers []LayerRange `json:"layers,omitempty"`
	// FirstGPULayers is used by first_layers_gpu.
	FirstGPULayers int `json:"first_gpu_layers,omitempty"`
	// TotalLayers is optional for first_layers_gpu. When set, the pattern
	// lists the remaining layers one range; when 0, it matches every layer
	// from FirstGPULayers up.
	TotalLayers int `json:"total_layers,omitempty"`
}

const (
	allExpertsPattern = `\.ffn_.*_exps.*=CPU`
	layerPrefix       = `blk\.(`
	layerSuffix       = `)\.ffn_.*_exps.*=CPU`
)

// Pattern returns the "regex=CPU" text of the placement.
func (p Placement) Pattern() (string, error) {
	switch p.Mode {
	case ExpertsCPUAll:
		return allExpertsPattern, nil
	case ExpertsCPULayers:
		rs, err := normalizeRanges(p.Layers)
		if err != nil {
			return "", err
		}
		if len(rs) == 0 {
			return "", errors.New("list at least one layer")
		}
		return layerPrefix + rangesRegex(rs) + layerSuffix, nil
	case FirstLayersGPU:
		n := p.FirstGPULayers
		if n < 0 || n > MaxLayer {
			return "", fmt.Errorf("first_gpu_layers must be between 0 and %d", MaxLayer)
		}
		if p.TotalLayers != 0 {
			if p.TotalLayers < 0 || p.TotalLayers > MaxLayer+1 {
				return "", fmt.Errorf("total_layers must be between 1 and %d", MaxLayer+1)
			}
			if n >= p.TotalLayers {
				return "", errors.New("first_gpu_layers must be lower than total_layers: no layer is left for the CPU")
			}
			if n == 0 {
				return allExpertsPattern, nil
			}
			return layerPrefix + rangesRegex([]LayerRange{{n, p.TotalLayers - 1}}) + layerSuffix, nil
		}
		if n == 0 {
			return allExpertsPattern, nil
		}
		return layerPrefix + atLeastRegex(n) + layerSuffix, nil
	}
	return "", fmt.Errorf("unknown placement mode %q", p.Mode)
}

// Option returns the "-ot:pattern" entry for the model "options" list.
func (p Placement) Option() (string, error) {
	pat, err := p.Pattern()
	if err != nil {
		return "", err
	}
	return "-ot:" + pat, nil
}

// ParseOption recognises an "-ot" options entry that this package generated.
// It returns false for any other pattern, which the caller shows as raw text.
func ParseOption(opt string) (Placement, bool) {
	name, pat, ok := strings.Cut(opt, ":")
	if !ok || (name != "-ot" && name != "--override-tensor") {
		return Placement{}, false
	}
	return ParsePattern(pat)
}

var layerGroupChars = regexp.MustCompile(`^[0-9\[\]\-|+]+$`)

// scanLimit is how many layer numbers ParsePattern tests a group against.
// It is above the highest layer, so an open-ended pattern reaches it.
const scanLimit = 4 * (MaxLayer + 1)

// ParsePattern recognises a pattern built by Placement.Pattern.
func ParsePattern(pat string) (Placement, bool) {
	if pat == allExpertsPattern {
		return Placement{Mode: ExpertsCPUAll}, true
	}
	if !strings.HasPrefix(pat, layerPrefix) || !strings.HasSuffix(pat, layerSuffix) ||
		len(pat) <= len(layerPrefix)+len(layerSuffix) {
		return Placement{}, false
	}
	group := pat[len(layerPrefix) : len(pat)-len(layerSuffix)]
	if len(group) > 2000 || !layerGroupChars.MatchString(group) {
		return Placement{}, false
	}
	re, err := regexp.Compile("^(?:" + group + ")$")
	if err != nil {
		return Placement{}, false
	}
	var set []int
	for n := 0; n < scanLimit; n++ {
		if re.MatchString(strconv.Itoa(n)) {
			set = append(set, n)
		}
	}
	if len(set) == 0 {
		return Placement{}, false
	}
	if set[len(set)-1] == scanLimit-1 {
		// Open-ended: all layers from set[0] up.
		p := Placement{Mode: FirstLayersGPU, FirstGPULayers: set[0]}
		if got, err := p.Pattern(); err == nil && got == pat {
			return p, true
		}
		return Placement{}, false
	}
	var rs []LayerRange
	for _, n := range set {
		if k := len(rs); k > 0 && rs[k-1].To == n-1 {
			rs[k-1].To = n
		} else {
			rs = append(rs, LayerRange{n, n})
		}
	}
	p := Placement{Mode: ExpertsCPULayers, Layers: rs}
	if got, err := p.Pattern(); err == nil && got == pat {
		return p, true
	}
	return Placement{}, false
}

// ParseLayerRanges reads "0-6,18-23" or "5". Ranges are sorted and merged.
func ParseLayerRanges(s string) ([]LayerRange, error) {
	var rs []LayerRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, isRange := strings.Cut(part, "-")
		from, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("%q is not a layer number or a range", part)
		}
		to := from
		if isRange {
			if to, err = strconv.Atoi(strings.TrimSpace(b)); err != nil {
				return nil, fmt.Errorf("%q is not a layer number or a range", part)
			}
		}
		rs = append(rs, LayerRange{from, to})
	}
	return normalizeRanges(rs)
}

// FormatLayerRanges is the inverse of ParseLayerRanges.
func FormatLayerRanges(rs []LayerRange) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.From == r.To {
			parts = append(parts, strconv.Itoa(r.From))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.From, r.To))
		}
	}
	return strings.Join(parts, ",")
}

// normalizeRanges validates, sorts and merges ranges. Ranges that overlap or
// touch become one.
func normalizeRanges(in []LayerRange) ([]LayerRange, error) {
	rs := append([]LayerRange(nil), in...)
	for _, r := range rs {
		if r.From < 0 || r.To > MaxLayer {
			return nil, fmt.Errorf("layers must be between 0 and %d", MaxLayer)
		}
		if r.From > r.To {
			return nil, fmt.Errorf("the range %d-%d runs backwards", r.From, r.To)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].From < rs[j].From })
	var out []LayerRange
	for _, r := range rs {
		if k := len(out); k > 0 && r.From <= out[k-1].To+1 {
			if r.To > out[k-1].To {
				out[k-1].To = r.To
			}
			continue
		}
		out = append(out, r)
	}
	return out, nil
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

func rangesRegex(rs []LayerRange) string {
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
