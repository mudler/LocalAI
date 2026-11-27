package routes

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"gorm.io/gorm"
)

// nodeReadyMiddleware returns middleware that checks the node registry is available.
func nodeReadyMiddleware(registry *nodes.NodeRegistry) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if registry == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "distributed mode not enabled",
				})
			}
			return next(c)
		}
	}
}

// RegisterNodeSelfServiceRoutes registers /api/node/ endpoints used by backend
// nodes themselves (register, heartbeat, drain, query own models, deregister).
// They are authenticated by the node, not by admin middleware.
//
// Registration is the step where a worker joins. It takes a join token, the
// shared registration token when the frontend has one, or the own credential of
// a node that registers again. It hands the worker its own credential. Every
// other route here takes that credential and nothing else, and only for the
// node that it belongs to, so the registration token, which every worker may
// hold, cannot act on another worker's node. A paired worker over P2P is
// authenticated by its peer ID, as before.
func RegisterNodeSelfServiceRoutes(e *echo.Echo, registry *nodes.NodeRegistry, registrationToken string, autoApprove bool, authDB *gorm.DB, hmacSecret string, natsCfg natsauth.Config, options ...localai.RegisterOption) {
	if registry == nil {
		return
	}

	readyMw := nodeReadyMiddleware(registry)

	e.POST("/api/node/register", localai.RegisterNodeEndpoint(registry, registrationToken, autoApprove, authDB, hmacSecret, natsCfg, options...), readyMw)

	node := e.Group("/api/node", readyMw, nodeCredentialAuth(registry, options...))
	node.POST("/:id/heartbeat", localai.HeartbeatEndpoint(registry, options...))
	node.POST("/:id/drain", localai.SelfDrainNodeEndpoint(registry))
	node.POST("/:id/resume", localai.ResumeNodeEndpoint(registry))
	node.POST("/:id/deregister", localai.DeactivateNodeEndpoint(registry))
	node.GET("/:id/models", localai.GetNodeModelsEndpoint(registry))
	node.POST("/:id/split", localai.NodeSplitEndpoint(registry, options...))
	node.DELETE("/:id", localai.DeactivateNodeEndpoint(registry))
}

// RegisterNodeAdminRoutes registers /api/nodes/ endpoints used by admins
// (list, get, get models, drain, delete, approve, backend management). Protected by admin middleware.
//
// galleryService/opcache/appConfig are threaded in for the async node-scoped
// backend install path (POST /:id/backends/install). That handler enqueues a
// ManagementOp on the gallery channel rather than blocking on a NATS reply, so
// the browser gets HTTP 202 + jobID immediately instead of waiting up to 3 minutes.
func RegisterNodeAdminRoutes(e *echo.Echo, registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache, appConfig *config.ApplicationConfig, adminMw echo.MiddlewareFunc, authDB *gorm.DB, hmacSecret string, registrationToken string, natsCfg natsauth.Config, workerHTTPDial nodes.WorkerNetDialerFor, upgrades localai.UpgradeInfoProvider, options ...localai.RegisterOption) {
	if registry == nil {
		return
	}

	readyMw := nodeReadyMiddleware(registry)

	admin := e.Group("/api/nodes", readyMw, adminMw)
	admin.GET("", localai.ListNodesEndpoint(registry, options...))

	// Cluster-wide loaded models (registered before /:id to avoid route conflicts)
	admin.GET("/models", localai.ListAllNodeModelsEndpoint(registry))

	// Model scheduling (registered before /:id to avoid route conflicts)
	admin.GET("/scheduling", localai.ListSchedulingEndpoint(registry))
	admin.GET("/scheduling/:model", localai.GetSchedulingEndpoint(registry))
	admin.POST("/scheduling", localai.SetSchedulingEndpoint(registry))
	admin.DELETE("/scheduling/:model", localai.DeleteSchedulingEndpoint(registry))

	admin.GET("/:id", localai.GetNodeEndpoint(registry, options...))
	admin.GET("/:id/models", localai.GetNodeModelsEndpoint(registry))
	admin.DELETE("/:id", localai.DeregisterNodeEndpoint(registry, unloader, options...))
	admin.POST("/:id/drain", localai.DrainNodeEndpoint(registry, unloader))
	admin.POST("/:id/resume", localai.ResumeNodeEndpoint(registry))
	admin.POST("/:id/approve", localai.ApproveNodeEndpoint(registry, authDB, hmacSecret, natsCfg, options...))

	// Backend management on workers
	admin.GET("/:id/backends", localai.ListBackendsOnNodeEndpoint(unloader, registry))
	admin.POST("/:id/backends/install", localai.InstallBackendOnNodeEndpoint(unloader, galleryService, opcache, appConfig), middleware.RequireBackendInstallEnabled(appConfig))
	// Upgrade is a distinct route (not install) because the worker's
	// backend.install handler short-circuits when the backend already exists
	// on disk; only the Upgrade op path force-reinstalls.
	admin.POST("/:id/backends/upgrade", localai.UpgradeBackendOnNodeEndpoint(galleryService, opcache, appConfig, localai.NewBackendUpgradeGuard(galleryService, upgrades)), middleware.RequireBackendInstallEnabled(appConfig))
	admin.POST("/:id/backends/delete", localai.DeleteBackendOnNodeEndpoint(unloader))

	// Model management on workers
	admin.POST("/:id/models/unload", localai.UnloadModelOnNodeEndpoint(unloader, registry))
	admin.POST("/:id/models/delete", localai.DeleteModelOnNodeEndpoint(unloader, registry))

	// Backend log streaming (proxied from worker HTTP server)
	admin.GET("/:id/backend-logs", localai.NodeBackendLogsListEndpoint(registry, registrationToken, workerHTTPDial))
	admin.GET("/:id/backend-logs/:modelId", localai.NodeBackendLogsLinesEndpoint(registry, registrationToken, workerHTTPDial))

	// Label management
	admin.GET("/:id/labels", localai.GetNodeLabelsEndpoint(registry))
	admin.PUT("/:id/labels", localai.SetNodeLabelsEndpoint(registry))
	admin.PATCH("/:id/labels", localai.MergeNodeLabelsEndpoint(registry))
	admin.DELETE("/:id/labels/:key", localai.DeleteNodeLabelEndpoint(registry))

	// Per-node replica capacity. PUT sets a sticky admin override that
	// survives worker restarts. DELETE clears the override so the worker's
	// CLI flag takes over again at the next re-registration.
	admin.PUT("/:id/max-replicas-per-model", localai.UpdateMaxReplicasPerModelEndpoint(registry))
	admin.DELETE("/:id/max-replicas-per-model", localai.ResetMaxReplicasPerModelEndpoint(registry))

	// Split server declaration: the node may serve model-split workers.
	admin.PUT("/:id/split-server", localai.SetSplitServerEndpoint(registry, options...))
	admin.DELETE("/:id/split-server", localai.ResetSplitServerEndpoint(registry))

	// Per-node VRAM allocation budget. PUT sets a sticky admin override that
	// survives worker restarts; DELETE clears it so the worker's reported
	// budget takes over again at the next re-registration.
	admin.PUT("/:id/vram-budget", localai.UpdateVRAMBudgetEndpoint(registry))
	admin.DELETE("/:id/vram-budget", localai.ResetVRAMBudgetEndpoint(registry))

	// Per-node memory limits: the effective values with their source, and
	// sticky admin overrides of the cluster memory settings.
	admin.GET("/:id/limits", localai.GetNodeLimitsEndpoint(registry))
	admin.PUT("/:id/limits", localai.SetNodeLimitsEndpoint(registry))
	admin.DELETE("/:id/limits", localai.ResetNodeLimitsEndpoint(registry))

	// WebSocket proxy for real-time log streaming from workers
	e.GET("/ws/nodes/:id/backend-logs/:modelId", localai.NodeBackendLogsWSEndpoint(registry, registrationToken, workerHTTPDial), readyMw, adminMw)
}

// nodeCredentialAuth validates the own credential of a node on the node
// self-service routes. The credential comes in an Authorization: Bearer header
// and is compared with the hash that registration stored, in constant time.
//
// A request is accepted for the node named by the :id of its route and for no
// other, so a worker cannot heartbeat, drain or deregister another worker. A
// node that holds no credential (it registered before credentials existed) is
// refused with 401 and the worker registers again. The shared registration
// token is not a credential here.
//
// A paired worker that calls over P2P needs no token: the P2P layer has
// authenticated its peer ID. It may act only on the node that is pinned to
// that peer, so a route with a node ID answers 403 for any other node. The
// registration route pins the node (see localai.RegisterNodeEndpoint).
func nodeCredentialAuth(registry *nodes.NodeRegistry, options ...localai.RegisterOption) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := c.Request().Context()
			id := c.Param("id")
			if peer, ok := localai.P2PWorkerPeer(ctx, options...); ok {
				if id == "" {
					return next(c)
				}
				node, err := registry.Get(ctx, id)
				if err != nil || node.PeerID != peer {
					return c.JSON(http.StatusForbidden, map[string]string{
						"error": "this worker may act on its own node only",
					})
				}
				return next(c)
			}

			token, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "missing or invalid Authorization header",
				})
			}
			// An unknown node and a wrong credential look the same.
			node, err := registry.Get(ctx, id)
			if err != nil || !nodes.NodeCredentialMatches(node, token) {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid node credential",
				})
			}
			return next(c)
		}
	}
}
