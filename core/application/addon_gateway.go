// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/artifacts"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/resources"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

func (a *Application) AddonGateway() *gateway.Gateway { return a.addonGateway }

// AddonToolProvider binds the tools of installed addons to the principal of a
// chat request. It is nil when the host has no addon gateway: the chat
// handler then rejects an opt-in with a typed error. The provider reads the
// ownership mode from configuration, never from a request.
func (a *Application) AddonToolProvider() *mcpTools.AddonToolProvider {
	if a.addonGateway == nil {
		return nil
	}
	p := mcpTools.NewAddonToolProvider(addontools.NewToolAdapter(a.addonGateway), a.AddonOwnershipMode())
	if db := a.AuthDB(); db != nil {
		has := addonFeatureCheck(db)
		hostOps := a.applicationConfig.AddonHostOps
		p = p.WithHidden(func(actor contract.Actor) map[string]bool { return addonsWithoutFeature(hostOps, has, actor) })
	}
	return p
}

// AddonHostOps is the host operation channel of the add-on supervisor, or nil
// when this process offers none.
func (a *Application) AddonHostOps() *addonruntime.HostOps { return a.addonHostOps }

// newAddonHostOps builds the host operation broker for the supervisor of the
// owner. A frontend without the add-on manager, or a host whose setting names
// no add-on, builds nothing, so the channel fails closed. The setting is a
// host setting read at startup; no manifest or request can add to it.
func (a *Application) newAddonHostOps(m *addons.Manager, limits contract.Limits) *addonruntime.HostOps {
	allowed := a.applicationConfig.AddonHostOps
	if m == nil || len(allowed) == 0 {
		return nil
	}
	in := hostops.NewInFlight()
	broker := hostops.New(in, addonFeatureCheck(a.authDB), limits)
	broker.SetAdminCheck(addonAdminCheck(a.authDB))
	return &addonruntime.HostOps{Broker: broker, InFlight: in, Allowed: allowed}
}

// RegisterAddonEngineJobs registers the engine job operations with the services
// the routes use. The broker is built at startup and the services after it, so
// the HTTP setup calls this once they exist. A frontend without the broker has
// nothing to register, and the channel then fails closed.
func (a *Application) RegisterAddonEngineJobs(ft hostops.FineTuner, q hostops.Quantizer, backends hostops.BackendLister) error {
	if a.addonHostOps == nil {
		return nil
	}
	return hostops.RegisterEngineJobs(a.addonHostOps.Broker, ft, q, backends)
}

// RegisterAddonEngineProgress registers the progress and stop operations over
// the same services as RegisterAddonEngineJobs. The sources are wrapped in the
// bounded-wait reader here; a nil source registers nothing for its kind.
func (a *Application) RegisterAddonEngineProgress(ft, q hostops.ProgressSource) error {
	if a.addonHostOps == nil {
		return nil
	}
	var ftp, qp hostops.Progresser
	if ft != nil {
		ftp = hostops.NewProgresser(ft)
	}
	if q != nil {
		qp = hostops.NewProgresser(q)
	}
	return hostops.RegisterEngineProgress(a.addonHostOps.Broker, ftp, qp)
}

// RegisterAddonEngineModels registers the model registration operations over
// the fine-tuning and quantization services. A nil source registers nothing
// for its kind.
func (a *Application) RegisterAddonEngineModels(ex hostops.Exporter, im hostops.Importer) error {
	if a.addonHostOps == nil {
		return nil
	}
	// The owner writes the exports, so it refuses them while its models path
	// does not hold its probe.
	ok := a.ownerProbeCurrent
	if ex != nil {
		ex = guardedExporter{Exporter: ex, ok: ok}
	}
	if im != nil {
		im = guardedImporter{Importer: im, ok: ok}
	}
	return hostops.RegisterEngineModels(a.addonHostOps.Broker, ex, im)
}

// RegisterAddonEngineDatasets registers the inline dataset operation over the
// fine-tuning dataset store. A nil store registers nothing.
func (a *Application) RegisterAddonEngineDatasets(ds hostops.DatasetStore) error {
	if a.addonHostOps == nil {
		return nil
	}
	return hostops.RegisterEngineDatasets(a.addonHostOps.Broker, ds)
}

// RegisterAddonWorkspace registers the workspace operations over the files of
// the chats. It registers nothing when this process has no host operation
// channel, no add-on runs here, or the files are off.
func (a *Application) RegisterAddonWorkspace(f *files.Service) error {
	if a.addonHostOps == nil || a.addonDataRoot == "" || !f.Available() {
		return nil
	}
	root := a.addonDataRoot
	return hostops.RegisterWorkspace(a.addonHostOps.Broker, &hostops.WorkspaceHost{
		Store:     f.Store(),
		Files:     f,
		Principal: func(actor contract.Actor) string { return addonWorkspacePrincipal(a.authDB != nil)(actor) },
		DataDir:   func(id string) string { return filepath.Join(root, id) },
	})
}

// addonWorkspacePrincipal maps an add-on actor to the owner of the files.
// Without accounts the chats and the Files page use the anonymous principal,
// so the synthetic local-owner actor must land there too, or the add-ons would
// work in a home the user never sees. With accounts the local-owner actor is
// no person: it has no files, and the workspace operations refuse it rather
// than share one account's home. An empty answer means refuse.
func addonWorkspacePrincipal(hasAccounts bool) func(contract.Actor) string {
	return func(actor contract.Actor) string {
		if actor.UserID == "" || actor.UserID == auth.AddonLocalOwnerID {
			if hasAccounts {
				return ""
			}
			return studio.AnonymousPrincipal
		}
		return actor.UserID
	}
}

// addonFeatureCheck answers whether an active account holds a feature, with
// the same rule as the route middleware. Without an account database, or for
// an account that is not active, the answer is no.
func addonFeatureCheck(db *gorm.DB) func(userID, feature string) bool {
	return func(userID, feature string) bool {
		if db == nil || userID == "" {
			return false
		}
		var u auth.User
		if db.Where("id = ? AND status = ?", userID, auth.StatusActive).First(&u).Error != nil {
			return false
		}
		return auth.HasFeatureAccess(db, &u, feature)
	}
}

// addonAdminCheck answers whether an actor is an administrator. With an account
// database the role of the active account decides in every mode, with the rule
// of the admin routes (auth.RequireAdmin); a local-owner actor is not an
// administrator by mode alone. Without an account database there are no roles,
// so the local owner of the install is the administrator and no other actor is.
func addonAdminCheck(db *gorm.DB) func(actor contract.Actor) bool {
	return func(actor contract.Actor) bool {
		if db == nil {
			return actor.Mode == "local-owner"
		}
		if actor.UserID == "" {
			return false
		}
		var u auth.User
		if db.Where("id = ? AND status = ?", actor.UserID, auth.StatusActive).First(&u).Error != nil {
			return false
		}
		return u.Role == auth.RoleAdmin
	}
}

// AddonPolicy is the owner-local resource policy. It is nil on frontends and on
// hosts without the addon transport, which then deny every resource.
func (a *Application) AddonPolicy() *policy.Policy { return a.addonPolicy }

// AddonOwnershipMode is the configured ownership mode. Named-user is the
// default; local-owner exists only when the deployment asked for it.
func (a *Application) AddonOwnershipMode() string {
	if m := a.applicationConfig.Addons.OwnershipMode; m != "" {
		return m
	}
	return auth.AddonModeNamedUser
}

// addonStatus is the manager view that creation authority consults.
type addonStatus interface {
	Status(ctx context.Context, id string) (addons.Status, error)
}

// addonCreateAuthority lets an actor create a resource only for an addon that
// is installed and enabled on the owner, with no lifecycle change in flight.
// A manager that cannot answer is unavailable, never a grant.
func addonCreateAuthority(status addonStatus, revalidate func(context.Context, contract.Actor) error) func(context.Context, contract.Actor, string) error {
	return func(ctx context.Context, actor contract.Actor, addonID string) error {
		if status == nil || revalidate == nil {
			return gateway.ErrForbidden
		}
		if err := revalidate(ctx, actor); err != nil {
			return err
		}
		s, err := status.Status(ctx, addonID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("addon status: %w: %w", gateway.ErrUnavailable, err)
		}
		if !s.Installed || !s.DesiredEnabled || !s.StartEligible {
			return gateway.ErrForbidden
		}
		return nil
	}
}

// confirmCredential says which credentials may confirm a proposal: the
// browser session or the local owner, as before, and the credential of a chat
// turn. An API key and an MCP session never can.
func confirmCredential(mode string) func(contract.Actor) bool {
	return func(a contract.Actor) bool {
		if a.CredentialID == auth.AddonChatsCredential {
			return mode == auth.AddonModeNamedUser && a.Mode == auth.AddonModeNamedUser
		}
		if mode == auth.AddonModeLocalOwner {
			return a.CredentialID == auth.AddonLocalOwnerID
		}
		id, ok := strings.CutPrefix(a.CredentialID, "session:")
		return ok && id != ""
	}
}

func (a *Application) startAddonGateway() error {
	cfg := a.applicationConfig.Addons
	if cfg.Transport == nil {
		return nil
	}
	mode := a.AddonOwnershipMode()
	if mode == auth.AddonModeNamedUser && a.authDB == nil {
		return gateway.ErrForbidden
	}
	c := *cfg.Transport
	if c.InstanceID != cfg.InstanceID || c.OwnerID != cfg.OwnerID || c.OwnerAddress != cfg.OwnerAddress {
		return gateway.ErrForbidden
	}
	carrier, e := gateway.NewCarrier(c)
	if e != nil {
		return e
	}
	if mode == auth.AddonModeLocalOwner {
		xlog.Warn(localOwnerWarning)
	}
	g := &gateway.Gateway{Limits: c.Limits, Manager: a.addonManager, Revalidate: auth.AddonRevalidate(a.authDB, mode)}
	if cfg.InstanceID != cfg.OwnerID {
		g.Forward = carrier
		a.addonGateway = g
		// The frontend installs backends on its own node and takes the
		// resolved plan from the owner.
		a.backendLibrary = &backendlib.Installer{
			Remote: remoteBackends{a}, SystemState: a.applicationConfig.SystemState, ModelLoader: a.modelLoader,
			MaxBytes: cfg.BackendMaxBytes, RequireIntegrity: a.applicationConfig.RequireBackendIntegrity,
		}
		go a.runLibraryRefresh(a.applicationConfig.Context)
		go a.runModelsProbe(a.applicationConfig.Context)
		return nil
	}
	// Only the owner holds the policy store. Other frontends reach it through the
	// gateway, and without it the gateway keeps denying every resource.
	if !filepath.IsAbs(cfg.Root) || cfg.ListenAddress == "" {
		return gateway.ErrForbidden
	}
	store, e := policy.OpenStore(filepath.Join(cfg.Root, "policy"), policy.Mode(mode))
	if e != nil {
		return e
	}
	// Proposals are durable beside the policy store and held only here. The
	// broker is the policy's proposal invalidator, so a revocation cancels the
	// pending proposals of that user before it is acknowledged.
	brokerOpts := policy.BrokerOptions{}
	if a.addonManager != nil {
		brokerOpts.Runtime = a.addonManager
	}
	// The same existence check settles the registrations a crash left and the
	// reservations of create proposals the proposer stops waiting for.
	exists := a.addonResourceExists
	if exists == nil {
		exists = a.addonBackingExists(contract.DefaultLimits())
	}
	brokerOpts.BackingExists = exists
	// Enabling an add-on for a chat or an agent is the consent to its changes,
	// so the chats' own credential may confirm what the chats adapter has just
	// stored. No request carries it: sessions and API keys build their own.
	brokerOpts.ConfirmCredential = confirmCredential(mode)
	// A deletion trusts only an explicit answer of the add-on.
	confirmed := a.addonResourceExists
	if confirmed == nil {
		confirmed = a.addonBackingConfirmed(contract.DefaultLimits())
	}
	broker, e := policy.OpenBroker(filepath.Join(cfg.Root, "proposals"), brokerOpts)
	if e != nil {
		_ = store.Close()
		return e
	}
	p := policy.New(store, policy.Options{
		Revalidate:      g.Revalidate,
		CreateAuthority: addonCreateAuthority(a.addonManager, g.Revalidate),
		Proposals:       broker,
	})
	broker.Attach(p)
	// Recovery gets the owner's access state. A restore or a purge that a
	// crash stopped is finished now, before anything is served; an add-on
	// that needs recovery stays unavailable until an administrator acts.
	if a.addonManager != nil {
		if rec := a.addonManager.DataRecovery(); rec != nil {
			rec.SetPolicy(&policy.RecoveryPort{Policy: p, Broker: broker})
			if e = rec.Reconcile(a.applicationConfig.Context); e != nil {
				xlog.Warn("addons: a data recovery journal could not be finished yet", "error", e)
			}
		}
	}
	var files *artifacts.Artifacts
	var selections *resources.SelectionStore
	closeState := func() {
		if files != nil {
			_ = files.Close()
		}
		if selections != nil {
			_ = selections.Close()
		}
		if a.addonReconcile != nil {
			a.addonReconcile.Close()
		}
		_ = broker.Close()
		_ = store.Close()
	}
	// Registrations left pending by a crash are settled before anything is
	// served. A record whose backing cannot be confirmed now (its addon is
	// disabled or failed to start) stays pending and hidden; it never stops
	// the host from starting, and the loop below settles it later.
	rep, e := p.ReconcileReport(a.applicationConfig.Context, exists)
	if e != nil {
		closeState()
		return errors.Join(e, addons.ErrRecoveryRequired)
	}
	if rep.Kept > 0 {
		xlog.Warn("addons: pending resource registrations could not be settled yet and stay pending", "settled", rep.Settled, "kept", rep.Kept)
	}
	// The conversation selections and the UI-only resource management sit
	// beside the policy store and are held only here.
	selections, e = resources.OpenSelections(filepath.Join(cfg.Root, "selections"), resources.SelectionOptions{})
	if e != nil {
		closeState()
		return errors.Join(e, addons.ErrRecoveryRequired)
	}
	resourceOpts := resources.Options{Policy: p, Broker: broker, Users: auth.NewAddonUsers(a.authDB), Selections: selections, BackingConfirmed: confirmed, Limits: contract.DefaultLimits()}
	if a.addonManager != nil {
		resourceOpts.Runtime, resourceOpts.Capabilities, resourceOpts.Tools = a.addonManager, a.addonManager, a.addonManager
	}
	manage := resources.New(resourceOpts)
	g.Resources = manage
	// The owner serves the library calls of the frontends. A node with no
	// source store (no catalog) has nothing to serve.
	if a.addonSources != nil {
		g.Libraries = a.newLibraryOwner()
		g.OS, g.Arch = cfg.Host.OS, cfg.Host.Arch
	}
	g.ModelsProbe = func() (modelsprobe.Probe, bool) {
		p := a.modelsProbe.own.Load()
		if p == nil {
			return modelsprobe.Probe{}, false
		}
		return *p, true
	}
	loop := policy.NewReconcileLoop(p, exists, a.addonReconcileTick)
	loop.Resume = manage.Resume
	loop.Report = func(r policy.ReconcileReport, err error) {
		if err != nil || r.Kept > 0 {
			xlog.Warn("addons: pending resource registrations remain", "settled", r.Settled, "kept", r.Kept, "error", err)
		}
	}
	// A deletion a crash left is resumed before anything is served; one that
	// cannot be finished now stays inaccessible and the loop retries it.
	manage.Resume(a.applicationConfig.Context)
	loop.Start(a.applicationConfig.Context)
	a.addonReconcile = loop
	// A proposal that was executing when the owner stopped is settled by
	// receipt lookup, or recorded as outcome-unknown. Nothing is applied again.
	if e = broker.Recover(a.applicationConfig.Context); e != nil {
		closeState()
		return errors.Join(e, addons.ErrRecoveryRequired)
	}
	// Unresolved outcomes are looked up again with backoff for a finite
	// window, and expiry stays recorded. Run ends with the application
	// context or when the broker is closed.
	go broker.Run(a.applicationConfig.Context)
	provider := p.Gateway()
	g.Admission, g.Resolver = provider, provider
	g.Proposals = broker.Gateway()
	// Private file results are staged beside the other owner state. Without a
	// manager there is no runtime to read them from, and the gateway then
	// denies every file result and download.
	if a.addonManager != nil {
		files, e = artifacts.Open(artifacts.Options{
			Dir:     filepath.Join(cfg.Root, "artifacts"),
			Policy:  p,
			Runtime: a.addonManager,
			Addon:   artifactAddonState(a.addonManager),
			Limits:  artifacts.DefaultLimits().WithExpiry(cfg.ArtifactExpiry),
		})
		if e != nil {
			closeState()
			return e
		}
		provider.Outputs = files
		g.Artifacts = files.Gateway()
		go files.Run(a.applicationConfig.Context)
	}
	handler, e := carrier.OwnerHandler(g)
	if e != nil {
		closeState()
		return e
	}
	listener, e := net.Listen("tcp", cfg.ListenAddress)
	if e != nil {
		closeState()
		return e
	}
	server := &http.Server{Handler: handler, TLSConfig: carrier.ServerTLS(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 65 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	a.addonServer = server
	a.addonGateway = g
	a.addonPolicy, a.addonStore = p, store
	if pinStore, err := a.AddonPinStore(); err != nil {
		xlog.Warn("addon pin sweep not started", "error", err)
	} else {
		a.startAddonPinSweep(a.applicationConfig.Context, pinStore)
	}
	go func() { _ = server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	go func() { <-a.applicationConfig.Context.Done(); _ = server.Close(); closeState() }()
	return nil
}

// addonBackingExists is the real existence check for resources whose backing
// lives in an addon (Tables databases). Without a manager there is no addon
// that could back a resource, so a pending record is dropped as before.
func (a *Application) addonBackingExists(limits contract.Limits) func(context.Context, policy.ResourceKey) (bool, error) {
	m := a.addonManager
	if m == nil {
		return func(context.Context, policy.ResourceKey) (bool, error) { return false, nil }
	}
	return policy.BackingExists(policy.BackingOptions{
		Runtime: m,
		Tools:   m,
		Installed: func(ctx context.Context, id string) (bool, error) {
			s, err := m.Status(ctx, id)
			return s.Installed, err
		},
		Limits: limits,
	})
}

// addonBackingConfirmed is the existence check of a deletion. Unlike
// addonBackingExists it never reads a missing manager, an add-on that is not
// installed or one that does not declare the existence tool as "gone".
func (a *Application) addonBackingConfirmed(limits contract.Limits) func(context.Context, policy.ResourceKey) (bool, error) {
	m := a.addonManager
	if m == nil {
		return func(context.Context, policy.ResourceKey) (bool, error) { return false, policy.ErrBackingUnanswered }
	}
	return policy.BackingConfirmed(policy.BackingOptions{
		Runtime: m,
		Tools:   m,
		Installed: func(ctx context.Context, id string) (bool, error) {
			s, err := m.Status(ctx, id)
			return s.Installed, err
		},
		Limits: limits,
	})
}

// artifactAddonState reports the lifecycle state the artifact store needs. A
// manager that cannot answer is an error, never an enabled addon.
func artifactAddonState(status addonStatus) func(context.Context, string) (artifacts.AddonState, error) {
	return func(ctx context.Context, addonID string) (artifacts.AddonState, error) {
		s, err := status.Status(ctx, addonID)
		if err != nil {
			return artifacts.AddonState{}, err
		}
		return artifacts.AddonState{Installed: s.Installed, Enabled: s.DesiredEnabled, Generation: s.Generation}, nil
	}
}

// localOwnerWarning is logged at startup in local-owner mode. There is no
// session in that mode, so the confirmation boundary is the browser headers
// only and does not stop a script that sets them.
const localOwnerWarning = "addons: local-owner mode is enabled; unauthenticated callers share one authority over every addon resource and sharing is disabled; any caller that can reach this port can confirm addon proposals"

// StudioAddonTools is the add-on broker as the chats use it, or nil when this
// host has no add-on gateway. A chat is not a request: its turn runs after the
// request ended and, in a cluster, can run on another frontend. The actor is
// therefore built from the chat's user and the credential of a chat turn, and
// the gateway checks the account and the add-ons feature again on every call.
// The ownership mode comes from configuration, never from a request.
func (a *Application) StudioAddonTools() studio.AddonTools {
	if a.addonGateway == nil {
		return nil
	}
	var has func(userID, feature string) bool
	if db := a.AuthDB(); db != nil {
		has = addonFeatureCheck(db)
	}
	return &studioAddons{adapter: addontools.NewChatToolAdapter(a.addonGateway), mode: a.AddonOwnershipMode(), hostOps: a.applicationConfig.AddonHostOps, has: has}
}

// lateStudioAddons asks for the broker at each call: the chats service starts
// before the add-on gateway does.
type lateStudioAddons struct{ a *Application }

func (l lateStudioAddons) Tools(ctx context.Context, user string) ([]studio.AddonTool, error) {
	b := l.a.StudioAddonTools()
	if b == nil {
		return nil, nil
	}
	return b.Tools(ctx, user)
}

// Unavailable forwards to the broker when it can say why an add-on cannot work.
func (l lateStudioAddons) Unavailable(ctx context.Context, user, addonID string, workspace bool) string {
	if g, ok := l.a.StudioAddonTools().(studio.AddonGate); ok {
		return g.Unavailable(ctx, user, addonID, workspace)
	}
	return ""
}

// AddonSkills forwards to the broker when it knows the skills of the add-ons.
func (l lateStudioAddons) AddonSkills(ctx context.Context, user string) ([]studio.AddonSkillSet, error) {
	if g, ok := l.a.StudioAddonTools().(studio.AddonSkillLister); ok {
		return g.AddonSkills(ctx, user)
	}
	return nil, nil
}

// Isolation forwards to the broker when it can say how commands are isolated.
func (l lateStudioAddons) Isolation(ctx context.Context, user, addonID string) string {
	if g, ok := l.a.StudioAddonTools().(studio.CommandIsolation); ok {
		return g.Isolation(ctx, user, addonID)
	}
	return ""
}

func (l lateStudioAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	b := l.a.StudioAddonTools()
	if b == nil {
		return "", gateway.ErrUnavailable
	}
	return b.Call(ctx, user, conv, name, args)
}

type studioAddons struct {
	adapter *addontools.ToolAdapter
	mode    string
	// hostOps maps an add-on ID to the host operation prefixes it may call. An
	// add-on that may call the workspace operations needs the Files workspace.
	hostOps map[string][]string
	// has answers whether a user holds a host feature. Nil offers every tool.
	has func(userID, feature string) bool
}

// hostOpFeatures maps the prefix of a host operation to the feature the host
// requires for it (see hostops). An add-on that may call such an operation
// does nothing for a person without the feature, so its tools are not offered.
var hostOpFeatures = map[string]string{"finetune.": auth.FeatureFineTuning, "quantize.": auth.FeatureQuantization}

// addonsWithoutFeature names the add-ons whose host operations need a feature
// the actor does not hold. Local-owner mode has no accounts and hides none.
func addonsWithoutFeature(hostOps map[string][]string, has func(userID, feature string) bool, actor contract.Actor) map[string]bool {
	if has == nil || actor.Mode == auth.AddonModeLocalOwner {
		return nil
	}
	denied := map[string]bool{}
	for addon, prefixes := range hostOps {
		for _, p := range prefixes {
			if f, ok := hostOpFeatures[p]; ok && !has(actor.UserID, f) {
				denied[addon] = true
			}
		}
	}
	return denied
}

// filterByFeature drops the tools of the add-ons whose host operations need a
// feature the actor does not hold, so a model is not offered a tool that can
// only answer "no permission". Local-owner mode has no accounts and keeps all.
func filterByFeature(tools []studio.AddonTool, hostOps map[string][]string, has func(userID, feature string) bool, actor contract.Actor) []studio.AddonTool {
	denied := addonsWithoutFeature(hostOps, has, actor)
	if len(denied) == 0 {
		return tools
	}
	out := make([]studio.AddonTool, 0, len(tools))
	for _, t := range tools {
		if !denied[t.AddonID] {
			out = append(out, t)
		}
	}
	return out
}

// sandboxAddonID is the add-on that runs commands and code.
const sandboxAddonID = "sandbox"

// sandboxRunTool is the tool of the Sandbox add-on that runs commands.
var sandboxRunTool = addontools.ToolName(sandboxAddonID, "bash")

// needsWorkspace says whether the add-on works on the files of the user.
func (s *studioAddons) needsWorkspace(addonID string) bool {
	for _, p := range s.hostOps[addonID] {
		if strings.HasPrefix(p, "workspace.") {
			return true
		}
	}
	return false
}

// Unavailable says why the tools of an add-on cannot work now, or "". An
// add-on that needs the Files workspace cannot work without it, and the
// Sandbox cannot run anything on a host where no backend works.
func (s *studioAddons) Unavailable(ctx context.Context, user, addonID string, workspace bool) string {
	if !workspace && s.needsWorkspace(addonID) {
		return "the Files workspace is off on this server, so there is no place for files or code"
	}
	if addonID != sandboxAddonID {
		return ""
	}
	actor, err := s.actor(user)
	if err != nil {
		return ""
	}
	st, ok := s.sandboxStatus(ctx, actor)
	if !ok || st.CanRun {
		return ""
	}
	return "no sandbox backend works on this host, so commands cannot run"
}

// sandboxState is the part of the Sandbox add-on's status read that the chats use.
type sandboxState struct {
	OK         bool   `json:"ok"`
	CanRun     bool   `json:"canRun"`
	Active     string `json:"active"`
	HostNotice bool   `json:"hostNotice"`
}

// sandboxStatus reads the Sandbox add-on's status as the actor. ok is false
// when the read failed or the add-on did not answer ok.
func (s *studioAddons) sandboxStatus(ctx context.Context, actor contract.Actor) (sandboxState, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := s.adapter.Execute(ctx, actor, addontools.ToolName(sandboxAddonID, "status"), json.RawMessage(`{}`))
	if err != nil || res.Kind != addontools.ResultData {
		return sandboxState{}, false
	}
	var st sandboxState
	if json.Unmarshal(res.Result, &st) != nil || !st.OK {
		return sandboxState{}, false
	}
	return st, true
}

// Isolation says how the Sandbox add-on runs commands: "isolated", "host" (on
// the server, with no isolation) or "" when it cannot tell.
func (s *studioAddons) Isolation(ctx context.Context, user, addonID string) string {
	if addonID != sandboxAddonID {
		return ""
	}
	actor, err := s.actor(user)
	if err != nil {
		return ""
	}
	st, ok := s.sandboxStatus(ctx, actor)
	switch {
	case !ok || !st.CanRun:
		return ""
	case st.HostNotice || st.Active == "host":
		return "host"
	}
	return "isolated"
}

func (s *studioAddons) actor(user string) (contract.Actor, error) {
	switch s.mode {
	case auth.AddonModeLocalOwner:
		return contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}, nil
	case auth.AddonModeNamedUser:
		if user == "" || user == studio.AnonymousPrincipal || user == auth.AddonLocalOwnerID || user == "legacy-api-key" {
			return contract.Actor{}, gateway.ErrForbidden
		}
		return contract.Actor{UserID: user, CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}, nil
	}
	return contract.Actor{}, gateway.ErrForbidden
}

func (s *studioAddons) Tools(ctx context.Context, user string) ([]studio.AddonTool, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	defs, err := s.adapter.Discover(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		// A user without the add-ons feature has no add-on tools. The chat
		// and its tools panel still work.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]studio.AddonTool, 0, len(defs))
	for _, d := range defs {
		raw, err := json.Marshal(d.Parameters)
		if err != nil {
			continue
		}
		out = append(out, studio.AddonTool{Name: d.Name, AddonID: d.AddonID, Description: d.Description, Schema: raw, Action: d.Kind != contract.ToolKindRead,
			Runs: d.Name == sandboxRunTool, Shadows: d.Shadows, InstalledAt: d.InstalledAt})
	}
	return filterByFeature(out, s.hostOps, s.has, actor), nil
}

// AddonSkills lists the add-ons that ship skills and that the user can see,
// less those whose host operations need a feature the user lacks: the same
// rule as their tools.
func (s *studioAddons) AddonSkills(ctx context.Context, user string) ([]studio.AddonSkillSet, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	list, err := s.adapter.Skills(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	denied := addonsWithoutFeature(s.hostOps, s.has, actor)
	out := make([]studio.AddonSkillSet, 0, len(list))
	for _, a := range list {
		if denied[a.AddonID] {
			continue
		}
		out = append(out, studio.AddonSkillSet{AddonID: a.AddonID, Title: a.Title, InstalledAt: a.InstalledAt, Skills: contract.SkillNames(a.Skills),
			Shadows: contract.ShadowedSkills(a.Skills), Running: a.Running})
	}
	return out, nil
}

func (s *studioAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	actor, err := s.actor(user)
	if err != nil {
		return "", err
	}
	res, err := s.adapter.ExecuteIn(ctx, actor, conv, name, args)
	if err != nil {
		return "", err
	}
	return studioAddonText(res), nil
}

// studioAddonText is what the model reads for a result. A change the chat made
// comes as an "applied" document whose note says what was done and whether the
// person can undo it (undoable, changeId and a one-line undo, or a reason). A change that stays a proposal (the owner could not apply
// it) is not applied, and the text says who can apply it.
func studioAddonText(res addontools.ToolResult) string {
	if res.Kind == addontools.ResultProposal && res.Proposal != nil {
		return fmt.Sprintf("The add-on %s proposed a change (%s, proposal %s). Nothing has been changed. "+
			"Tell the user to review and confirm it on the Add-ons page. Do not say the change was made.",
			res.Proposal.AddonID, res.Proposal.Action, res.Proposal.ID)
	}
	return res.JSON()
}
