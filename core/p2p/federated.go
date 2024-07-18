package p2p

const FederatedID = "federated"

type FederatedServer struct {
	listenAddr, service, p2ptoken string
	requestTable                  map[string]int
	loadBalanced                  bool
}

func NewFederatedServer(listenAddr, service, p2pToken string, loadBalanced bool) *FederatedServer {
	return &FederatedServer{
		listenAddr:   listenAddr,
		service:      service,
		p2ptoken:     p2pToken,
		requestTable: map[string]int{},
		loadBalanced: loadBalanced,
	}
}

func (fs *FederatedServer) SelectLeastUsedServer() string {
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
	return minKey
}

func (fs *FederatedServer) RecordRequest(nodeID string) {
	// increment the counter for the nodeID in the requestTable
	fs.requestTable[nodeID]++
}

func (fs *FederatedServer) EnsureRecordExist(nodeID string) {
	// if the nodeID is not in the requestTable, add it with a counter of 0
	_, ok := fs.requestTable[nodeID]
	if !ok {
		fs.requestTable[nodeID] = 0
	}
}
