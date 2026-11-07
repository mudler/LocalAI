package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	endpointsaddons "github.com/mudler/LocalAI/core/http/endpoints/addons"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/resources"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/signals"
)

// legacyAPIKeyUserID is the synthetic user the auth middleware gives a request
// that presents an environment-configured API key.
const legacyAPIKeyUserID = "legacy-api-key"

// StudioService returns the conversation service of the studio loop, or nil
// when it could not be built.
func (a *Application) StudioService() *studio.Service {
	return a.studioService
}

// StudioSelectionGuard is the check of the conversation selection of the
// add-on routes: a chat of a saved agent selects only a resource on the list
// of the agent. It is nil when the chats are off.
func (a *Application) StudioSelectionGuard() endpointsaddons.SelectionGuard {
	if a.studioService == nil {
		return nil
	}
	hasAccounts := a.authDB != nil
	return func(ctx context.Context, actor contract.Actor, conv, addonID, resourceID string) (bool, error) {
		user := addonWorkspacePrincipal(hasAccounts)(actor)
		if user == "" {
			return false, nil
		}
		return a.studioService.ResourceSelectable(ctx, user, conv, addonID, resourceID)
	}
}

// StudioBuiltins is the registry of the built-in chat tools an add-on may
// replace (bash; web_search and web_fetch register here too). It is nil when
// the chats are off. Settings > Tools lists what it holds.
func (a *Application) StudioBuiltins() *builtins.Registry {
	if a.studioService == nil {
		return nil
	}
	return a.studioService.Builtins()
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
	var replay studio.ReplayCarrier
	instance := ""
	if d := a.Distributed(); d != nil && d.Nats != nil {
		bus = studio.NewBroadcastBus(d.Nats, nil)
		fan = notify.NewBroadcastFanout(d.Nats, nil)
		// A client whose event stream reconnects to another frontend gets the
		// events it missed from the process that runs the turn.
		replay = studio.NewReplayCarrier(d.Nats, nil)
		instance = a.applicationConfig.Distributed.InstanceID
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
	a.studioWeb = newStudioWeb(a.applicationConfig, a.serverSelf())
	deps := studio.Deps{
		AutoResume:   a.applicationConfig.ChatsAutoResume,
		CloseTimeout: studioCloseTimeout,
		Bus:          bus,
		Replay:       replay,
		Instance:     instance,
		Store:        a.studioStore,
		Provider:     a.studioProvider,
		Users:        a.studioUsers(),
		Allowed:      a.studioFeatureAllowed,
		// Read at each turn, so the runtime setting applies at once.
		SuggestionsDefault: func() bool { return !a.applicationConfig.DisableChatSuggestions },
		StateDir:           stateDir,
		MCP:                a.studioMCP,
		MCPServers:         a.studioModelMCPServers,
		Agents:             a.studioAgents,
		// Saved agents are shared through the policy of the add-on owner.
		// The owner process holds it; a frontend reaches it through the
		// add-on carrier (addonSharingForward). Without either no agent is
		// shared through this process, which fails closed.
		AgentShares:       studio.PolicyAgentShares{Policy: a.AddonPolicy, Forward: a.addonSharingForward},
		ReferenceShares:   a.studioReferenceShares,
		Notify:            inbox,
		Push:              pusher,
		AddonTools:        lateStudioAddons{a},
		Assistant:         studioAssistant{a},
		ClientTools:       studioAssistant{a},
		Collections:       studioCollections{a},
		ContextTokens:     a.studioContextTokens,
		RemoteModel:       a.studioRemoteModel,
		PageConsents:      a.studioPageConsents(),
		SharedFolderAudit: a.studioSharedFolderAudit(),
		Proposals:         a.studioProposalStore(),
		UserSetting:       a.studioUserSetting,
		SetUserSetting:    a.studioSetUserSetting,
		EgressSettings:    a.studioEgressSettings,
		EgressAudit:       a.studioEgressAudit(),
		EgressAuditDays:   func() int { return a.applicationConfig.EffectiveChatsEgressAuditDays() },
		FindUser:          a.studioFindUser(),
		FolderDirectory:   a.studioFolderDirectory(),
		SharedPagesOn:     func() bool { return !a.applicationConfig.DisableSharedPages },
		Modalities:        a.studioModalities,
		MCPContent:        studioMCPContent{a: a},
		AgentOptions:      a.studioAgentOptions,
		Persistent:        a.studioStoreKind != "memory",
		Files:             a.StudioFiles(),
		Media:             media.New(a.studioProvider, media.UserLookup(a.studioUsers()), a.studioHelperModel).WithVisionChooser(a.studioVisionModel).WithClient(a.studioMediaClient),
		Web:               a.studioWeb,
	}
	if a.studioMCPConns != nil {
		deps.MCPConnectors = a.studioMCPConns
	}
	// The built-in bash runs commands on this machine when no Sandbox serves
	// bash. Its scratch folders are per process; the files are the truth.
	// The hooks run in chat turns, which start only after svcRef is set; it
	// is atomic because they read it from other goroutines.
	var svcRef atomic.Pointer[studio.Service]
	if f := a.StudioFiles(); f.Available() {
		hxDir := filepath.Join(stateDir, "host-exec")
		if dp := a.applicationConfig.DataPath; dp != "" {
			hxDir = filepath.Join(dp, "chats", "host-exec")
		}
		// The actor and the view of each command come from the chat's
		// resolved access, so they are the turn's: a chat whose agent works in
		// one folder gets a fenced actor, which the runner refuses.
		a.studioHostExec = hostexec.New(hostexec.Options{
			Store: f.Store(), StateDir: hxDir, Audit: a.studioHostExecAudit(), Geteuid: os.Geteuid,
			Actor: func(ctx context.Context, user, chat string) (workspace.Actor, error) {
				svc := svcRef.Load()
				if svc == nil {
					return workspace.Actor{}, errStudioStarting
				}
				return svc.CommandActor(ctx, user, chat)
			},
			View: func(ctx context.Context, act workspace.Actor) (workspace.View, error) {
				svc := svcRef.Load()
				if svc == nil {
					return workspace.View{}, errStudioStarting
				}
				return svc.CommandView(ctx, act)
			},
		})
		if r := a.studioHostExec.Unavailable(); r != "" {
			xlog.Info("Chats: the built-in bash cannot run commands on this machine", "reason", r)
		}
		deps.HostExec = a.studioHostExec
	}
	deps.HostExecMode = a.studioHostExecMode
	deps.SavedAgentsOn = func() bool { return !a.applicationConfig.DisableChatsSavedAgents }
	deps.LocalAIClientOn = func() bool { return !a.applicationConfig.DisableChatsLocalAIClient }
	deps.LocalAIAdminOn = func() bool { return !a.applicationConfig.DisableChatsLocalAIAdmin }
	deps.ToolSearchMode = a.applicationConfig.EffectiveChatsToolSearch
	deps.ExtractLimits = func() files.ExtractLimits {
		c := a.applicationConfig
		return files.ExtractLimits{
			MaxBytes: c.EffectiveChatsExtractMaxBytes(), MaxEntries: c.EffectiveChatsExtractMaxEntries(),
			MaxFileBytes: studioMaxFileSize, SevenZipRar: c.ChatsExtractSevenZipRar,
		}
	}
	deps.WorkFolderOn = func() bool { return !a.applicationConfig.DisableChatsWorkFolder }
	deps.GoalLimits = func() (int, int) {
		return a.applicationConfig.EffectiveChatsGoalMaxTurns(), a.applicationConfig.EffectiveChatsGoalMaxSteps()
	}
	deps.IsAdmin = a.studioIsAdmin
	deps.ToolPins = a.studioToolPins
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
	svcRef.Store(svc)
	// The kind "agent" registers with the policy when the add-on owner opens
	// it, or at once when it is already open.
	if err := a.RegisterShareKinds(svc); err != nil {
		xlog.Warn("Chats: saved agents cannot be shared", "error", err)
	}
	a.startStudioReaper(svc)
	a.startStudioEgressPrune(svc)
	if a.applicationConfig.Distributed.ChatWorker {
		// The hosts that start unattended runs keep their leader locks on the
		// frontends; this process runs the turns they send (StartChatWorker).
		xlog.Info("Chats: this is a chat worker; triggers, connectors and agent jobs start on the frontends")
		return
	}
	a.startStudioConnectors(svc)
	if a.ownsAddonsOnly() {
		xlog.Info("Chats: this is the add-on owner; connectors, triggers, loops and agent jobs run on the frontends")
	} else {
		a.startStudioTriggers(svc)
		a.startStudioLoops(svc)
		a.startStudioJobs(svc)
	}
	a.prepareStudioConversion(svc)
}

// ownsAddonsOnly says whether this process is a dedicated add-on owner
// (--addons-role=owner). It hosts the add-ons and has no models, so the
// background work that starts chat turns belongs to the frontends.
func (a *Application) ownsAddonsOnly() bool {
	return a.applicationConfig.Addons.DedicatedOwner
}

// errStudioStarting is the error of a command hook called before the chat
// service is up. No chat turn runs then, so it is not expected.
var errStudioStarting = errors.New("chats: the service is starting")

// studioCloseTimeout bounds studio.Service.Close in this process. The
// termination hooks, Close among them, are cut off after
// signals.DefaultHookTimeout, so a bound of the service's own default (25
// seconds) would never fire here: the process would exit first and leave its
// turns claimed until the reaper finds them. Marking them interrupted needs a
// few seconds of the budget as well.
const studioCloseTimeout = signals.DefaultHookTimeout - 5*time.Second

// A turn beats every 15 seconds; one that has not beaten for this long belongs
// to a frontend that died.
const (
	studioTurnTimeout = time.Minute
	studioReapEvery   = 30 * time.Second
)

// startStudioReaper frees the chats whose turn was left running by a process
// that died. Only turns that stopped beating are reaped: the database may be
// shared with another live process, whose turns must go on. A process that
// died leaves a claim that goes stale within studioTurnTimeout, and the next
// tick frees it. In a cluster one frontend does this at each tick, the one that
// holds the advisory lock.
func (a *Application) startStudioReaper(svc *studio.Service) {
	ctx := a.backgroundContext()
	reap := func() {
		n, err := svc.ReapStale(ctx, studioTurnTimeout)
		if err != nil {
			xlog.Warn("Chats: could not reap stale turns", "error", err)
		} else if n > 0 {
			xlog.Info("Chats: ended turns whose frontend stopped", "turns", n)
		}
	}
	// The same tick resumes the interrupted turns that are due (see
	// studio.Service.SweepResumes). The store's claim keeps two sweeps from
	// starting one turn twice, and the lock keeps the work on one frontend.
	sweep := func() {
		n, err := svc.SweepResumes(ctx)
		if err != nil {
			xlog.Warn("Chats: could not resume interrupted turns", "error", err)
		} else if n > 0 {
			xlog.Info("Chats: resumed interrupted turns", "turns", n)
		}
	}
	if a.authDB == nil {
		// The JSON store settles its own claims when it loads, so nothing is
		// left to reap; the turns it interrupted may still resume.
		if a.applicationConfig.ChatsAutoResume {
			a.everyStudioTick(ctx, sweep)
		}
		return
	}
	step := func() { reap(); sweep() }
	if a.Distributed() != nil {
		go advisorylock.RunLeaderLoop(ctx, a.authDB, advisorylock.KeyChatsReaper, studioReapEvery, step)
		return
	}
	// The conditional update of a reap is safe to race with another process.
	step()
	a.everyStudioTick(ctx, step)
}

// everyStudioTick runs f every studioReapEvery until ctx ends.
func (a *Application) everyStudioTick(ctx context.Context, f func()) {
	go func() {
		tick := time.NewTicker(studioReapEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				f()
			}
		}
	}()
}

// studioUsers returns the user lookup, or nil when auth is off.
func (a *Application) studioUsers() studio.UserLookup {
	if !a.AuthActive() {
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

// studioModelConfig is the config of the model that serves a name. An alias is
// a redirect whose own config carries no model settings (the loader gives it the
// generic default window), so the settings come from its target.
func (a *Application) studioModelConfig(model string) (config.ModelConfig, bool) {
	bcl := a.ModelConfigLoader()
	name, _ := bcl.ResolveAliasName(model)
	return bcl.GetModelConfig(name)
}

// studioContextTokens is the context window a request to the model can use. A
// config without context_size runs the backend with its default window, so that
// is the limit (the same one /v1/models/capabilities reports), not a guess of
// the harness. Zero only for a model with no local config (a remote one), whose
// window is not known here.
func (a *Application) studioContextTokens(model string) int {
	cfg, ok := a.studioModelConfig(model)
	if !ok {
		return 0
	}
	return corebackend.EffectiveRequestContextSize(cfg)
}

// studioModalities says which inputs a chat model takes natively, from its
// configuration. An unknown model takes none.
func (a *Application) studioModalities(model string) studio.Modalities {
	cfg, ok := a.studioModelConfig(model)
	if !ok {
		return studio.Modalities{}
	}
	return studio.Modalities{Vision: cfg.VisionSupported(), Video: cfg.VideoInputSupported()}
}

// studioAgentOptions is the agent: block of a model's config: the options of the
// tool loop that nib supports. The planning, loop detection and reasoning
// switches of the old loop have no counterpart and are ignored. The step
// limit is the model's agent.max_iterations, else chats_max_iterations.
func (a *Application) studioAgentOptions(model string) nibharness.AgentOptions {
	opts := nibharness.AgentOptions{Iterations: a.applicationConfig.EffectiveChatsMaxIterations()}
	cfg, ok := a.studioModelConfig(model)
	if !ok {
		return opts
	}
	if cfg.Agent.MaxIterations > 0 {
		opts.Iterations = cfg.Agent.MaxIterations
	}
	opts.MaxAttempts, opts.ForceReasoning = cfg.Agent.MaxAttempts, cfg.Agent.ForceReasoningTool
	return opts
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

// studioHostExecMode is chats_host_exec as it applies now. Empty is the
// default: off on a server with accounts (several people), ask without.
func (a *Application) studioHostExecMode() string {
	if m := a.applicationConfig.ChatSelectionSnapshot().HostExec; m != "" {
		return m
	}
	if a.authDB != nil {
		return hostexec.ModeOff
	}
	return hostexec.ModeAsk
}

// studioHostExecAudit opens the audit of the commands run on this machine,
// beside the audit of the shared folders.
func (a *Application) studioHostExecAudit() hostexec.AuditStore {
	var (
		st  hostexec.AuditStore
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = hostexec.NewGORMAudit(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = hostexec.NewJSONAudit(filepath.Join(a.applicationConfig.DataPath, "chats", "host-exec-audit"))
	default:
		st, err = hostexec.NewJSONAudit("")
	}
	if err != nil {
		xlog.Warn("Chats: the audit of the commands on this machine is kept in memory", "error", err)
		st, _ = hostexec.NewJSONAudit("")
	}
	return st
}

// studioEgressSettings is the chats_egress_* settings as they apply now. A
// list value that does not parse keeps the last valid one (see lastGood).
func (a *Application) localStudioEgressSettings() egress.Settings {
	cfg := a.applicationConfig
	a.studioEgressListsOnce.Do(a.initStudioEgressLists)
	return egress.Settings{
		Ceiling:         egress.Preset(strings.TrimSpace(cfg.ChatsEgressCeiling)),
		Default:         egress.Preset(strings.TrimSpace(cfg.ChatsEgressDefault)),
		AllowPrivate:    cfg.ChatsEgressAllowPrivate,
		Ports:           a.studioEgressPorts.get(cfg.ChatsEgressPorts),
		ExtraRegistries: a.studioEgressExtra.get(cfg.ChatsEgressExtraRegistries),
		AllowedHosts:    a.studioEgressAllowed.get(cfg.ChatsEgressAllowedHosts),
	}
}

func (a *Application) initStudioEgressLists() {
	a.studioEgressPorts = newLastGood("chats_egress_ports", config.ParseEgressPorts, []int{80, 443})
	a.studioEgressExtra = newLastGood("chats_egress_extra_registries", config.ParseEgressHosts, nil)
	a.studioEgressAllowed = newLastGood("chats_egress_allowed_hosts", func(raw string) ([]egress.AllowedHost, error) {
		// The shape is judged here; Decide keeps a private literal away
		// from the list whatever the private switch says.
		hosts, err := config.ParseEgressAllowedHosts(raw, true)
		if err != nil {
			return nil, err
		}
		out := make([]egress.AllowedHost, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, egress.AllowedHost{Host: h.Host, Port: h.Port})
		}
		return out, nil
	}, nil)
}

// studioEgressAudit opens the audit of the command network requests, beside
// the audit of the commands run on this machine.
func (a *Application) studioEgressAudit() audit.Store {
	var (
		st  audit.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = audit.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = audit.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "egress-audit"))
	default:
		st, err = audit.NewJSON("")
	}
	if err != nil {
		xlog.Warn("Chats: the audit of the command network is kept in memory", "error", err)
		st, _ = audit.NewJSON("")
	}
	return st
}

// studioEgressPruneEvery is how often the network audit loses its old lines.
const studioEgressPruneEvery = 24 * time.Hour

// startStudioEgressPrune removes the old lines of the network audit and of the
// audit of the commands on this machine once at start and then daily. With a database one frontend does it at each tick,
// under an advisory lock; a standalone process does it alone.
func (a *Application) startStudioEgressPrune(svc *studio.Service) {
	ctx := a.backgroundContext()
	prune := func() {
		n, err := svc.PruneEgressAudit(ctx)
		if err != nil {
			xlog.Warn("Chats: could not prune the command network audit", "error", err)
		} else if n > 0 {
			xlog.Info("Chats: pruned the command network audit", "lines", n)
		}
		n, err = svc.PruneHostExecAudit(ctx)
		if err != nil {
			xlog.Warn("Chats: could not prune the audit of the commands on this machine", "error", err)
		} else if n > 0 {
			xlog.Info("Chats: pruned the audit of the commands on this machine", "lines", n)
		}
	}
	if a.authDB != nil && a.Distributed() != nil {
		go advisorylock.RunLeaderLoop(ctx, a.authDB, advisorylock.KeyChatsEgressPrune, studioEgressPruneEvery, prune)
		return
	}
	go func() {
		prune()
		t := time.NewTicker(studioEgressPruneEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				prune()
			}
		}
	}()
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

// studioProposalStore opens the store of the change proposals beside the
// chats: the auth database (shared by the frontends of a cluster), a file under
// the data path, or memory.
func (a *Application) studioProposalStore() proposals.Store {
	var (
		st  proposals.Store
		err error
	)
	switch {
	case a.authDB != nil:
		st, err = proposals.NewGORM(a.authDB)
	case a.applicationConfig.DataPath != "":
		st, err = proposals.NewJSON(filepath.Join(a.applicationConfig.DataPath, "chats", "proposals"))
	default:
		st, err = proposals.NewJSON("")
	}
	if err != nil {
		xlog.Warn("Chats: proposed changes are kept in memory", "error", err)
		st, _ = proposals.NewJSON("")
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

// studioFolderDirectory is the account directory people pick from when they
// share a folder of their own home: the user picker of the sharing routes,
// with its rules and its own rate per person. The picker is this frontend's
// own, so the rate is counted per frontend, not for the whole cluster.
// Without an account database there is none and folders are not shared by
// their owners.
func (a *Application) studioFolderDirectory() sharedfolders.Directory {
	if a.authDB == nil {
		return nil
	}
	u := auth.NewAddonUsers(a.authDB)
	return folderDirectory{u: u, picker: resources.NewAccountPicker(func() resources.AccountFinder { return u.Accounts }, 0, 0, nil)}
}

type folderDirectory struct {
	u      *auth.AddonUsers
	picker *resources.AccountPicker
}

func (d folderDirectory) Accounts(ctx context.Context, caller, query string, limit int) ([]sharedfolders.User, error) {
	list, err := d.picker.Search(ctx, caller, "", query, limit, nil)
	switch {
	case errors.Is(err, gateway.ErrSearchLimited):
		return nil, sharedfolders.ErrSearchLimited
	case errors.Is(err, gateway.ErrInvalidRequest):
		return nil, sharedfolders.ErrBadQuery
	case err != nil:
		return nil, err
	}
	out := make([]sharedfolders.User, 0, len(list.Users))
	for _, r := range list.Users {
		out = append(out, sharedfolders.User{ID: r.ID, Name: r.DisplayName})
	}
	return out, nil
}

func (d folderDirectory) Active(ctx context.Context, id string) (sharedfolders.User, error) {
	if err := d.u.Active(ctx, id); err != nil {
		if errors.Is(err, gateway.ErrForbidden) {
			return sharedfolders.User{}, sharedfolders.ErrUnknownUser
		}
		return sharedfolders.User{}, err
	}
	names, err := d.u.Lookup(ctx, []string{id})
	if err != nil {
		return sharedfolders.User{}, err
	}
	return sharedfolders.User{ID: id, Name: names[id]}, nil
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

// backgroundContext is the context of the background loops of the
// application: the one of the configuration, or Background when the caller
// gave none.
func (a *Application) backgroundContext() context.Context {
	if ctx := a.applicationConfig.Context; ctx != nil {
		return ctx
	}
	return context.Background()
}
