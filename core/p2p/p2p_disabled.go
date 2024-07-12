//go:build !p2p
// +build !p2p

package p2p

import (
	"context"
	"fmt"

	"github.com/mudler/edgevpn/pkg/node"
)

func GenerateToken() string {
	return "not implemented"
}

func ServiceDiscoverer(ctx context.Context, node *node.Node, token, servicesID string, fn func()) error {
	return fmt.Errorf("not implemented")
}

func ExposeService(ctx context.Context, host, port, token, servicesID string) error {
	return fmt.Errorf("not implemented")
}

func IsP2PEnabled() bool {
	return false
}

func NewNode(token string) (*node.Node, error) {
	return nil, fmt.Errorf("not implemented")
}
