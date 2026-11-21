package routes

import (
	"github.com/labstack/echo/v4"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
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

// ClusterSettingsPath is the admin API of the cluster settings. It sits under
// /api/cluster/, which is not a public prefix: only the exact paths of the
// connect and peer routes skip the global authentication, and this one answers
// to an admin only.
const ClusterSettingsPath = "/api/cluster/settings"

// ClusterAdmin is what the admin API of the cluster needs from the distributed
// services. A frontend that is not distributed passes nil values, and the
// routes answer 503.
type ClusterAdmin struct {
	Settings localai.ClusterSettings
	NATS     localai.NATSChecker
	State    localai.ClusterState
}

// RegisterClusterAdminRoutes registers the admin API of the cluster:
//
//	GET  /api/cluster/settings  the settings, the broadcast bus of this frontend and the live frontends
//	PUT  /api/cluster/settings  store a NATS address or the carrier weights
//
// The routes are registered on every frontend, as the connect route is, so that
// the route coverage test walks them and a frontend that is not distributed
// answers 503 and not 404. The admin middleware runs first.
func RegisterClusterAdminRoutes(e *echo.Echo, adminMw echo.MiddlewareFunc, admin ClusterAdmin) {
	if admin.Settings == nil || admin.NATS == nil || admin.State == nil {
		unavailable := localai.UnavailableClusterEndpoint()
		e.GET(ClusterSettingsPath, unavailable, adminMw)
		e.PUT(ClusterSettingsPath, unavailable, adminMw)
		return
	}
	e.GET(ClusterSettingsPath, localai.GetClusterSettingsEndpoint(admin.Settings, admin.State), adminMw)
	e.PUT(ClusterSettingsPath, localai.PutClusterSettingsEndpoint(admin.Settings, admin.NATS), adminMw)
}
