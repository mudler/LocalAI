//go:build darwin

package host

import "syscall"

// statCtimeNano returns the change time of a file, in nanoseconds.
func statCtimeNano(st *syscall.Stat_t) int64 { return st.Ctimespec.Nano() }
