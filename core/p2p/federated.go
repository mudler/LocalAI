package p2p

import (
	"fmt"
	"math/rand/v2"
	"sync"

	"github.com/mudler/xlog"
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
	requestTable                  map[string]int // Active connections owned by this server.
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
			xlog.Info("Node is offline", "node", v.ID)
		}
	}

	if len(tunnelAddresses) == 0 {
		return ""
	}

	return tunnelAddresses[rand.IntN(len(tunnelAddresses))]
}

// SelectLeastUsedServer reserves one connection on the least loaded online node.
// Call ReleaseRequest once forwarding ends, including on errors before forwarding.
func (fs *FederatedServer) SelectLeastUsedServer() string {
	fs.Lock()
	defer fs.Unlock()

	var candidates []string
	var min int
	for _, node := range GetAvailableNodes(fs.service) {
		if !node.IsOnline() {
			continue
		}
		count := fs.requestTable[node.ID]
		if len(candidates) == 0 || count < min {
			min = count
			candidates = candidates[:0]
		}
		if count == min {
			candidates = append(candidates, node.ID)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	id := candidates[rand.IntN(len(candidates))]
	fs.requestTable[id]++
	xlog.Debug("Selected tunnel", "tunnel", id, "active_connections", fs.requestTable[id])
	return id
}

func (fs *FederatedServer) ReleaseRequest(nodeID string) {
	fs.Lock()
	defer fs.Unlock()
	// Retain active counts while a node is offline: it may reappear before
	// its existing connections finish. Remove only drained entries.
	if fs.requestTable[nodeID] <= 1 {
		delete(fs.requestTable, nodeID)
	} else {
		fs.requestTable[nodeID]--
	}
}
