//go:build darwin

package host

import (
	"os/exec"
	"strconv"
	"strings"
)

// procStart returns a token that identifies the process pid by its start time,
// and ok=false when no such process exists. A reused pid has another token.
func procStart(pid int) (string, bool) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output() // #nosec G204 -- pid is an int
	if err != nil {
		return "", false
	}
	t := strings.TrimSpace(string(out))
	return t, t != ""
}
