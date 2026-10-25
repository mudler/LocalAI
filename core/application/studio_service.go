package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
)

// legacyAPIKeyUserID is the synthetic user the auth middleware gives a request
// that presents an environment-configured API key.
const legacyAPIKeyUserID = "legacy-api-key"

// StudioService returns the conversation service of the studio loop, or nil
// when it could not be built.
func (a *Application) StudioService() *studio.Service {
	return a.studioService
}

// initStudioService builds the conversation service. It needs the store and the
// provider, so it runs after both.
func (a *Application) initStudioService() {
	if a.studioStore == nil || a.studioProvider == nil {
		return
	}
	stateDir := ""
	if dp := a.applicationConfig.DataPath; dp != "" {
		stateDir = filepath.Join(dp, "chats", "state")
	} else {
		// Memory-only mode keeps nothing across restarts, so the harness's
		// private files go to a folder that is removed at shutdown.
		dir, err := os.MkdirTemp("", "localai-chats-*")
		if err != nil {
			xlog.Warn("Chats disabled: no folder for its state", "error", err)
			return
		}
		a.studioTempDir, stateDir = dir, dir
	}
	svc, err := studio.New(studio.Deps{
		Store:         a.studioStore,
		Provider:      a.studioProvider,
		Users:         a.studioUsers(),
		StateDir:      stateDir,
		MCP:           a.studioMCP,
		ContextTokens: a.studioContextTokens,
	})
	if err != nil {
		xlog.Warn("Chats disabled: failed to start the service", "error", err)
		return
	}
	a.studioService = svc
}

// studioUsers returns the user lookup, or nil when auth is off.
func (a *Application) studioUsers() studio.UserLookup {
	if a.authDB == nil && len(a.applicationConfig.ApiKeys) == 0 {
		return nil
	}
	return a.loadStudioUser
}

// loadStudioUser returns the active user with this ID. A chat acts as its
// owner, so an account that was disabled or never approved is refused.
func (a *Application) loadStudioUser(ctx context.Context, id string) (*auth.User, error) {
	if id == legacyAPIKeyUserID {
		return &auth.User{ID: id, Name: "API Key User", Role: auth.RoleAdmin, Status: auth.StatusActive}, nil
	}
	if a.authDB == nil {
		return nil, errors.New("no user database")
	}
	var u auth.User
	if err := a.authDB.WithContext(ctx).Where("id = ?", id).Take(&u).Error; err != nil {
		return nil, err
	}
	if u.Status != auth.StatusActive {
		return nil, errors.New("the user is not active")
	}
	return &u, nil
}

// studioContextTokens is the context window of a model, or zero when its config
// does not say.
func (a *Application) studioContextTokens(model string) int {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok || cfg.ContextSize == nil {
		return 0
	}
	return *cfg.ContextSize
}

// studioMCPServers picks the MCP servers a chat uses, by the chat route's
// rules for a request that names none: every configured server when the model
// has mcp.auto and the user may use MCP, otherwise none.
func studioMCPServers(cfg *config.ModelConfig, permitted bool) []string {
	if !permitted || !cfg.MCP.Auto || !cfg.MCP.HasMCPServers() {
		return nil
	}
	names, err := cfg.MCP.ServerNames()
	if err != nil {
		xlog.Error("Failed to parse MCP config", "model", cfg.Name, "error", err)
		return nil
	}
	return names
}

// studioMCP builds the MCP tool executor for a user's chat with a model. It is
// the chat route's logic without the request: the executor reuses MCP sessions
// cached by model name and config, and in a cluster routes calls to workers.
func (a *Application) studioMCP(ctx context.Context, user, model string) mcpendpoint.ToolExecutor {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok || !cfg.MCP.HasMCPServers() || a.applicationConfig.DisableMCP {
		return nil
	}
	permitted := true
	if a.authDB != nil {
		u, err := a.loadStudioUser(ctx, user)
		permitted = err == nil && auth.HasFeatureAccess(a.authDB, u, auth.FeatureMCP)
	}
	servers := studioMCPServers(&cfg, permitted)
	if len(servers) == 0 {
		return nil
	}
	remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
	if err != nil {
		xlog.Error("Failed to parse MCP config", "model", model, "error", err)
		return nil
	}
	var control mcpendpoint.AgentControl
	if d := a.Distributed(); d != nil {
		control = d.AgentControl
	}
	return mcpendpoint.NewToolExecutor(ctx, control, cfg.MCPSessionKey(), remote, stdio, servers)
}
