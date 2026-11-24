package nodes

import (
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// nodeClients holds one HTTP client for each node. A client is per node, not
// shared, because an http.Transport carries one DialContext and a worker is
// reached through its own dialer, and because the idle pool is keyed by
// host:port only: two workers that report the same address (NAT, loopback)
// would otherwise get each other's connections. The zero value is ready to use.
type nodeClients struct {
	mu      sync.Mutex
	clients map[string]nodeClient
}

// nodeClient is a client and the transport it was built on. The transport is
// kept beside the client because closing the idle streams of a node needs it,
// and what http.Client.Transport holds is whatever the shared constructor wrapped
// it in.
type nodeClient struct {
	client    *http.Client
	transport *http.Transport
}

// get returns the client of nodeID, and builds it on first use with the
// transport that newTransport returns.
func (n *nodeClients) get(nodeID string, newTransport func() (*http.Transport, error)) (*http.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if c, ok := n.clients[nodeID]; ok {
		return c.client, nil
	}
	transport, err := newTransport()
	if err != nil {
		return nil, err
	}
	client := httpclient.New(httpclient.WithTransport(transport))
	if n.clients == nil {
		n.clients = map[string]nodeClient{}
	}
	n.clients[nodeID] = nodeClient{client: client, transport: transport}
	return client, nil
}

// forget drops the client of a node and closes the idle streams it holds.
func (n *nodeClients) forget(nodeID string) {
	n.mu.Lock()
	c, ok := n.clients[nodeID]
	delete(n.clients, nodeID)
	n.mu.Unlock()
	if ok {
		c.transport.CloseIdleConnections()
	}
}

// closeAll drops the client of every node and closes the idle streams that their
// transports hold.
func (n *nodeClients) closeAll() {
	n.mu.Lock()
	clients := n.clients
	n.clients = nil
	n.mu.Unlock()
	for _, c := range clients {
		c.transport.CloseIdleConnections()
	}
}
