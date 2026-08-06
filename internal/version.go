package internal

import "fmt"

var Version = ""
var Commit = ""

func PrintableVersion() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}

// UserAgent returns the version-aware client identity used for outbound requests.
func UserAgent() string {
	if Version == "" {
		return "LocalAI"
	}
	return fmt.Sprintf("LocalAI/%s", Version)
}
