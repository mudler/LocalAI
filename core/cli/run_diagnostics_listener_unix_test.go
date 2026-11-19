//go:build !windows

package cli

import (
	"os"
	"strconv"
	"syscall"
)

// shutdownListenerOnPort finds the socket this process listens on for the IPv4
// port and shuts it down. It reads /proc/self/fd, so it works on Linux only.
func shutdownListenerOnPort(port int) (found bool, err error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		address, err := syscall.Getsockname(fd)
		if err != nil {
			continue
		}
		if address, ok := address.(*syscall.SockaddrInet4); ok && address.Port == port {
			return true, syscall.Shutdown(fd, syscall.SHUT_RDWR)
		}
	}
	return false, nil
}
