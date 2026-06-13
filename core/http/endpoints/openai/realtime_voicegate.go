package openai

import "math"

// cosineDistance returns 1 - cosine_similarity, matching the voice registry's
// distance convention (lower = closer). Returns 1 (treated as "no match") for
// zero-length, mismatched, or zero-magnitude vectors.
func cosineDistance(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return float32(1 - dot/(math.Sqrt(na)*math.Sqrt(nb)))
}
