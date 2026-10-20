// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	endpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"gorm.io/gorm"
)

func RegisterAddonRoutes(e *echo.Echo, a *application.Application) {
	RegisterAddonRoutesFor(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode())
}

// RegisterAddonRoutesFor registers the addon routes over one gateway. A
// frontend of a distributed deployment serves exactly the routes of the owner
// over its forwarding gateway, so the routes are one list.
func RegisterAddonRoutesFor(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string) {
	group := e.Group("/api/addons", auth.RequireFeature(db, auth.FeatureAddons), auth.CSRFMiddleware(), endpoints.BrowserProtection())
	mode := endpoints.WithOwnership(ownership)
	group.POST("/dispatch", endpoints.Dispatch(gw, db, mode))
	group.POST("/relay", endpoints.Relay(gw, db, mode))
	// Proposals. Confirm is the only route that applies a stored mutation, and
	// it accepts only the proposer's browser session; no MCP tool maps to it.
	group.POST("/proposals", endpoints.Propose(gw, db, mode))
	// The caller's own pending proposals: where a tool-made proposal waits for
	// the proposer's browser session. Read-only.
	group.GET("/proposals", endpoints.ListPendingProposals(gw, db, mode))
	group.GET("/proposals/:id", endpoints.GetProposal(gw, db, mode))
	group.POST("/proposals/:id/confirm", endpoints.ConfirmProposal(gw, db, mode))
	group.POST("/proposals/:id/cancel", endpoints.CancelProposal(gw, db, mode))
	group.POST("/proposals/:id/reconcile", endpoints.ReconcileProposal(gw, db, mode))
	// Resolve is the owner's explicit recovery for an unresolved outcome. It
	// has the same confirmation-surface rules as confirm and no MCP tool.
	group.POST("/proposals/:id/resolve", endpoints.ResolveProposal(gw, db, mode))
	// The owner role lists the unresolved proposals of its resource, which
	// resolve needs, under the same confirmation-surface rules and no MCP tool.
	group.GET("/resources/:resourceId/proposals", endpoints.ListUnresolvedProposals(gw, db, mode))
	// UI-only resource management: listing, creation, whole-resource sharing,
	// rename and deletion, and the conversation selection. These are never
	// tools and no MCP tool maps to them; the mutations accept only the
	// person's own browser session.
	group.GET("/resources", endpoints.ListResources(gw, db, mode))
	group.POST("/resources", endpoints.CreateResource(gw, db, mode))
	group.GET("/resources/:resourceId/grants", endpoints.ListResourceGrants(gw, db, mode))
	group.PUT("/resources/:resourceId/grants/:userId", endpoints.SetResourceGrant(gw, db, mode))
	group.DELETE("/resources/:resourceId/grants/:userId", endpoints.RevokeResourceGrant(gw, db, mode))
	group.GET("/resources/:resourceId/users", endpoints.SearchResourceUsers(gw, db, mode))
	group.POST("/resources/:resourceId/rename", endpoints.RenameResource(gw, db, mode))
	group.POST("/resources/:resourceId/delete", endpoints.DeleteResource(gw, db, mode))
	// One data or schema change by the person's own action, with no card.
	group.POST("/resources/:resourceId/apply", endpoints.ApplyResource(gw, db, mode))
	group.GET("/resources/:resourceId/operations/:operationId", endpoints.GetResourceOperation(gw, db, mode))
	// The resource a conversation works on. The stored reference is never a
	// credential: the owner authorizes it again on every use.
	group.GET("/conversations/:conversationId/selection/:addonId", endpoints.GetConversationSelection(gw, db, mode))
	group.PUT("/conversations/:conversationId/selection/:addonId", endpoints.PutConversationSelection(gw, db, mode))
	group.DELETE("/conversations/:conversationId/selection/:addonId", endpoints.DeleteConversationSelection(gw, db, mode))
	// Private file results. Both routes are reads by the requesting principal
	// only; the bytes are relayed from the owner that holds them. No MCP tool
	// maps to them in this release.
	group.GET("/artifacts/:id", endpoints.GetArtifact(gw, db, mode))
	group.GET("/artifacts/:id/download", endpoints.DownloadArtifact(gw, db, mode))
	// The Apps surface. The list and the icon are reads for every caller with
	// the addons feature; the owner decides what each caller sees. Start and
	// stop are administrator-only lifecycle changes through the same owner and
	// operation identity as typed dispatch. No MCP tool maps to these routes
	// in this release; the admin mutation tools are a later task.
	group.GET("/apps", endpoints.ListApps(gw, db, mode))
	group.GET("/apps/:id/icon", endpoints.GetAppIcon(gw, db, mode))
	group.POST("/apps/:id/start", endpoints.StartApp(gw, db, mode))
	group.POST("/apps/:id/stop", endpoints.StopApp(gw, db, mode))
	// Data recovery: backups, an explicit restore and the purge of retained
	// data. Administrator-only, accepted only from the administrator's own
	// browser session, never a tool; the owner decides.
	group.GET("/:id/recovery", endpoints.GetRecovery(gw, db, mode))
	group.POST("/:id/recovery/backup", endpoints.CreateBackup(gw, db, mode))
	group.POST("/:id/recovery/verify", endpoints.VerifyBackup(gw, db, mode))
	group.POST("/:id/recovery/restore", endpoints.RestoreBackup(gw, db, mode))
	group.POST("/:id/recovery/delete-backup", endpoints.DeleteBackup(gw, db, mode))
	group.POST("/:id/recovery/prune", endpoints.PruneBackups(gw, db, mode))
	group.POST("/:id/purge", endpoints.PurgeAddon(gw, db, mode))
	// Update detection and the explicit upgrade. The status is a read for
	// every caller with the feature (the owner redacts it for anyone who is
	// not an administrator); the check and the upgrade accept only the
	// administrator's own browser session. Never tools: no MCP tool maps to
	// them, and a status read may be exposed as a tool later.
	group.GET("/:id/update", endpoints.GetUpdate(gw, db, mode))
	group.POST("/:id/update/check", endpoints.CheckUpdate(gw, db, mode))
	group.POST("/:id/upgrade", endpoints.UpgradeAddon(gw, db, mode))
	// Immutable UI code of an installed, enabled addon, by package digest. A
	// read for every caller with the feature; the owner serves only declared
	// files of the current package. No MCP tool maps to it.
	group.GET("/:id/assets/:digest/*", endpoints.GetAsset(gw, db, mode))
}
