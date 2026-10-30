package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
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
	var bus studio.Bus
	var fan notify.Fanout
	if d := a.Distributed(); d != nil && d.Nats != nil {
		bus = studio.NewBroadcastBus(d.Nats, nil)
		fan = notify.NewBroadcastFanout(d.Nats, nil)
	}
	inbox := notify.NewService(notify.Options{Store: a.studioNotify, Fanout: fan})
	a.studioInbox = inbox
	// A notification of a run goes to the Telegram chat of a binding too. The
	// connector manager is set later, so the hook looks it up when it runs.
	inbox.OnAdd(func(n notify.Notification) {
		if c := a.studioConnectorsHook.Load(); c != nil {
			c.Forward(context.Background(), n)
		}
	})
	var pusher *notify.Pusher
	if a.studioPush != nil {
		pusher = notify.NewPusher(notify.PusherOptions{Store: a.studioPush})
		// The frontend that stores a notification pushes it, so a notification
		// is pushed once in a cluster.
		inbox.OnAdd(pusher.SendAsync)
		a.studioPusher = pusher
	}
	a.initStudioMCPConnectors()
	a.studioWeb = newStudioWeb(a.applicationConfig)
	deps := studio.Deps{
		Bus:      bus,
		Store:    a.studioStore,
		Provider: a.studioProvider,
		Users:    a.studioUsers(),
		Allowed:  a.studioFeatureAllowed,
		// Read at each turn, so the runtime setting applies at once.
		SuggestionsDefault: func() bool { return !a.applicationConfig.DisableChatSuggestions },
		StateDir:           stateDir,
		MCP:                a.studioMCP,
		MCPServers:         a.studioModelMCPServers,
		Agents:             a.studioAgents,
		Notify:             inbox,
		Push:               pusher,
		AddonTools:         lateStudioAddons{a},
		Assistant:          studioAssistant{a},
		Collections:        studioCollections{a},
		ContextTokens:      a.studioContextTokens,
		RemoteModel:        a.studioRemoteModel,
		PageConsents:       a.studioPageConsents(),
		SharedFolderAudit:  a.studioSharedFolderAudit(),
		FindUser:           a.studioFindUser(),
		SharedPagesOn:      func() bool { return !a.applicationConfig.DisableSharedPages },
		Modalities:         a.studioModalities,
		MCPContent:         studioMCPContent{a: a},
		AgentOptions:       a.studioAgentOptions,
		Persistent:         a.studioStoreKind != "memory",
		Files:              a.StudioFiles(),
		Media:              media.New(a.studioProvider, media.UserLookup(a.studioUsers()), a.studioHelperModel),
		Web:                a.studioWeb,
	}
	if a.studioMCPConns != nil {
		deps.MCPConnectors = a.studioMCPConns
	}
	if d := a.Distributed(); d != nil && d.Nats != nil && a.applicationConfig.Distributed.ChatWorkers && !a.applicationConfig.Distributed.ChatWorker {
		deps.Workers = nodes.NewNATSChatStarter(d.Nats, nodes.WithChatStartTimeout(a.applicationConfig.Distributed.ChatStartTimeout))
	}
	if a.applicationConfig.Distributed.ChatWorker {
		deps.RunLimit = a.studioWorkerRunLimit
		deps.MaxWorkerTurns = a.applicationConfig.Distributed.ChatWorkerMaxTurns
	}
	svc, err := studio.New(deps)
	if err != nil {
		xlog.Warn("Chats disabled: failed to start the service", "error", err)
		return
	}
	a.studioService = svc
	a.startStudioReaper(svc)
	if a.applicationConfig.Distributed.ChatWorker {
		// The hosts that start unattended runs keep their leader locks on the
		// frontends; this process runs the turns they send (StartChatWorker).
		xlog.Info("Chats: this is a chat worker; triggers, connectors and agent jobs start on the frontends")
		return
	}
	a.startStudioConnectors(svc)
	a.startStudioTriggers(svc)
	a.startStudioJobs(svc)
	a.prepareStudioConversion(svc)
}

// A turn beats every 15 seconds; one that has not beaten for this long belongs
// to a frontend that died.
const (
	studioTurnTimeout = time.Minute
	studioReapEvery   = 30 * time.Second
)

// startStudioReaper frees the chats whose turn was left running by a process
// that died. A standalone process runs once at startup with no grace: every
// claim then belongs to a process that no longer exists. In a cluster the other
// frontends are alive, so only turns that stopped beating are reaped, by the one
// frontend that holds the advisory lock at each tick.
func (a *Application) startStudioReaper(svc *studio.Service) {
	ctx := a.applicationConfig.Context
	if ctx == nil {
		ctx = context.Background()
	}
	reap := func(olderThan time.Duration) {
		n, err := svc.ReapStale(ctx, olderThan)
		if err != nil {
			xlog.Warn("Chats: could not reap stale turns", "error", err)
		} else if n > 0 {
			xlog.Info("Chats: ended turns whose frontend stopped", "turns", n)
		}
	}
	if a.Distributed() == nil {
		if a.authDB != nil {
			// The JSON store settles its own claims when it loads; a database does not.
			reap(0)
		}
		return
	}
	if a.authDB == nil {
		return
	}
	go advisorylock.RunLeaderLoop(ctx, a.authDB, advisorylock.KeyChatsReaper, studioReapEvery, func() { reap(studioTurnTimeout) })
}

// studioUsers returns the user lookup, or nil when auth is off.
func (a *Application) studioUsers() studio.UserLookup {
	if a.authDB == nil && len(a.applicationConfig.ApiKeys) == 0 {
		return nil
	}
	return a.loadStudioUser
}

// studioFeatureAllowed says whether a chat owner may use a feature. With no
// auth database everyone may.
func (a *Application) studioFeatureAllowed(user *auth.User, feature string) bool {
	if a.authDB == nil || user == nil {
		return true
	}
	return auth.HasFeatureAccess(a.authDB, user, feature)
}

// loadStudioUser returns the active user with this ID. A chat acts as its
// owner, so an account that was disabled or never approved is refused, and so
// is one whose chats feature an administrator switched off. The route gate
// checks the feature for a request; this check is for what runs without one:
// triggers, bots and background runs, which ask on every run.
func (a *Application) loadStudioUser(ctx context.Context, id string) (*auth.User, error) {
	if id == legacyAPIKeyUserID {
		return &auth.User{ID: id, Name: "API Key User", Role: auth.RoleAdmin, Status: auth.StatusActive}, nil
	}
	if a.authDB == nil {
		return nil, errors.New("no user database")
	}
	var u auth.User
	if err := a.authDB.WithContext(ctx).Where("id = ?", id).Take(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, studio.ErrNoUser
		}
		return nil, err
	}
	if u.Status != auth.StatusActive {
		return nil, studio.ErrUserDisabled
	}
	if !auth.HasFeatureAccess(a.authDB, &u, auth.FeatureChats) {
		return nil, studio.ErrChatsOff
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

// studioModalities says which inputs a chat model takes natively, from its
// configuration. An unknown model takes none.
func (a *Application) studioModalities(model string) studio.Modalities {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok {
		return studio.Modalities{}
	}
	return studio.Modalities{Vision: cfg.VisionSupported(), Video: cfg.VideoInputSupported()}
}

// studioAgentOptions is the agent: block of a model's config: the options of the
// tool loop that nib supports. The planning, loop detection and reasoning
// switches of the old loop have no counterpart and are ignored.
func (a *Application) studioAgentOptions(model string) nibharness.AgentOptions {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok {
		return nibharness.AgentOptions{}
	}
	return nibharness.AgentOptions{Iterations: cfg.Agent.MaxIterations, MaxAttempts: cfg.Agent.MaxAttempts, ForceReasoning: cfg.Agent.ForceReasoningTool}
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

// studioAgentMCPServers picks the servers a saved agent names from the model's
// configuration. Without the MCP feature none is used and all the names are
// missing.
func studioAgentMCPServers(cfg *config.ModelConfig, permitted bool, only []string) (servers, missing []string) {
	if !permitted {
		return nil, only
	}
	have, err := cfg.MCP.ServerNames()
	if err != nil {
		xlog.Error("Failed to parse MCP config", "model", cfg.Name, "error", err)
		return nil, only
	}
	for _, n := range only {
		if slices.Contains(have, n) {
			servers = append(servers, n)
		} else {
			missing = append(missing, n)
		}
	}
	return servers, missing
}

// studioMCP builds the MCP tool executor for a user's chat with a model. It is
// the chat route's logic without the request: the executor reuses MCP sessions
// cached by model name and config, and in a cluster routes calls to workers.
// For a chat with a saved agent (limited), the servers are the ones the agent
// names that the model's configuration has, whether or not the model picks them
// by itself; the names it cannot use come back as missing.
func (a *Application) studioMCP(ctx context.Context, user, model string, only []string, limited bool) (mcpendpoint.ToolExecutor, []string) {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	if !ok || !cfg.MCP.HasMCPServers() || a.applicationConfig.DisableMCP {
		return nil, only
	}
	permitted := true
	if a.authDB != nil {
		u, err := a.loadStudioUser(ctx, user)
		permitted = err == nil && auth.HasFeatureAccess(a.authDB, u, auth.FeatureMCP)
	}
	var servers, missing []string
	if limited {
		servers, missing = studioAgentMCPServers(&cfg, permitted, only)
	} else {
		servers = studioMCPServers(&cfg, permitted)
	}
	if len(servers) == 0 {
		return nil, missing
	}
	remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
	if err != nil {
		xlog.Error("Failed to parse MCP config", "model", model, "error", err)
		return nil, missing
	}
	var control mcpendpoint.AgentControl
	if d := a.Distributed(); d != nil {
		control = d.AgentControl
	}
	return mcpendpoint.NewToolExecutor(ctx, control, cfg.MCPSessionKey(), remote, stdio, servers), missing
}

// studioRemoteModel says whether a model is served by a remote proxy, so the
// cost of a trigger run can say that its tokens leave this server.
func (a *Application) studioRemoteModel(model string) bool {
	cfg, ok := a.ModelConfigLoader().GetModelConfig(model)
	return ok && cfg.IsRemoteProxy()
}

// studioSharedFolderAudit opens the audit of the shared folders, beside the
// consents to shared pages.
func (a *Application) studioSharedFolderAudit() sharedfolders.AuditStore {
	var (
		st  sharedfolders.AuditStore
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = sharedfolders.NewGORMAudit(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = sharedfolders.NewJSONAudit(filepath.Join(a.applicationConfig.DataPath, "chats", "shared-folders"))
	default:
		st, err = sharedfolders.NewJSONAudit("")
	}
	if err != nil {
		xlog.Warn("Chats: the audit of the shared folders is kept in memory", "error", err)
		st, _ = sharedfolders.NewJSONAudit("")
	}
	return st
}

// studioFindUser finds a user by ID, email or name, whatever their status, for
// the members of a shared folder. Without an auth database there is no user to
// find, and the shared folders take any principal.
func (a *Application) studioFindUser() func(ctx context.Context, ref string) (*auth.User, error) {
	if a.authDB == nil {
		return nil
	}
	return func(ctx context.Context, ref string) (*auth.User, error) {
		for _, col := range []string{"id", "email", "name"} {
			var users []auth.User
			if err := a.authDB.WithContext(ctx).Where(col+" = ?", ref).Limit(2).Find(&users).Error; err != nil {
				return nil, err
			}
			switch len(users) {
			case 1:
				return &users[0], nil
			case 2:
				// Two users share the name: ask for the email or the ID.
				return nil, fmt.Errorf("%w: more than one user is named %q; use the email or the ID", sharedfolders.ErrUnknownUser, ref)
			}
		}
		return nil, studio.ErrNoUser
	}
}

// studioPageConsents opens the store of the consents to shared pages, beside
// the triggers: the auth database (shared by the frontends of a cluster), a
// file under the data path, or memory.
func (a *Application) studioPageConsents() pages.ConsentStore {
	var (
		st  pages.ConsentStore
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = pages.NewGORMConsents(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = pages.NewJSONConsents(filepath.Join(a.applicationConfig.DataPath, "chats", "pages"))
	default:
		st, err = pages.NewJSONConsents("")
	}
	if err != nil {
		xlog.Warn("Chats: the consents to shared pages are kept in memory", "error", err)
		st, _ = pages.NewJSONConsents("")
	}
	return st
}
