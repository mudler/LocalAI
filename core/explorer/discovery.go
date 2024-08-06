package explorer

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/edgevpn/pkg/blockchain"
)

type DiscoveryServer struct {
	database     *Database
	networkState *NetworkState
}

type NetworkState struct {
	Nodes map[string]map[string]p2p.NodeData
}

func NewDiscoveryServer(db *Database) *DiscoveryServer {
	return &DiscoveryServer{
		database: db,
		networkState: &NetworkState{
			Nodes: map[string]map[string]p2p.NodeData{},
		},
	}
}

func (s *DiscoveryServer) runBackground() {
	for _, token := range s.database.TokenList() {

		c, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()

		// Connect to the network
		// Get the number of nodes
		// save it in the current state (mutex)
		// do not do in parallel
		n, err := p2p.NewNode(token)
		if err != nil {
			fmt.Println(err)
			continue
		}
		err = n.Start(c)
		if err != nil {
			fmt.Println(err)
			continue
		}

		ledger, err := n.Ledger()
		if err != nil {
			fmt.Println(err)
			continue
		}

		ledgerKeys := make(chan string)
		go s.getLedgerKeys(c, ledger, ledgerKeys)

		ledgerK := []string{}

		for key := range ledgerKeys {
			ledgerK = append(ledgerK, key)
		}
		fmt.Println("Token network", token)
		fmt.Println("Found the following ledger keys in the network", ledgerK)
		// get new services, allocate and return to the channel

		// TODO:
		// a function ensureServices that:
		// - starts a service if not started, if the worker is Online
		// - checks that workers are Online, if not cancel the context of allocateLocalService
		// - discoveryTunnels should return all the nodes and addresses associated with it
		// - the caller should take now care of the fact that we are always returning fresh informations
	}
}

func (s *DiscoveryServer) getLedgerKeys(c context.Context, ledger *blockchain.Ledger, ledgerKeys chan string) {
	keys := map[string]struct{}{}

	for {
		select {
		case <-c.Done():
			return
		default:
			time.Sleep(5 * time.Second)

			data := ledger.LastBlock().Storage
			for k, _ := range data {
				if _, ok := keys[k]; !ok {
					keys[k] = struct{}{}
					ledgerKeys <- k
				}
			}
		}
	}

}

// Start the discovery server. This is meant to be run in to a goroutine.
func (s *DiscoveryServer) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
			// Collect data
			s.runBackground()
		}
	}
}
