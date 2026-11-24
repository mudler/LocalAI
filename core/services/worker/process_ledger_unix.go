//go:build !windows

package worker

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// killProcessGroup sends SIGKILL to the process group led by pid.
func killProcessGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// procStartTime reads the start time from /proc/<pid>/stat.
func procStartTime(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name is in parentheses and may hold spaces; the fields after
	// the last ')' are space separated and start at field 3. Field 22 is the
	// start time.
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return ""
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
