// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
)

func RegisterAddonRoutes(e *echo.Echo, a *application.Application) {
	group := e.Group("/api/addons", auth.RequireFeature(a.AuthDB(), auth.FeatureAddons), auth.CSRFMiddleware(), endpoints.BrowserProtection())
	mode := endpoints.WithOwnership(a.AddonOwnershipMode())
	group.POST("/dispatch", endpoints.Dispatch(a.AddonGateway(), a.AuthDB(), mode))
	group.POST("/relay", endpoints.Relay(a.AddonGateway(), a.AuthDB(), mode))
	// Proposals. Confirm is the only route that applies a stored mutation, and
	// it accepts only the proposer's browser session; no MCP tool maps to it.
	group.POST("/proposals", endpoints.Propose(a.AddonGateway(), a.AuthDB(), mode))
	group.GET("/proposals/:id", endpoints.GetProposal(a.AddonGateway(), a.AuthDB(), mode))
	group.POST("/proposals/:id/confirm", endpoints.ConfirmProposal(a.AddonGateway(), a.AuthDB(), mode))
	group.POST("/proposals/:id/cancel", endpoints.CancelProposal(a.AddonGateway(), a.AuthDB(), mode))
	group.POST("/proposals/:id/reconcile", endpoints.ReconcileProposal(a.AddonGateway(), a.AuthDB(), mode))
	// Resolve is the owner's explicit recovery for an unresolved outcome. It
	// has the same confirmation-surface rules as confirm and no MCP tool.
	group.POST("/proposals/:id/resolve", endpoints.ResolveProposal(a.AddonGateway(), a.AuthDB(), mode))
	// The owner role lists the unresolved proposals of its resource, which
	// resolve needs, under the same confirmation-surface rules and no MCP tool.
	group.GET("/resources/:resourceId/proposals", endpoints.ListUnresolvedProposals(a.AddonGateway(), a.AuthDB(), mode))
}
