package routes

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/nodes"
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
// These are authenticated via the registration token, not admin middleware.
//
// TODO(security): Node self-service endpoints authenticate via shared registration
// token but do not verify per-node identity. A compromised worker can heartbeat/drain/
// deregister other nodes. Future: issue per-node JWT at registration, validate node
// identity on subsequent requests (compare :id param with token subject).
func RegisterNodeSelfServiceRoutes(e *echo.Echo, registry *nodes.NodeRegistry, registrationToken string, autoApprove bool, authDB *gorm.DB, hmacSecret string) {
	if registry == nil {
		return
	}

	readyMw := nodeReadyMiddleware(registry)
	tokenAuthMw := nodeTokenAuth(registrationToken)

	node := e.Group("/api/node", readyMw, tokenAuthMw)
	node.POST("/register", localai.RegisterNodeEndpoint(registry, registrationToken, autoApprove, authDB, hmacSecret))
	node.POST("/:id/heartbeat", localai.HeartbeatEndpoint(registry))
	node.POST("/:id/drain", localai.DrainNodeEndpoint(registry))
	node.POST("/:id/deregister", localai.DeactivateNodeEndpoint(registry))
	node.GET("/:id/models", localai.GetNodeModelsEndpoint(registry))
	node.DELETE("/:id", localai.DeactivateNodeEndpoint(registry))
}

// RegisterNodeAdminRoutes registers /api/nodes/ endpoints used by admins
// (list, get, get models, drain, delete, approve, backend management). Protected by admin middleware.
func RegisterNodeAdminRoutes(e *echo.Echo, registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender, adminMw echo.MiddlewareFunc, authDB *gorm.DB, hmacSecret string, registrationToken string) {
	if registry == nil {
		return
	}

	readyMw := nodeReadyMiddleware(registry)

	admin := e.Group("/api/nodes", readyMw, adminMw)
	admin.GET("", localai.ListNodesEndpoint(registry))
	admin.GET("/:id", localai.GetNodeEndpoint(registry))
	admin.GET("/:id/models", localai.GetNodeModelsEndpoint(registry))
	admin.DELETE("/:id", localai.DeregisterNodeEndpoint(registry))
	admin.POST("/:id/drain", localai.DrainNodeEndpoint(registry))
	admin.POST("/:id/approve", localai.ApproveNodeEndpoint(registry, authDB, hmacSecret))

	// Backend management on workers
	admin.GET("/:id/backends", localai.ListBackendsOnNodeEndpoint(unloader))
	admin.POST("/:id/backends/install", localai.InstallBackendOnNodeEndpoint(unloader))
	admin.POST("/:id/backends/delete", localai.DeleteBackendOnNodeEndpoint(unloader))

	// Model management on workers
	admin.POST("/:id/models/unload", localai.UnloadModelOnNodeEndpoint(unloader, registry))
	admin.POST("/:id/models/delete", localai.DeleteModelOnNodeEndpoint(unloader, registry))

	// Backend log streaming (proxied from worker HTTP server)
	admin.GET("/:id/backend-logs", localai.NodeBackendLogsListEndpoint(registry, registrationToken))
	admin.GET("/:id/backend-logs/:modelId", localai.NodeBackendLogsLinesEndpoint(registry, registrationToken))

	// WebSocket proxy for real-time log streaming from workers
	e.GET("/ws/nodes/:id/backend-logs/:modelId", localai.NodeBackendLogsWSEndpoint(registry, registrationToken), readyMw, adminMw)
}

// nodeTokenAuth validates the registration token for node self-service endpoints.
// When registrationToken is empty (single-node / non-distributed mode), these
// endpoints are unprotected. This is intentional: in single-node mode there are
// no remote workers to authenticate. Operators enabling distributed mode MUST
// set a registration token via LOCALAI_REGISTRATION_TOKEN or config.
//
// It validates the token from an Authorization: Bearer <token> header using
// constant-time comparison.
func nodeTokenAuth(registrationToken string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if registrationToken == "" {
				return next(c)
			}

			token, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
			if !ok {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "missing or invalid Authorization header",
				})
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(registrationToken)) != 1 {
				return c.JSON(http.StatusUnauthorized, map[string]string{
					"error": "invalid registration token",
				})
			}

			return next(c)
		}
	}
}
