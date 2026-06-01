package p2p

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/clusterrouting"
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
			xlog.Info("Node is offline", "node", v.ID)
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

// buildFederatedCandidates maps the currently-online federated peers into the
// shared routing policy's candidate form. InFlight comes from the per-peer
// request counter (lower means fewer requests routed there); AvailableVRAM
// comes from the gossiped NodeData. LastUsed is intentionally left zero:
// federation has no per-peer last-used clock, and the request counter already
// spreads load, so the VRAM tier deterministically breaks in-flight ties.
func buildFederatedCandidates(nodes []schema.NodeData, requestTable map[string]int, now time.Time) []clusterrouting.ReplicaCandidate {
	candidates := make([]clusterrouting.ReplicaCandidate, 0, len(nodes))
	for _, nd := range nodes {
		if !nd.IsOnlineAt(now) {
			continue
		}
		candidates = append(candidates, clusterrouting.ReplicaCandidate{
			NodeID:        nd.ID,
			InFlight:      requestTable[nd.ID],
			AvailableVRAM: nd.AvailableVRAM,
		})
	}
	return candidates
}

// SelectBestServer picks the online federated peer to serve the next request
// using the shared cluster-routing policy (least in-flight, then most free
// VRAM). Returns "" when no peer is online.
func (fs *FederatedServer) SelectBestServer() string {
	fs.syncTableStatus()
	// Snapshot the node set before taking fs.Lock so the fs critical section
	// only guards requestTable. GetAvailableNodes takes its own global mutex;
	// calling it outside fs.Lock avoids a fs.Mutex -> node.mu lock ordering.
	nodes := GetAvailableNodes(fs.service)
	fs.Lock()
	defer fs.Unlock()
	candidates := buildFederatedCandidates(nodes, fs.requestTable, time.Now())
	best := clusterrouting.PickBestReplica(candidates)
	if best == nil {
		xlog.Debug("No online federated peers to select", "request_table", fs.requestTable)
		return ""
	}
	xlog.Debug("Selected federated peer", "peer", best.NodeID, "request_table", fs.requestTable)
	return best.NodeID
}

func (fs *FederatedServer) RecordRequest(nodeID string) {
	fs.Lock()
	defer fs.Unlock()
	// increment the counter for the nodeID in the requestTable
	fs.requestTable[nodeID]++

	xlog.Debug("Recording request", "request_table", fs.requestTable, "request", nodeID)
}

func (fs *FederatedServer) ensureRecordExist(nodeID string) {
	// if the nodeID is not in the requestTable, add it with a counter of 0
	_, ok := fs.requestTable[nodeID]
	if !ok {
		fs.requestTable[nodeID] = 0
	}

	xlog.Debug("Ensure record exists", "request_table", fs.requestTable, "request", nodeID)
}
