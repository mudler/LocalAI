package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/application"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
)

// RegisterP2PRoutes registers the admin routes of the P2P layer and hands the
// application the handlers it serves over P2P: e itself, which only paired
// peers reach, and a handler of its own for the routes that exist over P2P
// alone: POST /api/p2p/redeem, the one route an unpaired peer may call, and
// GET /api/p2p/routes and POST /api/p2p/device/session, for paired devices.
// None is registered on e, so the normal HTTP listener answers 404 for them.
func RegisterP2PRoutes(e *echo.Echo, app *application.Application, adminMw echo.MiddlewareFunc) {
	svc := app.P2P()
	registerP2PAdminRoutes(e, svc, adminMw)
	registerP2PUserRoutes(e, app.P2PUsers())
	registerP2PSplitRoutes(e, app.P2PSplit(), adminMw)
	app.ServeP2PHTTP(e, P2POnlyHandler(svc, app.P2PUsers()))
}

func registerP2PAdminRoutes(e *echo.Echo, svc p2papi.Service, adminMw echo.MiddlewareFunc) {
	e.GET("/api/p2p/status", p2papi.StatusEndpoint(svc), adminMw)
	e.POST("/api/p2p/invites", p2papi.MintInviteEndpoint(svc), adminMw)
	e.GET("/api/p2p/invites", p2papi.ListInvitesEndpoint(svc), adminMw)
	e.DELETE("/api/p2p/invites/:id", p2papi.CancelInviteEndpoint(svc), adminMw)
	e.GET("/api/p2p/devices", p2papi.ListDevicesEndpoint(svc), adminMw)
	e.PATCH("/api/p2p/devices/:id", p2papi.RenameDeviceEndpoint(svc), adminMw)
	e.DELETE("/api/p2p/devices/:id", p2papi.RevokeDeviceEndpoint(svc), adminMw)
	e.GET("/api/p2p/relays", p2papi.ListRelaysEndpoint(svc), adminMw)
	e.POST("/api/p2p/relays", p2papi.AddRelayEndpoint(svc), adminMw)
	e.DELETE("/api/p2p/relays/:id", p2papi.RemoveRelayEndpoint(svc), adminMw)
	e.POST("/api/p2p/relays/:id/check", p2papi.CheckRelayEndpoint(svc), adminMw)
	e.POST("/api/p2p/relays/:id/sync", p2papi.SyncRelayEndpoint(svc), adminMw)
	e.POST("/api/p2p/relays/:id/claim", p2papi.ClaimRelayEndpoint(svc), adminMw)
	e.GET("/api/p2p/relays/:id/limits", p2papi.RelayLimitsEndpoint(svc), adminMw)
	e.PUT("/api/p2p/relays/:id/limits", p2papi.SetRelayLimitsEndpoint(svc), adminMw)
	e.POST("/api/p2p/identity/regenerate", p2papi.RegenerateIdentityEndpoint(svc), adminMw)
	e.GET("/api/p2p/user-pairing", p2papi.UserPairingEndpoint(svc), adminMw)
	e.PUT("/api/p2p/user-pairing", p2papi.SetUserPairingEndpoint(svc), adminMw)
}

// registerP2PUserRoutes registers the routes of a user who pairs their own
// devices. They are the only P2P routes without the admin middleware: the
// route feature registry gives each one the feature p2p_pair, and no client
// feature, so a client key is refused.
func registerP2PUserRoutes(e *echo.Echo, svc p2papi.UserService) {
	e.GET("/api/p2p/me", p2papi.MyPairingEndpoint(svc))
	e.POST("/api/p2p/me/invites", p2papi.MintMyInviteEndpoint(svc))
	e.GET("/api/p2p/me/invites", p2papi.ListMyInvitesEndpoint(svc))
	e.DELETE("/api/p2p/me/invites/:id", p2papi.CancelMyInviteEndpoint(svc))
	e.GET("/api/p2p/me/devices", p2papi.ListMyDevicesEndpoint(svc))
	e.PATCH("/api/p2p/me/devices/:id", p2papi.RenameMyDeviceEndpoint(svc))
	e.DELETE("/api/p2p/me/devices/:id", p2papi.RevokeMyDeviceEndpoint(svc))
}

func registerP2PSplitRoutes(e *echo.Echo, svc p2papi.SplitService, adminMw echo.MiddlewareFunc) {
	e.GET("/api/p2p/split-workers", p2papi.ListSplitWorkersEndpoint(svc), adminMw)
	e.PATCH("/api/p2p/split-workers/:id", p2papi.SetSplitWorkerEndpoint(svc), adminMw)
}

// P2POnlyHandler serves POST /api/p2p/redeem, GET /api/p2p/routes and POST
// /api/p2p/device/session for requests that arrived over P2P. It has no
// session, CSRF or API key middleware: a redeeming caller has no account yet,
// a device asking for routes may have none, a device signing in proves itself
// with its credential, and the connection already authenticated its peer ID.
// The application lets only paired peers reach the routes and session routes.
func P2POnlyHandler(svc p2papi.Service, users p2papi.UserService) http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.POST("/api/p2p/redeem", p2papi.RedeemEndpoint(svc))
	e.GET("/api/p2p/routes", p2papi.RoutesEndpoint(svc))
	e.POST("/api/p2p/device/session", p2papi.DeviceSessionEndpoint(users))
	return e
}
