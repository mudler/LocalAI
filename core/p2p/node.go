package p2p

import (
	"sync"
	"time"
)

const (
	defaultServicesID = "services"
	WorkerID          = "worker"
)

type NodeData struct {
	Name          string
	ID            string
	TunnelAddress string
	ServiceID     string
	LastSeen      time.Time
}

func (d NodeData) IsOnline() bool {
	now := time.Now()
	// if the node was seen in the last 40 seconds, it's online
	return now.Sub(d.LastSeen) < 40*time.Second
}

var mu sync.Mutex
var nodes = map[string]map[string]NodeData{}

func GetAvailableNodes(serviceID string) []NodeData {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	var availableNodes = []NodeData{}
	for _, v := range nodes[serviceID] {
		availableNodes = append(availableNodes, v)
	}
	return availableNodes
}

func GetNode(serviceID, nodeID string) (NodeData, bool) {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := nodes[serviceID]; !ok {
		return NodeData{}, false
	}
	nd, exists := nodes[serviceID][nodeID]
	return nd, exists
}

func AddNode(serviceID string, node NodeData) {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	if nodes[serviceID] == nil {
		nodes[serviceID] = map[string]NodeData{}
	}
	nodes[serviceID][node.ID] = node
}
