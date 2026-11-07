package openai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
)

// requireAssistantAccess gates a chat request that asked for the LocalAI
// Assistant tool surface (metadata.localai_assistant=true). The assistant's
// in-process MCP server can install models, edit configs and trigger backend
// upgrades, so it must be admin-only — the chat route itself only enforces
// FeatureChat (default-on for every user). When auth is disabled the gate is
// a no-op; the operator already chose to trust every caller.
func requireAssistantAccess(c echo.Context, authEnabled bool) error {
	if !authEnabled {
		return nil
	}
	user := auth.GetUser(c)
	if user == nil || user.Role != auth.RoleAdmin {
		return echo.NewHTTPError(http.StatusForbidden, "localai_assistant requires admin")
	}
	return nil
}

// assistantSwitchedOffMessage is the answer to a request for the admin tools
// while the instance switch (chats_localai_admin in Settings > Tools) is off.
const assistantSwitchedOffMessage = "the LocalAI assistant is switched off on this server"

// requireAssistantOn gates metadata.localai_assistant=true: first the admin
// role (403, so a non-admin learns nothing about the switch), then the
// instance switch of the admin tool group (503). off is
// ApplicationConfig.DisableChatsLocalAIAdmin, read when the request arrives.
func requireAssistantOn(c echo.Context, authEnabled, off bool) error {
	if err := requireAssistantAccess(c, authEnabled); err != nil {
		return err
	}
	if off {
		return echo.NewHTTPError(http.StatusServiceUnavailable, assistantSwitchedOffMessage)
	}
	return nil
}
