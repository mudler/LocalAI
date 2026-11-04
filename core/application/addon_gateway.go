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
	"slices"
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
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
		authority := a.addonAuthority()
		p = p.WithHidden(func(actor contract.Actor) map[string]bool {
			return addonsWithoutFeature(authority, a.installedAddonIDs(), has, actor)
		})
	}
	return p
}

// AddonHostOps is the host operation channel of the add-on supervisor, or nil
// when this process offers none.
func (a *Application) AddonHostOps() *addonruntime.HostOps { return a.addonHostOps }

// addonAuthority is what every reader of add-on host operation grants asks:
// the broker on each call, the manager at admission, and the tool filters. It
// is the Authority set with SetAddonAuthority, and until one is set, the
// startup policy of the deployment, which never changes.
func (a *Application) addonAuthority() hostops.Authority {
	a.addonAuthorityOnce.Do(func() {
		if a.hostAuthority == nil {
			a.hostAuthority = a.buildAddonAuthority()
		}
	})
	return a.hostAuthority
}

// SetAddonGrantStore sets the store of UI grants that the add-on manager
// writes at install and upgrade. Call it once, before the add-on manager starts.
func (a *Application) SetAddonGrantStore(s addons.GrantStore) { a.addonGrantStore = s }

// AddonOperationCompleted reports whether a lifecycle operation completed in
// the owner's journal. An Authority that counts pending grants only after their
// operation completed calls this late-bound, since it is built before the
// manager. It never blocks on the manager lock.
func (a *Application) AddonOperationCompleted(operationID string) bool {
	return a.addonManager != nil && a.addonManager.OperationCompleted(operationID)
}

// SetAddonAuthority sets the Authority of the add-on host operations. It must
// be called once, before the add-on manager starts; a later call is ignored,
// so a grant never changes the rule by which it is checked.
func (a *Application) SetAddonAuthority(auth hostops.Authority) {
	a.addonAuthorityOnce.Do(func() { a.hostAuthority = auth })
}

// installedAddonIDs lists the add-ons the owner knows, or none in a process
// without the add-on manager.
func (a *Application) installedAddonIDs() []string {
	if a.addonManager == nil {
		return nil
	}
	ids, err := a.addonManager.InstalledIDs(context.Background())
	if err != nil {
		return nil
	}
	return ids
}

// newAddonHostOps builds the host operation broker for the supervisor of the
// owner. A frontend without the add-on manager builds nothing, so the channel
// fails closed. The broker exists whenever the manager does, even when no
// grant exists yet: a grant made later applies to the next call, with no
// restart. What an add-on may call is the Authority; no manifest or request can
// add to it.
func (a *Application) newAddonHostOps(m *addons.Manager, limits contract.Limits) *addonruntime.HostOps {
	if m == nil {
		return nil
	}
	in := hostops.NewInFlight()
	broker := hostops.New(in, addonFeatureCheck(a.authDB), limits)
	broker.SetAdminCheck(addonAdminCheck(a.authDB))
	return &addonruntime.HostOps{Broker: broker, InFlight: in, Authority: a.addonAuthority()}
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
		Principal: func(actor contract.Actor) string { return addonWorkspacePrincipal(a.authDB != nil)(actor) },
		DataDir:   func(id string) string { return filepath.Join(root, id) },
		// The chats service is built after the broker: it is looked up at
		// each call. A chat's call gets the chat's access, so a chat whose
		// agent has a folder reaches only that folder through an add-on;
		// without the chats service a call is refused.
		Access: func(ctx context.Context, principal, chat string) (session.Access, error) {
			if svc := a.StudioService(); svc != nil {
				return svc.AddonAccess(ctx, principal, chat)
			}
			return session.Access{}, errors.New("the chats service is not running")
		},
		Chat: func(ctx context.Context, principal, rid string) string {
			if svc := a.StudioService(); svc != nil {
				return svc.ChatOfAction(ctx, principal, rid)
			}
			return ""
		},
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

// confirmPerson says which credentials stand for a person at a host surface:
// confirmCredential without the credential of a chat turn.
func confirmPerson(mode string) func(contract.Actor) bool {
	inner := confirmCredential(mode)
	return func(a contract.Actor) bool {
		return a.CredentialID != auth.AddonChatsCredential && inner(a)
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
	g := &gateway.Gateway{Transfers: a.transfers(), Limits: c.Limits, Manager: a.addonManager, ConnectorAuthority: connectorAuthority{a}, Revalidate: auth.AddonRevalidate(a.authDB, mode)}
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
	g.HostSettings = a.addonHostSettings
	g.Credentials = credentialSource{a}
	g.HostSettingsAdmin = func(ctx context.Context, actor contract.Actor) error {
		if mode == auth.AddonModeLocalOwner {
			return auth.ValidateLocalOwnerActor(actor)
		}
		return auth.ValidateAddonActor(ctx, a.authDB, actor, true)
	}
	if e := a.wireExecutionBinding(g); e != nil {
		return e
	}
	if e := a.wireHostGrantsOwner(g); e != nil {
		return e
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
	// A change under a credential that waits for a person is applied from a
	// host surface only, never by a chat turn.
	brokerOpts.ConfirmPerson = confirmPerson(mode)
	brokerOpts.Credentials = credentialSource{a}
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
	return &studioAddons{adapter: addontools.NewChatToolAdapter(a.addonGateway).WithConnectionNames(a.chatConnectionName), mode: a.AddonOwnershipMode(), hostOps: a.addonAuthority(), ids: a.installedAddonIDs, has: has}
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

// AddonContextFiles forwards to the broker when it knows the context files of
// the add-ons.
func (l lateStudioAddons) AddonContextFiles(ctx context.Context, user string) ([]studio.AddonContextSet, error) {
	if g, ok := l.a.StudioAddonTools().(studio.AddonContextLister); ok {
		return g.AddonContextFiles(ctx, user)
	}
	return nil, nil
}

// AddonUserSettings forwards to the broker when it knows the per-person
// settings the add-ons declare.
func (l lateStudioAddons) AddonUserSettings(ctx context.Context, user string) ([]studio.AddonUserSettingSet, error) {
	if g, ok := l.a.StudioAddonTools().(studio.AddonUserSettingsLister); ok {
		return g.AddonUserSettings(ctx, user)
	}
	return nil, nil
}

// AddonListings forwards to the broker when it knows what the add-ons declare.
func (l lateStudioAddons) AddonListings(ctx context.Context, user string) ([]studio.AddonListing, error) {
	if g, ok := l.a.StudioAddonTools().(studio.AddonLister); ok {
		return g.AddonListings(ctx, user)
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

// Controls forwards to the broker when it knows the host controls of the
// add-ons.
func (l lateStudioAddons) Controls(ctx context.Context, user, addonID string) []string {
	if g, ok := l.a.StudioAddonTools().(studio.AddonControls); ok {
		return g.Controls(ctx, user, addonID)
	}
	return nil
}

// Network forwards to the broker when it knows the network the add-ons declare.
func (l lateStudioAddons) Network(ctx context.Context, user, addonID string) (studio.AddonNetwork, bool) {
	if g, ok := l.a.StudioAddonTools().(studio.AddonNetworks); ok {
		return g.Network(ctx, user, addonID)
	}
	return studio.AddonNetwork{}, false
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
	// hostOps says which host operations an add-on may call. An add-on that may
	// call the workspace operations needs the Files workspace.
	hostOps hostops.Authority
	// ids lists the installed add-ons, for the rules that name add-ons.
	ids func() []string
	// has answers whether a user holds a host feature. Nil offers every tool.
	has func(userID, feature string) bool
}

// hostOpFeatures maps the prefix of a host operation to the feature the host
// requires for it (see hostops). An add-on that may call such an operation
// does nothing for a person without the feature, so its tools are not offered.
var hostOpFeatures = map[string]string{"finetune.": auth.FeatureFineTuning, "quantize.": auth.FeatureQuantization}

// addonsWithoutFeature names the add-ons whose host operations need a feature
// the actor does not hold. Local-owner mode has no accounts and hides none.
func addonsWithoutFeature(authority hostops.Authority, ids []string, has func(userID, feature string) bool, actor contract.Actor) map[string]bool {
	if has == nil || actor.Mode == auth.AddonModeLocalOwner || authority == nil {
		return nil
	}
	denied := map[string]bool{}
	for _, addon := range ids {
		for _, p := range heldOperations(authority, addon) {
			for family, f := range hostOpFeatures {
				if strings.HasPrefix(p, family) && !has(actor.UserID, f) {
					denied[addon] = true
				}
			}
		}
	}
	return denied
}

// heldOperations lists the operation prefixes and names an add-on holds now.
func heldOperations(authority hostops.Authority, addonID string) []string {
	if authority == nil {
		return nil
	}
	set := authority.Snapshot(addonID)
	return append(slices.Clone(set.Prefixes), set.Operations...)
}

// filterByFeature drops the tools of the add-ons whose host operations need a
// feature the actor does not hold, so a model is not offered a tool that can
// only answer "no permission". Local-owner mode has no accounts and keeps all.
func filterByFeature(tools []studio.AddonTool, hostOps hostops.Authority, has func(userID, feature string) bool, actor contract.Actor) []studio.AddonTool {
	ids := make([]string, 0, len(tools))
	for _, t := range tools {
		ids = append(ids, t.AddonID)
	}
	denied := addonsWithoutFeature(hostOps, ids, has, actor)
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

// installed lists the installed add-ons, or none when no list is wired.
func (s *studioAddons) installed() []string {
	if s.ids == nil {
		return nil
	}
	return s.ids()
}

// needsWorkspace says whether the add-on works on the files of the user.
func (s *studioAddons) needsWorkspace(addonID string) bool {
	return hasWorkspaceOperation(heldOperations(s.hostOps, addonID))
}

func hasWorkspaceOperation(operations []string) bool {
	for _, p := range operations {
		if strings.HasPrefix(p, "workspace.") {
			return true
		}
	}
	return false
}

// Unavailable consumes only the owner-selected request-local observation.
func (s *studioAddons) Unavailable(ctx context.Context, user, addonID string, workspace bool) string {
	if !workspace && s.needsWorkspace(addonID) {
		return "the Files workspace is off on this server, so there is no place for files or code"
	}
	value, explanation := studio.ExecutionObservation(ctx, user, addonID)
	if value.Readiness == "unavailable" {
		if explanation != "" {
			return explanation
		}
		return "the selected execution provider is unavailable"
	}
	return ""
}

// Isolation is unknown outside an admitted scope or after freshness is lost.
func (s *studioAddons) Isolation(ctx context.Context, user, addonID string) string {
	value, _ := studio.ExecutionObservation(ctx, user, addonID)
	if value.Readiness != "available" || value.Isolation == "unknown" {
		return ""
	}
	return string(value.Isolation)
}

// Controls returns the host controls the apps list draws for the add-on, as
// the user reads the list, or nil.
func (s *studioAddons) Controls(ctx context.Context, user, addonID string) []string {
	actor, err := s.actor(user)
	if err != nil {
		return nil
	}
	out, err := s.adapter.Controls(ctx, actor, addonID)
	if err != nil {
		return nil
	}
	return out
}

// Network returns the network the add-on declares while the apps list draws
// its network-policy control, as the user reads the list.
func (s *studioAddons) Network(ctx context.Context, user, addonID string) (studio.AddonNetwork, bool) {
	actor, err := s.actor(user)
	if err != nil {
		return studio.AddonNetwork{}, false
	}
	v, ok, err := s.adapter.Network(ctx, actor, addonID)
	if err != nil || !ok {
		return studio.AddonNetwork{}, false
	}
	return studio.AddonNetwork{Title: v.Title, Presets: v.Presets}, true
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
			Shadows: d.Shadows, Execution: d.Execution, InstalledAt: d.InstalledAt, Permission: d.Permission, Credentialed: d.Credentialed})
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
	denied := addonsWithoutFeature(s.hostOps, s.installed(), s.has, actor)
	out := make([]studio.AddonSkillSet, 0, len(list))
	for _, a := range list {
		if denied[a.AddonID] {
			continue
		}
		out = append(out, studio.AddonSkillSet{AddonID: a.AddonID, Title: a.Title, InstalledAt: a.InstalledAt, Skills: contract.SkillNames(a.Skills), Isolated: a.Isolated,
			Shadows: contract.ShadowedSkills(a.Skills), Slots: a.Slots, Running: a.Running})
	}
	return out, nil
}

// AddonContextFiles lists the add-ons that declare context files and that the
// user can see, less those whose host operations need a feature the user
// lacks: the same rule as their tools and skills.
func (s *studioAddons) AddonContextFiles(ctx context.Context, user string) ([]studio.AddonContextSet, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	list, err := s.adapter.ContextFiles(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	denied := addonsWithoutFeature(s.hostOps, s.installed(), s.has, actor)
	out := make([]studio.AddonContextSet, 0, len(list))
	for _, a := range list {
		if denied[a.AddonID] {
			continue
		}
		out = append(out, studio.AddonContextSet{AddonID: a.AddonID, Files: a.Files, Running: a.Running})
	}
	return out, nil
}

// AddonUserSettings lists the per-person settings the add-ons declare, for the
// host rules that read one by its role. The same visibility rule as the context
// files: an add-on the user cannot see brings nothing.
func (s *studioAddons) AddonUserSettings(ctx context.Context, user string) ([]studio.AddonUserSettingSet, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	list, err := s.adapter.UserSettings(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]studio.AddonUserSettingSet, 0, len(list))
	for _, a := range list {
		out = append(out, studio.AddonUserSettingSet{AddonID: a.AddonID, Settings: a.Settings, Running: a.Running})
	}
	return out, nil
}

// AddonListings gives the title and description each add-on declares, for the
// lists of what a chat can switch.
func (s *studioAddons) AddonListings(ctx context.Context, user string) ([]studio.AddonListing, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	list, err := s.adapter.Listings(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]studio.AddonListing, 0, len(list))
	for _, a := range list {
		out = append(out, studio.AddonListing{AddonID: a.AddonID, Title: a.Title, Description: a.Description})
	}
	return out, nil
}

// AddonShadowers lists the add-ons that replace a built-in chat tool and that
// the user can see, in any state, less those whose host operations need a
// feature the user lacks: the same rule as their tools.
func (s *studioAddons) AddonShadowers(ctx context.Context, user string) ([]studio.AddonShadower, error) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, nil
	}
	list, err := s.adapter.Shadowers(ctx, actor)
	if errors.Is(err, gateway.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	denied := addonsWithoutFeature(s.hostOps, s.installed(), s.has, actor)
	out := make([]studio.AddonShadower, 0, len(list))
	for _, a := range list {
		if denied[a.AddonID] {
			continue
		}
		out = append(out, studio.AddonShadower{AddonID: a.AddonID, Title: a.Title, InstalledAt: a.InstalledAt, Tools: a.Tools, Running: a.Running})
	}
	return out, nil
}

func (s *studioAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	actor, err := s.actor(user)
	if err != nil {
		return "", err
	}
	// The call is bound to the chat on the host side, so the add-on's
	// workspace operations act with the chat's access (a chat whose agent has
	// a folder reaches only that folder), for every tool kind.
	res, err := s.adapter.ExecuteChat(ctx, actor, addontools.NewChatCall(user, conv), name, args)
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
		// A change that runs with a credential names the connection it will be
		// made with, so the person, and the connector chat that relays this
		// text, see it before they confirm.
		with := ""
		if res.Proposal.Connection != "" {
			with = " It will be made " + res.Proposal.Connection + "; say which connection in your message."
		}
		return fmt.Sprintf("The add-on %s proposed a change (%s, proposal %s).%s Nothing has been changed. "+
			"Tell the user to review and confirm it on the Add-ons page. Do not say the change was made.",
			res.Proposal.AddonID, res.Proposal.Action, res.Proposal.ID, with)
	}
	return res.JSON()
}

// Resolve at call time: the connector service can start after the private gateway.
type connectorAuthority struct{ a *Application }

func (c connectorAuthority) AuthorizeConnector(ctx context.Context, scope studio.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorRequest, error) {
	if c.a.studioConnectors == nil {
		return req, gateway.ErrUnavailable
	}
	return c.a.studioConnectors.AuthorizeConnector(ctx, scope, req)
}

// credentialSource gives the tool calls of an add-on that declares credentials
// the token and the allowed resource of the caller's own binding. It is the
// owner's: the sealed tokens are on the owner only. The connector service can
// start after the gateway, so it is resolved at call time.
type credentialSource struct{ a *Application }

func (c credentialSource) Credential(ctx context.Context, actor contract.Actor, addonID, key string) (gateway.CredentialGrant, error) {
	if c.a.studioConnectors == nil {
		return gateway.CredentialGrant{}, gateway.ErrUnavailable
	}
	user := addonWorkspacePrincipal(c.a.authDB != nil)(actor)
	if user == "" {
		// A local-owner actor on an install with accounts is no person.
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeNoCredential}
	}
	bindingID, err := c.connectionFor(ctx, user, addonID)
	if err != nil {
		return gateway.CredentialGrant{}, err
	}
	grant, err := c.a.studioConnectors.CredentialFor(ctx, user, addonID, bindingID, key)
	var notAllowed *connectors.NotAllowedError
	switch {
	case err == nil:
		return gateway.CredentialGrant{Credential: grant.Secret, ResourceKey: grant.Resource.Key, ResourceID: grant.Resource.ID, WriteMode: grant.WriteMode,
			BindingID: grant.BindingID, BindingName: grant.BindingName}, nil
	case errors.Is(err, connectors.ErrNoConnection):
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
	case errors.Is(err, connectors.ErrNoCredential):
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeNoCredential}
	case errors.As(err, &notAllowed):
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeResourceNotAllowed, Allowed: notAllowed.Allowed}
	}
	// The cause may name a binding; it stays out of the error that travels.
	xlog.Warn("addons: the credential of a binding could not be read", "addon", addonID)
	return gateway.CredentialGrant{}, gateway.ErrUnavailable
}

// connectionFor names the binding a credentialed call uses. The host never
// picks one for the person:
//
//  1. the binding named in the context, which only host code sets: the chats
//     service after it checked the model's name against the chat's enabled
//     set, or the broker applying a proposal that was previewed with it. In a
//     chat call it must also be in the set that chat enables now, so a bug
//     above cannot widen it;
//  2. the one connection a chat enables, for a chat call that names none;
//  3. for a call that comes from no chat, such as the add-on's own page, the
//     person's only connection.
//
// Anything else is a select_connection refusal.
func (c credentialSource) connectionFor(ctx context.Context, user, addonID string) (string, error) {
	override := gateway.ConnectionOverride(ctx)
	if b, ok := hostops.BindingOf(ctx); ok && c.a.studioStore != nil {
		rec, err := c.a.studioStore.GetChat(ctx, b.Principal, b.ChatID)
		if err != nil || rec.Principal != b.Principal {
			return "", &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
		}
		set, err := c.a.studioConnectors.ConnectionsOf(ctx, user, rec, addonID)
		if err != nil {
			return "", gateway.ErrUnavailable
		}
		switch {
		case override != "":
			if slices.Contains(set.IDs(), override) {
				return override, nil
			}
		case len(set.Items) == 1:
			return set.Items[0].ID, nil
		case len(set.Items) == 0:
			if list, err := c.a.studioConnectors.Connections(ctx, user, addonID); err == nil && len(list) == 0 {
				return "", &gateway.CredentialRefusal{Code: gateway.CodeNoCredential}
			}
		}
		return "", &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
	}
	if override != "" {
		return override, nil
	}
	only, err := c.a.studioConnectors.SoleConnection(ctx, user, addonID)
	if err != nil {
		return "", gateway.ErrUnavailable
	}
	if only.ID == "" {
		// None or several: with no chat to hold a choice there is no one to ask.
		list, err := c.a.studioConnectors.Connections(ctx, user, addonID)
		if err == nil && len(list) == 0 {
			return "", &gateway.CredentialRefusal{Code: gateway.CodeNoCredential}
		}
		return "", &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
	}
	return only.ID, nil
}

// chatConnectionName tells the chat adapter the display name of the binding a
// call of the chat uses, so a tool result can say which connection acted. The
// chat is the one the host bound to the call, never a name the model wrote.
func (a *Application) chatConnectionName(ctx context.Context, conversationID, bindingID string) string {
	b, ok := hostops.BindingOf(ctx)
	if !ok || b.ChatID != conversationID || a.studioConnectors == nil {
		return ""
	}
	return a.studioConnectors.ConnectionName(ctx, b.Principal, bindingID)
}
