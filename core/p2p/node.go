package p2p

import (
	"slices"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/schema"
)

const (
	defaultServicesID = "services"
	WorkerID          = "worker"
)

var mu sync.Mutex
var nodes = map[string]map[string]schema.NodeData{}

func GetAvailableNodes(serviceID string) []schema.NodeData {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	var availableNodes = []schema.NodeData{}
	for _, v := range nodes[serviceID] {
		availableNodes = append(availableNodes, v)
	}

	slices.SortFunc(availableNodes, func(a, b schema.NodeData) int {
		return strings.Compare(a.ID, b.ID)
	})

	return availableNodes
}

func GetNode(serviceID, nodeID string) (schema.NodeData, bool) {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := nodes[serviceID]; !ok {
		return schema.NodeData{}, false
	}
	nd, exists := nodes[serviceID][nodeID]
	return nd, exists
}

func AddNode(serviceID string, node schema.NodeData) {
	if serviceID == "" {
		serviceID = defaultServicesID
	}
	mu.Lock()
	defer mu.Unlock()
	if nodes[serviceID] == nil {
		nodes[serviceID] = map[string]schema.NodeData{}
	}
	nodes[serviceID][node.ID] = node
}
