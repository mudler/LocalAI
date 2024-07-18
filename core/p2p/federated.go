package p2p

const FederatedID = "federated"

type FederatedServer struct {
	listenAddr, service, p2ptoken string
}

func NewFederatedServer(listenAddr, service, p2pToken string) *FederatedServer {
	return &FederatedServer{
		listenAddr: listenAddr,
		service:    service,
		p2ptoken:   p2pToken,
	}
}
