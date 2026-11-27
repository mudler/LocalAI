package config

import (
	"regexp"
	"testing"
)

// BenchmarkValidateBackendName checks a backend name as Validate does for
// every request: compiling the pattern each time, as before, and with the
// compiled pattern.
func BenchmarkValidateBackendName(b *testing.B) {
	b.Run("compile-per-call", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = regexp.MustCompile(`^[a-zA-Z0-9-_]+$`).MatchString("llama-cpp")
		}
	})
	b.Run("compiled", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = backendNameRe.MatchString("llama-cpp")
		}
	})
}
