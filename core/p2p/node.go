package p2p

import (
	"sync"
	"time"
)

type NodeData struct {
	Name          string
	ID            string
	TunnelAddress string
	LastSeen      time.Time
}

func (d NodeData) IsOnline() bool {
	now := time.Now()
	// if the node was seen in the last 40 seconds, it's online
	return now.Sub(d.LastSeen) < 40*time.Second
}

var mu sync.Mutex
var nodes = map[string]NodeData{}

func GetAvailableNodes() []NodeData {
	mu.Lock()
	defer mu.Unlock()
	var availableNodes = []NodeData{}
	for _, v := range nodes {
		availableNodes = append(availableNodes, v)
	}
	return availableNodes
}

func AddNode(node NodeData) {
	mu.Lock()
	defer mu.Unlock()
	nodes[node.ID] = node
}
