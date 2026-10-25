package main

import (
	"strconv"
	"strings"
)

const (
	// defaultMaxSeries and defaultMaxPoints bound one request, so a single call
	// cannot ask for an unbounded batch. Both can be raised in the model options.
	defaultMaxSeries = 256
	defaultMaxPoints = 2_000_000
	// defaultThreads is used when LocalAI passes no thread budget.
	defaultThreads = 4
)

// defaultQuantiles are returned when a request names none.
var defaultQuantiles = []float64{0.1, 0.5, 0.9}

// loadOptions holds the parsed model-level options.
type loadOptions struct {
	defaultQuantiles []float64
	maxContext       int32
	maxSeries        int
	maxPoints        int
}

func splitOption(o string) (key, value string, ok bool) {
	i := strings.Index(o, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:]), true
}

// parseOptions reads the backend "key:value" option list. Unknown keys and
// malformed values are ignored and keep the default. Keys:
//
//	default_quantiles  comma list of levels, default 0.1,0.5,0.9
//	max_context        history cap used when a request sets none, 0 = model default
//	max_series         most series in one request
//	max_total_points   most input values in one request
func parseOptions(opts []string) loadOptions {
	o := loadOptions{
		defaultQuantiles: append([]float64(nil), defaultQuantiles...),
		maxSeries:        defaultMaxSeries,
		maxPoints:        defaultMaxPoints,
	}
	for _, oo := range opts {
		key, value, ok := splitOption(oo)
		if !ok {
			continue
		}
		switch key {
		case "default_quantiles":
			var levels []float64
			valid := true
			for _, part := range strings.Split(value, ",") {
				f, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
				if err != nil || f <= 0 || f >= 1 {
					valid = false
					break
				}
				levels = append(levels, f)
			}
			if valid && len(levels) > 0 {
				o.defaultQuantiles = levels
			}
		case "max_context":
			if n, err := strconv.ParseInt(value, 10, 32); err == nil && n >= 0 {
				o.maxContext = int32(n)
			}
		case "max_series":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				o.maxSeries = n
			}
		case "max_total_points":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				o.maxPoints = n
			}
		}
	}
	return o
}
