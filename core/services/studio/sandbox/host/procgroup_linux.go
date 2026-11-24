//go:build linux

package host

import (
	"os"
	"strconv"
	"strings"
)

// procStart returns a token that identifies the process pid by its start time,
// and ok=false when no such process exists. A reused pid has another token.
func procStart(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304 -- a pid
	if err != nil {
		return "", false
	}
	// The name in field 2 may hold spaces and brackets: count from its end.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	f := strings.Fields(s[i+1:])
	// f[0] is field 3 (state); the start time is field 22.
	if len(f) < 20 {
		return "", false
	}
	if f[0] == "Z" {
		// A zombie is gone for our purpose: its group may still live.
		return "", false
	}
	return f[19], true
}
