package cli_api

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/edgevpn/pkg/node"

	"github.com/rs/zerolog/log"
)

func StartP2PStack(ctx context.Context, address, token, networkID string, federated bool, app *application.Application) error {
	var n *node.Node
	// Here we are avoiding creating multiple nodes:
	// - if the federated mode is enabled, we create a federated node and expose a service
	// - exposing a service creates a node with specific options, and we don't want to create another node

	// If the federated mode is enabled, we expose a service to the local instance running
	// at r.Address
	if federated {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}

		// Here a new node is created and started
		// and a service is exposed by the node
		node, err := p2p.ExposeService(ctx, "localhost", port, token, p2p.NetworkID(networkID, p2p.FederatedID))
		if err != nil {
			return err
		}

		if err := p2p.ServiceDiscoverer(ctx, node, token, p2p.NetworkID(networkID, p2p.FederatedID), nil, false); err != nil {
			return err
		}

		n = node

		// start node sync in the background
		if err := p2p.Sync(ctx, node, app); err != nil {
			return err
		}
	}

	// If the p2p mode is enabled, we start the service discovery
	if token != "" {
		// If a node wasn't created previously, create it
		if n == nil {
			node, err := p2p.NewNode(token)
			if err != nil {
				return err
			}
			err = node.Start(ctx)
			if err != nil {
				return fmt.Errorf("starting new node: %w", err)
			}
			n = node
		}

		// Attach a ServiceDiscoverer to the p2p node
		log.Info().Msg("Starting P2P server discovery...")
		if err := p2p.ServiceDiscoverer(ctx, n, token, p2p.NetworkID(networkID, p2p.WorkerID), func(serviceID string, node schema.NodeData) {
			var tunnelAddresses []string
			for _, v := range p2p.GetAvailableNodes(p2p.NetworkID(networkID, p2p.WorkerID)) {
				if v.IsOnline() {
					tunnelAddresses = append(tunnelAddresses, v.TunnelAddress)
				} else {
					log.Info().Msgf("Node %s is offline", v.ID)
				}
			}
			tunnelEnvVar := strings.Join(tunnelAddresses, ",")

			os.Setenv("LLAMACPP_GRPC_SERVERS", tunnelEnvVar)
			log.Debug().Msgf("setting LLAMACPP_GRPC_SERVERS to %s", tunnelEnvVar)
		}, true); err != nil {
			return err
		}
	}

	return nil
}
