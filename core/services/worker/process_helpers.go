package worker

import (
	"os"
	"strings"
)

// readLastLinesFromFile reads the last n lines from a file.
// Returns an empty string if the file cannot be read.
func readLastLinesFromFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
