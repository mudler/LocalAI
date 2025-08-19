package explorer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/edgevpn/pkg/blockchain"
)

type DiscoveryServer struct {
	sync.Mutex
	database       *Database
	connectionTime time.Duration
	errorThreshold int
}

// NewDiscoveryServer creates a new DiscoveryServer with the given Database.
// it keeps the db state in sync with the network state
func NewDiscoveryServer(db *Database, dur time.Duration, failureThreshold int) *DiscoveryServer {
	if dur == 0 {
		dur = 50 * time.Second
	}
	if failureThreshold == 0 {
		failureThreshold = 3
	}
	return &DiscoveryServer{
		database:       db,
		connectionTime: dur,
		errorThreshold: failureThreshold,
	}
}

type Network struct {
	Clusters []ClusterData
}

func (s *DiscoveryServer) runBackground() {
	if len(s.database.TokenList()) == 0 {
		time.Sleep(5 * time.Second) // avoid busy loop
		return
	}

	for _, token := range s.database.TokenList() {
		c, cancel := context.WithTimeout(context.Background(), s.connectionTime)
		defer cancel()

		// Connect to the network
		// Get the number of nodes
		// save it in the current state (mutex)
		// do not do in parallel
		n, err := p2p.NewNode(token)
		if err != nil {
			log.Err(err).Msg("Failed to create node")
			s.failedToken(token)
			continue
		}

		err = n.Start(c)
		if err != nil {
			log.Err(err).Msg("Failed to start node")
			s.failedToken(token)
			continue
		}

		ledger, err := n.Ledger()
		if err != nil {
			log.Err(err).Msg("Failed to start ledger")
			s.failedToken(token)
			continue
		}

		networkData := make(chan ClusterData)

		// get the network data - it takes the whole timeout
		// as we might not be connected to the network yet,
		// and few attempts would have to be made before bailing out
		go s.retrieveNetworkData(c, ledger, networkData)

		hasWorkers := false
		ledgerK := []ClusterData{}
		for key := range networkData {
			ledgerK = append(ledgerK, key)
			if len(key.Workers) > 0 {
				hasWorkers = true
			}
		}

		log.Debug().Any("network", token).Msgf("Network has %d clusters", len(ledgerK))
		if len(ledgerK) != 0 {
			for _, k := range ledgerK {
				log.Debug().Any("network", token).Msgf("Clusterdata %+v", k)
			}
		}

		if hasWorkers {
			s.Lock()
			data, _ := s.database.Get(token)
			(&data).Clusters = ledgerK
			(&data).Failures = 0
			s.database.Set(token, data)
			s.Unlock()
		} else {
			s.failedToken(token)
		}
	}

	s.deleteFailedConnections()
}

func (s *DiscoveryServer) failedToken(token string) {
	s.Lock()
	defer s.Unlock()
	data, _ := s.database.Get(token)
	(&data).Failures++
	s.database.Set(token, data)
}

func (s *DiscoveryServer) deleteFailedConnections() {
	s.Lock()
	defer s.Unlock()
	for _, t := range s.database.TokenList() {
		data, _ := s.database.Get(t)
		if data.Failures > s.errorThreshold {
			log.Info().Any("token", t).Msg("Token has been removed from the database")
			s.database.Delete(t)
		}
	}
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

				atLeastOneWorker := false
			DATA:
				for _, v := range data[d] {
					nd := &schema.NodeData{}
					if err := v.Unmarshal(nd); err != nil {
						continue DATA
					}

					if nd.IsOnline() {
						atLeastOneWorker = true
						(&cd).Workers = append(cd.Workers, nd.ID)
					}
				}

				if atLeastOneWorker {
					clusters[d] = cd
				}
			}
		}
	}
}

// Start the discovery server. This is meant to be run in to a goroutine.
func (s *DiscoveryServer) Start(ctx context.Context, keepRunning bool) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled")
		default:
			// Collect data
			s.runBackground()
			if !keepRunning {
				return nil
			}
		}
	}
}
