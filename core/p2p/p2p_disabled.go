//go:build !p2p
// +build !p2p

package p2p

import (
	"context"
	"fmt"

	"github.com/mudler/edgevpn/pkg/node"
)

func GenerateToken(DHTInterval, OTPInterval int) string {
	return "not implemented"
}

func (f *FederatedServer) Start(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}

func ServiceDiscoverer(ctx context.Context, node *node.Node, token, servicesID string, fn func(string, NodeData), allocate bool) error {
	return fmt.Errorf("not implemented")
}

func ExposeService(ctx context.Context, host, port, token, servicesID string) (*node.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func IsP2PEnabled() bool {
	return false
}

func NewNode(token string) (*node.Node, error) {
	return nil, fmt.Errorf("not implemented")
}
