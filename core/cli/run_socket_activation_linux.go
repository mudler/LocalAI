//go:build linux

package cli

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

const systemdListenFDStart = 3

func systemdActivatedListeners() ([]net.Listener, error) {
	listenPID := os.Getenv("LISTEN_PID")
	listenFDs := os.Getenv("LISTEN_FDS")
	if listenPID == "" && listenFDs == "" {
		return nil, nil
	}

	defer func() {
		for _, key := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
			_ = os.Unsetenv(key)
		}
	}()

	// A half-populated environment is not an activation attempt. Container runtimes
	// started from a socket-activated system unit leak a bare LISTEN_PID into every
	// container they spawn, and systemd's own sd_listen_fds() treats either variable
	// being absent as "not activated" rather than as an error.
	if listenPID == "" || listenFDs == "" {
		return nil, nil
	}

	pid, err := strconv.Atoi(listenPID)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_PID %q: %w", listenPID, err)
	}
	count, err := strconv.Atoi(listenFDs)
	if err != nil || count < 0 {
		return nil, fmt.Errorf("invalid LISTEN_FDS %q", listenFDs)
	}
	if pid != os.Getpid() || count == 0 {
		return nil, nil
	}

	return listenersFromSystemdFDs(systemdListenFDStart, count)
}

func listenersFromSystemdFDs(start, count int) (_ []net.Listener, err error) {
	listeners := make([]net.Listener, 0, count)
	defer func() {
		if err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
		}
	}()

	for offset := range count {
		fd := uintptr(start + offset)
		file := os.NewFile(fd, fmt.Sprintf("LISTEN_FD_%d", fd))
		if file == nil {
			return nil, fmt.Errorf("opening systemd listener file descriptor %d", fd)
		}
		listener, listenerErr := net.FileListener(file)
		closeErr := file.Close()
		if listenerErr != nil {
			return nil, fmt.Errorf("using systemd file descriptor %d as a stream listener: %w", fd, listenerErr)
		}
		if closeErr != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("closing inherited systemd file descriptor %d: %w", fd, closeErr)
		}
		listeners = append(listeners, listener)
	}

	return listeners, nil
}
