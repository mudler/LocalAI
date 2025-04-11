//go:build !p2p
// +build !p2p

package p2p

import (
	"context"
	"fmt"

	cliP2P "github.com/mudler/LocalAI/core/cli/p2p"
	p2pConfig "github.com/mudler/edgevpn/pkg/config"
	"github.com/mudler/edgevpn/pkg/node"
)

func GenerateNewConnectionData(DHTInterval, OTPInterval int, privkey string, peerguardMode bool) (*node.YAMLConnectionConfig, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *FederatedServer) Start(ctx context.Context, p2pCommonFlags cliP2P.P2PCommonFlags) error {
	return fmt.Errorf("not implemented")
}

func ServiceDiscoverer(ctx context.Context, n *node.Node, servicesID string, discoveryFunc func(serviceID string, node NodeData), allocate bool) error {
	return fmt.Errorf("not implemented")
}

func ExposeService(ctx context.Context, p2pCfg p2pConfig.Config, host, port, servicesID string) (*node.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func IsP2PEnabled() bool {
	return false
}

func NewNode(p2pCfg p2pConfig.Config) (*node.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func NewP2PConfig(p2pCommonFlags cliP2P.P2PCommonFlags) p2pConfig.Config {
	return p2pConfig.Config{}
}
