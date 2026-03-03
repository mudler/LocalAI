package sound

import "math"

// generateSineWave produces a sine wave of the given frequency at the given sample rate.
func generateSineWave(freq float64, sampleRate, numSamples int) []int16 {
	out := make([]int16, numSamples)
	for i := range out {
		t := float64(i) / float64(sampleRate)
		out[i] = int16(math.MaxInt16 / 2 * math.Sin(2*math.Pi*freq*t))
	}
	return out
}

// computeCorrelation returns the normalised Pearson correlation between two
// equal-length int16 slices. Returns 0 when either signal has zero energy.
func computeCorrelation(a, b []int16) float64 {
	n := len(a)
	if n == 0 || n != len(b) {
		return 0
	}
	var sumAB, sumA2, sumB2 float64
	for i := 0; i < n; i++ {
		fa, fb := float64(a[i]), float64(b[i])
		sumAB += fa * fb
		sumA2 += fa * fa
		sumB2 += fb * fb
	}
	denom := math.Sqrt(sumA2 * sumB2)
	if denom == 0 {
		return 0
	}
	return sumAB / denom
}

// estimateFrequency estimates the dominant frequency of a mono int16 signal
// using zero-crossing count.
func estimateFrequency(samples []int16, sampleRate int) float64 {
	if len(samples) < 2 {
		return 0
	}
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if (samples[i-1] >= 0 && samples[i] < 0) || (samples[i-1] < 0 && samples[i] >= 0) {
			crossings++
		}
	}
	duration := float64(len(samples)) / float64(sampleRate)
	// Each full cycle has 2 zero crossings.
	return float64(crossings) / (2 * duration)
}

// computeRMS returns the root-mean-square of an int16 slice.
func computeRMS(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// generatePCMBytes creates a little-endian int16 PCM byte slice containing a
// sine wave of the given frequency at the given sample rate and duration.
func generatePCMBytes(freq float64, sampleRate, durationMs int) []byte {
	numSamples := sampleRate * durationMs / 1000
	samples := generateSineWave(freq, sampleRate, numSamples)
	return Int16toBytesLE(samples)
}
