package routes

import (
	"github.com/labstack/echo/v4"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// RegisterClusterRoutes registers the routes that the processes of a cluster
// call among themselves. They check their own credentials, and the global auth
// middleware lets them through (see auth.ClusterConnectPath).
//
// The connect route is registered on every replica, in every deployment, and
// refuses every worker that has no tunnel credential. A frontend that is not
// distributed answers 503 to a caller that has a credential and 401 to one that
// has none.
//
// The peer route is registered in the same way, for the same reason: the route
// coverage test walks it, and it answers 401 to a dial with no credential. It
// checks the credential of the dialling replica against the instances table, and
// a deployment with no table answers 503.
func RegisterClusterRoutes(e *echo.Echo, registry *nodes.NodeRegistry, tunnels *tunnel.Registry, instances *cluster.Registry, onPeer func(string, *tunnel.Session)) {
	e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(registry, tunnels))
	e.GET(tunnel.PeerPath, clusterapi.PeerHandler(instances, onPeer))
}
