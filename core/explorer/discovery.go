package explorer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/edgevpn/pkg/blockchain"
)

type DiscoveryServer struct {
	sync.Mutex
	database     *Database
	networkState *NetworkState
}

type NetworkState struct {
	Networks map[string]Network
}

func (s *DiscoveryServer) NetworkState() *NetworkState {
	s.Lock()
	defer s.Unlock()
	return s.networkState
}

func NewDiscoveryServer(db *Database) *DiscoveryServer {
	return &DiscoveryServer{
		database: db,
		networkState: &NetworkState{
			Networks: map[string]Network{},
		},
	}
}

type Network struct {
	Clusters []ClusterData
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

		networkData := make(chan ClusterData)

		// get the network data - it takes the whole timeout
		// as we might not be connected to the network yet,
		// and few attempts would have to be made before bailing out
		go s.retrieveNetworkData(c, ledger, networkData)

		ledgerK := []ClusterData{}
		for key := range networkData {
			ledgerK = append(ledgerK, key)
		}

		s.Lock()
		s.networkState.Networks[token] = Network{
			Clusters: ledgerK,
		}
		s.Unlock()
	}
}

type ClusterData struct {
	Workers   []string
	Type      string
	NetworkID string
}

func (s *DiscoveryServer) retrieveNetworkData(c context.Context, ledger *blockchain.Ledger, networkData chan ClusterData) {
	clusters := map[string]ClusterData{}

	defer func() {
		for _, n := range clusters {
			networkData <- n
		}
		close(networkData)
	}()

	for {
		select {
		case <-c.Done():
			return
		default:
			time.Sleep(5 * time.Second)

			data := ledger.LastBlock().Storage
		LEDGER:
			for d := range data {
				toScanForWorkers := false
				cd := ClusterData{}
				isWorkerCluster := d == p2p.WorkerID || (strings.Contains(d, "_") && strings.Contains(d, p2p.WorkerID))
				isFederatedCluster := d == p2p.FederatedID || (strings.Contains(d, "_") && strings.Contains(d, p2p.FederatedID))
				switch {
				case isWorkerCluster:
					toScanForWorkers = true
					cd.Type = "worker"
				case isFederatedCluster:
					toScanForWorkers = true
					cd.Type = "federated"
				}

				if strings.Contains(d, "_") {
					cd.NetworkID = strings.Split(d, "_")[0]
				}

				if !toScanForWorkers {
					continue LEDGER
				}

			DATA:
				for _, v := range data[d] {
					nd := &p2p.NodeData{}
					if err := v.Unmarshal(nd); err != nil {
						continue DATA
					}

					if nd.IsOnline() {
						(&cd).Workers = append(cd.Workers, nd.ID)
					}
				}

				clusters[d] = cd
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
