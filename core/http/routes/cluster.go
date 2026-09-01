package routes

import (
	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
)

// RegisterClusterRoutes registers the replica-to-replica peer link. onPeer
// receives every authenticated session; see clusterep.PeerHandler for what it
// is expected to do with it.
//
// The path is core/services/cluster's own constant, so the handler and the
// dialler cannot be registered and dialled at different paths. That the path
// also falls under auth.ClusterPathPrefix, and so bypasses the global session
// middleware, is asserted by driving a request through that middleware in
// core/http/endpoints/cluster/peer_test.go.
//
// The route carries no auth middleware: it authenticates itself against the
// cluster token, because a peer replica has no session and no user.
func RegisterClusterRoutes(e *echo.Echo, token string, onPeer func(string, *yamux.Session)) {
	e.GET(clustersvc.PeerPath, clusterep.PeerHandler(token, onPeer))
}

// RegisterWorkerTunnelRoute registers the endpoint a worker dials to open its
// tunnel. registry authenticates the dial against the node's own stored token;
// tunnels is what the resulting session is attached to.
//
// Unlike the peer link this is registered in EVERY deployment, single-binary
// ones included, and both arguments may be nil there. Two reasons. The handler
// fails closed without a registry, since a token can only be checked against a
// node row and there are none; and being registered unconditionally is what
// puts the route in front of the route-coverage test under build tag `auth`,
// which is the thing that holds the reject-before-upgrade rule in place. A
// route registered only in distributed mode is invisible to that test.
//
// Like the peer link, it carries no auth middleware and derives its path from
// core/services/cluster's own constant, so the handler and the worker's dialler
// cannot end up on different paths.
func RegisterWorkerTunnelRoute(e *echo.Echo, registry *nodes.NodeRegistry, tunnels *clustersvc.TunnelRegistry) {
	e.GET(clustersvc.ConnectPath, clusterep.ConnectHandler(registry, tunnels))
}
