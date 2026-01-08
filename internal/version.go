package internal

import "fmt"

var Version = ""
var Commit = ""

func PrintableVersion() string {
	return fmt.Sprintf("%s (%s)", Version, Commit)
}
