package p2p

import (
	"fmt"
	"math/rand/v2"
	"sync"

	"github.com/rs/zerolog/log"
)

const FederatedID = "federated"

func NetworkID(networkID, serviceID string) string {
	if networkID != "" {
		return fmt.Sprintf("%s_%s", networkID, serviceID)
	}
	return serviceID
}

type FederatedServer struct {
	sync.Mutex
	listenAddr, service, p2ptoken string
	requestTable                  map[string]int
	loadBalanced                  bool
	workerTarget                  string
}

func NewFederatedServer(listenAddr, service, p2pToken string, loadBalanced bool, workerTarget string) *FederatedServer {
	return &FederatedServer{
		listenAddr:   listenAddr,
		service:      service,
		p2ptoken:     p2pToken,
		requestTable: map[string]int{},
		loadBalanced: loadBalanced,
		workerTarget: workerTarget,
	}
}

func (fs *FederatedServer) RandomServer() string {
	var tunnelAddresses []string
	for _, v := range GetAvailableNodes(fs.service) {
		if v.IsOnline() {
			tunnelAddresses = append(tunnelAddresses, v.ID)
		} else {
			delete(fs.requestTable, v.ID) // make sure it's not tracked
			log.Info().Msgf("Node %s is offline", v.ID)
		}
	}

	if len(tunnelAddresses) == 0 {
		return ""
	}

	return tunnelAddresses[rand.IntN(len(tunnelAddresses))]
}

func (fs *FederatedServer) syncTableStatus() {
	fs.Lock()
	defer fs.Unlock()
	currentTunnels := make(map[string]struct{})

	for _, v := range GetAvailableNodes(fs.service) {
		if v.IsOnline() {
			fs.ensureRecordExist(v.ID)
			currentTunnels[v.ID] = struct{}{}
		}
	}

	// delete tunnels that don't exist anymore
	for t := range fs.requestTable {
		if _, ok := currentTunnels[t]; !ok {
			delete(fs.requestTable, t)
		}
	}
}

func (fs *FederatedServer) SelectLeastUsedServer() string {
	fs.syncTableStatus()

	fs.Lock()
	defer fs.Unlock()

	log.Debug().Any("request_table", fs.requestTable).Msgf("SelectLeastUsedServer()")

	// cycle over requestTable and find the entry with the lower number
	// if there are multiple entries with the same number, select one randomly
	// if there are no entries, return an empty string
	var min int
	var minKey string
	for k, v := range fs.requestTable {
		if min == 0 || v < min {
			min = v
			minKey = k
		}
	}
	log.Debug().Any("requests_served", min).Any("request_table", fs.requestTable).Msgf("Selected tunnel %s", minKey)

	return minKey
}

func (fs *FederatedServer) RecordRequest(nodeID string) {
	fs.Lock()
	defer fs.Unlock()
	// increment the counter for the nodeID in the requestTable
	fs.requestTable[nodeID]++

	log.Debug().Any("request_table", fs.requestTable).Any("request", nodeID).Msgf("Recording request")
}

func (fs *FederatedServer) ensureRecordExist(nodeID string) {
	// if the nodeID is not in the requestTable, add it with a counter of 0
	_, ok := fs.requestTable[nodeID]
	if !ok {
		fs.requestTable[nodeID] = 0
	}

	log.Debug().Any("request_table", fs.requestTable).Any("request", nodeID).Msgf("Ensure record exists")
}
