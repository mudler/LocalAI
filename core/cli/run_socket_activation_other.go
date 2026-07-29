//go:build !linux

package cli

import "net"

func systemdActivatedListeners() ([]net.Listener, error) {
	return nil, nil
}
