package worker

import "strings"

// ParseNodeLabels parses a comma-separated `k=v,k=v` string into a map.
// Whitespace around keys, values, and pairs is trimmed; pairs without
// `=` are skipped silently.
func ParseNodeLabels(input string) map[string]string {
	labels := make(map[string]string)
	if input == "" {
		return labels
	}
	for _, pair := range strings.Split(input, ",") {
		pair = strings.TrimSpace(pair)
		if k, v, ok := strings.Cut(pair, "="); ok {
			labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return labels
}
