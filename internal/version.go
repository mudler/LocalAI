package internal

import (
	"fmt"
	"runtime"
)

var Version = ""
var Commit = ""

func PrintableVersion() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}

// UserAgent returns the version-aware client identity used for outbound
// requests to registries and galleries.
//
// The OS/arch suffix follows ordinary HTTP client convention (apt, pip and
// docker all send the equivalent) and rides on requests LocalAI already makes.
// It discloses nothing a registry cannot already infer: pulling a linux/amd64
// manifest reveals the same thing.
//
// An empty Version means a source build, which is worth being able to tell
// apart from a released one when reading server logs.
func UserAgent() string {
	platform := fmt.Sprintf("(%s; %s)", runtime.GOOS, runtime.GOARCH)
	if Version == "" {
		return "LocalAI " + platform
	}
	return fmt.Sprintf("LocalAI/%s %s", Version, platform)
}
