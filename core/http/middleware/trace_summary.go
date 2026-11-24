// SPDX-License-Identifier: MIT

package middleware

import (
	"math"
	"slices"
	"time"
)

// TraceSummary is the counted view of the trace buffer.
//
// It exists so a caller that wants "how many, how many failed, how slow" does
// not have to fetch every exchange and count them in the browser. The Operate
// overview needs exactly those three numbers, and the trace list is capped in
// the thousands, so shipping it across the wire to produce a single integer is
// waste that grows with the buffer.
type TraceSummary struct {
	Total       int           `json:"total"`
	Errors      int           `json:"errors"`
	P95Millis   int64         `json:"p95_ms"`
	WindowHours int           `json:"window_hours"`
	Buckets     []TraceBucket `json:"buckets"`
}

// TraceBucket is one column of a sparkline: oldest first, so the series reads
// left to right the way a chart is drawn.
type TraceBucket struct {
	Start  time.Time `json:"start"`
	Count  int       `json:"count"`
	Errors int       `json:"errors"`
}

// GetTracesSummary counts the buffered exchanges over the given window.
func GetTracesSummary(window time.Duration, buckets int) TraceSummary {
	return summarize(GetTraces(), window, buckets)
}

func summarize(traces []APIExchange, window time.Duration, buckets int) TraceSummary {
	if buckets < 1 {
		buckets = 1
	}
	now := time.Now()
	cutoff := now.Add(-window)

	summary := TraceSummary{
		WindowHours: int(window.Hours()),
		// Never nil: a nil slice serialises as null and breaks .map() on the
		// other side, which is a silent runtime error rather than an empty chart.
		Buckets: make([]TraceBucket, buckets),
	}

	bucketWidth := window / time.Duration(buckets)
	for i := range summary.Buckets {
		summary.Buckets[i].Start = cutoff.Add(time.Duration(i) * bucketWidth)
	}

	durations := make([]time.Duration, 0, len(traces))
	for _, t := range traces {
		if t.Timestamp.Before(cutoff) {
			continue
		}
		summary.Total++
		failed := isFailure(t)
		if failed {
			summary.Errors++
		}
		durations = append(durations, t.Duration)

		// Clamp rather than skip: a request timestamped a hair in the future
		// (clock skew, or arriving mid-call) still belongs in the newest column.
		idx := int(t.Timestamp.Sub(cutoff) / bucketWidth)
		if idx >= buckets {
			idx = buckets - 1
		}
		if idx < 0 {
			idx = 0
		}
		summary.Buckets[idx].Count++
		if failed {
			summary.Buckets[idx].Errors++
		}
	}

	summary.P95Millis = percentileMillis(durations, 0.95)
	return summary
}

// A 4xx is the caller getting it wrong, which is not the installation being
// unhealthy. Only 5xx and a transport-level error count against the runtime.
func isFailure(t APIExchange) bool {
	return t.Error != "" || t.Response.Status >= 500
}

func percentileMillis(durations []time.Duration, p float64) int64 {
	if len(durations) == 0 {
		return 0
	}
	slices.Sort(durations)
	// Nearest-rank: the smallest value at or above the pth percentile.
	rank := int(math.Ceil(p*float64(len(durations)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(durations) {
		rank = len(durations) - 1
	}
	return durations[rank].Milliseconds()
}
