package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/application"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
)

// RegisterP2PRoutes registers the admin routes of the P2P layer and hands the
// application the handlers it serves over P2P: e itself, which only paired
// peers reach, and a handler of its own for POST /api/p2p/redeem, the one
// route an unpaired peer may call. The redeem route is not registered on e, so
// the normal HTTP listener answers 404 for it.
func RegisterP2PRoutes(e *echo.Echo, app *application.Application, adminMw echo.MiddlewareFunc) {
	svc := app.P2P()
	registerP2PAdminRoutes(e, svc, adminMw)
	registerP2PSplitRoutes(e, app.P2PSplit(), adminMw)
	app.ServeP2PHTTP(e, P2PRedeemHandler(svc))
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
}

func registerP2PSplitRoutes(e *echo.Echo, svc p2papi.SplitService, adminMw echo.MiddlewareFunc) {
	e.GET("/api/p2p/split-workers", p2papi.ListSplitWorkersEndpoint(svc), adminMw)
	e.PATCH("/api/p2p/split-workers/:id", p2papi.SetSplitWorkerEndpoint(svc), adminMw)
}

// P2PRedeemHandler serves POST /api/p2p/redeem for requests that arrived over
// P2P. It has no session, CSRF or API key middleware: the caller has no
// account yet, and the connection already authenticated its peer ID.
func P2PRedeemHandler(svc p2papi.Service) http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.POST("/api/p2p/redeem", p2papi.RedeemEndpoint(svc))
	return e
}
