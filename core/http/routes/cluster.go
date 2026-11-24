package routes

import (
	"net/http"

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
	// JoinTokens is the store of join tokens. Nil on a frontend that is not
	// distributed.
	JoinTokens localai.JoinTokens
}

// ClusterJoinTokensPath is the admin API of the join tokens of workers.
const ClusterJoinTokensPath = "/api/cluster/join-tokens"

// RegisterClusterAdminRoutes registers the admin API of the cluster:
//
//	GET  /api/cluster/settings  the settings, the broadcast bus of this frontend and the live frontends
//	PUT  /api/cluster/settings  store a NATS address or the carrier weights
//	POST   /api/cluster/join-tokens      mint a join token and the commands that carry it
//	GET    /api/cluster/join-tokens      list the join tokens and their state
//	DELETE /api/cluster/join-tokens/:id  revoke a join token
//
// The routes are registered on every frontend, as the connect route is, so that
// the route coverage test walks them and a frontend that is not distributed
// answers 503 and not 404. The admin middleware runs first.
func RegisterClusterAdminRoutes(e *echo.Echo, adminMw echo.MiddlewareFunc, admin ClusterAdmin) {
	if admin.Settings == nil || admin.NATS == nil || admin.State == nil || admin.JoinTokens == nil {
		unavailable := localai.UnavailableClusterEndpoint()
		e.GET(ClusterSettingsPath, unavailable, adminMw)
		e.PUT(ClusterSettingsPath, unavailable, adminMw)
		e.POST(ClusterJoinTokensPath, unavailable, adminMw)
		e.GET(ClusterJoinTokensPath, unavailable, adminMw)
		e.DELETE(ClusterJoinTokensPath+"/:id", unavailable, adminMw)
		return
	}
	e.POST(ClusterJoinTokensPath, localai.MintJoinTokenEndpoint(admin.JoinTokens, admin.Settings), adminMw)
	e.GET(ClusterJoinTokensPath, localai.ListJoinTokensEndpoint(admin.JoinTokens), adminMw)
	e.DELETE(ClusterJoinTokensPath+"/:id", localai.RevokeJoinTokenEndpoint(admin.JoinTokens), adminMw)
	e.GET(ClusterSettingsPath, localai.GetClusterSettingsEndpoint(admin.Settings, admin.State), adminMw)
	e.PUT(ClusterSettingsPath, localai.PutClusterSettingsEndpoint(admin.Settings, admin.NATS), adminMw)
}

// RegisterJoinScriptRoutes registers the public scripts that join a worker:
// /join.sh, /join.ps1 and /join.yaml. Each answers only for a join token that is
// valid, and does not spend it (see localai.JoinScriptEndpoint). The routes exist
// on every frontend; one that is not distributed has no tokens and answers 404.
func RegisterJoinScriptRoutes(e *echo.Echo, tokens localai.JoinTokens, limiter *cluster.JoinFailureLimiter) {
	for path, kind := range map[string]localai.JoinScriptKind{
		localai.JoinShellPath:      localai.JoinScriptShell,
		localai.JoinPowerShellPath: localai.JoinScriptPowerShell,
		localai.JoinKubernetesPath: localai.JoinScriptKubernetes,
	} {
		if tokens == nil {
			e.GET(path, func(c echo.Context) error {
				return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
			})
			continue
		}
		e.GET(path, localai.JoinScriptEndpoint(tokens, limiter, kind))
	}
}
