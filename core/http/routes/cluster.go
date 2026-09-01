package routes

import (
	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"

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
