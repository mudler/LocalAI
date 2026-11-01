package application

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// StudioMCPConnectors returns the service that keeps the MCP connectors users
// defined, or nil when the chats have no store.
func (a *Application) StudioMCPConnectors() *mcpconns.Service { return a.studioMCPConns }

// initStudioMCPConnectors builds the connectors service. It shares the sealing
// key of the Telegram connectors: one key per instance, a sealed value tied to
// its record and owner.
func (a *Application) initStudioMCPConnectors() {
	if a.studioMCPStore == nil || a.studioConnStore == nil {
		return
	}
	svc, err := mcpconns.New(mcpconns.Options{
		Store:        a.studioMCPStore,
		Key:          func(ctx context.Context) ([]byte, error) { return a.studioConnStore.EnsureKey(ctx, connectors.NewKey) },
		Permitted:    a.studioMCPPermitted,
		IsAdmin:      a.studioIsAdmin,
		AllowPrivate: func() bool { return a.applicationConfig.MCPConnectorsAllowPrivate },
		// A local command would run on whichever frontend serves the request.
		Distributed: a.Distributed() != nil,
	})
	if err != nil {
		return
	}
	a.studioMCPConns = svc
}

// studioMCPPermitted says whether a user may use MCP now: the server has not
// turned it off and the user has the MCP feature. It is asked on every use.
func (a *Application) studioMCPPermitted(ctx context.Context, user string) bool {
	if a.applicationConfig.DisableMCP {
		return false
	}
	if a.authDB == nil {
		return true
	}
	u, err := a.loadStudioUser(ctx, user)
	return err == nil && auth.HasFeatureAccess(a.authDB, u, auth.FeatureMCP)
}

// studioIsAdmin says whether a user is an administrator. With auth off the
// local owner counts as one.
func (a *Application) studioIsAdmin(ctx context.Context, user string) bool {
	if a.studioUsers() == nil {
		return true
	}
	u, err := a.loadStudioUser(ctx, user)
	return err == nil && u.Role == auth.RoleAdmin
}

// studioModelMCPServers lists the servers of the model's own MCP configuration
// for the per-chat switches. It is empty when the user may not use MCP.
func (a *Application) studioModelMCPServers(ctx context.Context, user, model string) []studio.ModelMCPServer {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok || !cfg.MCP.HasMCPServers() || !a.studioMCPPermitted(ctx, user) {
		return nil
	}
	names, err := cfg.MCP.ServerNames()
	if err != nil {
		return nil
	}
	out := make([]studio.ModelMCPServer, len(names))
	for i, n := range names {
		out[i] = studio.ModelMCPServer{Name: n, Auto: cfg.MCP.Auto}
	}
	return out
}
