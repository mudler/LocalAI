package internal

import "fmt"

var Version = ""
var Commit = ""

func PrintableVersion() string {
	return fmt.Sprintf("LocalAI %s (%s)", Version, Commit)
}
