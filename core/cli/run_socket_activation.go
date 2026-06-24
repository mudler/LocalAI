package cli

import (
	"fmt"
	"net"
)

func selectSystemdListener(listeners []net.Listener) (net.Listener, error) {
	switch len(listeners) {
	case 0:
		return nil, nil
	case 1:
		return listeners[0], nil
	default:
		return nil, fmt.Errorf("systemd socket activation requires exactly one stream listener, got %d", len(listeners))
	}
}
