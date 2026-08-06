package internal

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestUserAgent(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved })

	platform := fmt.Sprintf("(%s; %s)", runtime.GOOS, runtime.GOARCH)

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{
			name:    "source build without a stamped version",
			version: "",
			want:    "LocalAI " + platform,
		},
		{
			name:    "released build",
			version: "v3.2.1",
			want:    "LocalAI/v3.2.1 " + platform,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			if got := UserAgent(); got != tt.want {
				t.Errorf("UserAgent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The platform suffix is what distinguishes a real build from the bare
// fallback, so assert it is genuinely present rather than trusting only the
// composed strings above — those would still pass if the format string and
// the expectation drifted together.
func TestUserAgentAlwaysCarriesPlatform(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved })

	for _, v := range []string{"", "v9.9.9"} {
		Version = v
		got := UserAgent()
		if !strings.Contains(got, runtime.GOOS) || !strings.Contains(got, runtime.GOARCH) {
			t.Errorf("UserAgent() = %q, missing GOOS %q or GOARCH %q", got, runtime.GOOS, runtime.GOARCH)
		}
	}
}
