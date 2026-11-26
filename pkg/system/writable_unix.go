//go:build !windows

package system

import (
	"os"
	"syscall"
)

func ownerUID(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(st.Uid)
}
