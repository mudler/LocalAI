package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// connectorTransportForTests replaces the transport of applications built after
// UseConnectorTransportForTests.
var connectorTransportForTests studio.ConnectorTransport

// connectorTransportWired is true once an application gave its transfer table
// to the test transport. A later application of the same process, such as an
// isolated one a spec starts, must not take the transport over.
var connectorTransportWired bool

// UseConnectorTransportForTests makes the connectors of applications built after
// the call talk through t instead of the add-on manager, and returns a function
// that restores the real one. It exists so an end-to-end test can serve a
// connector without an installed add-on.
func UseConnectorTransportForTests(t studio.ConnectorTransport) (restore func()) {
	prev := connectorTransportForTests
	connectorTransportForTests, connectorTransportWired = t, false
	return func() { connectorTransportForTests, connectorTransportWired = prev, false }
}

// connectorOptionsForTests changes the options of the connector manager of
// applications built after UseConnectorOptionsForTests.
var connectorOptionsForTests func(*studio.ConnectorOptions)

// UseConnectorOptionsForTests lets f change the options of the connector
// manager of applications built after the call, and returns a function that
// restores the real ones. It exists so a conformance test can give the OAuth
// logins a token client that reaches a fake service on loopback, which the
// guarded client of production refuses, and a clock it moves.
func UseConnectorOptionsForTests(f func(*studio.ConnectorOptions)) (restore func()) {
	prev := connectorOptionsForTests
	connectorOptionsForTests = f
	return func() { connectorOptionsForTests = prev }
}

// connectorTransport reaches the connector add-ons through the add-on manager.
// The manager looks them up on every call, so an add-on that is installed or
// removed later is seen at once.
type connectorTransport struct{ a *Application }

func (t connectorTransport) Kinds(ctx context.Context) ([]studio.ConnectorKind, error) {
	g := t.a.addonGateway
	if g == nil {
		if cfg := t.a.ApplicationConfig(); cfg != nil && !cfg.Addons.Enabled {
			return []studio.ConnectorKind{}, nil
		}
		return nil, gateway.ErrUnavailable
	}
	return g.ConnectorKinds(ctx)
}

func (t connectorTransport) Call(ctx context.Context, scope studio.ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	g := t.a.addonGateway
	if g == nil {
		return contract.ConnectorResponse{}, gateway.ErrUnavailable
	}
	resp, err := g.ConnectorCall(ctx, scope, req)
	if errors.Is(err, gateway.ErrForbidden) {
		return resp, connectors.ErrForbidden
	}
	return resp, err
}

// RegisterAddonConnector registers the host operation with which a connector
// add-on pushes a message. It registers nothing when this process has no host
// operation channel or no connector manager. The message goes through the same
// function as a message the manager pulls itself. A dedicated add-on owner
// refuses it instead.
func (a *Application) RegisterAddonConnector() error {
	if a.addonHostOps == nil {
		return nil
	}
	// The two file operations serve every connector add-on that declares
	// files.transfer; the handles come with the connector calls. They are
	// registered wherever add-ons run, also on a dedicated owner.
	if err := hostops.RegisterConnectorTransfer(a.addonHostOps.Broker, a.transfers()); err != nil {
		return err
	}
	if a.studioConnectors == nil {
		return nil
	}
	if a.ownsAddonsOnly() {
		// A pushed message would start its chat turn here, and the owner has no
		// models. Nothing forwards it to a frontend, so refuse it and let the
		// add-on see the failure.
		xlog.Warn("Connectors: this add-on owner refuses connector.inbound; push connector add-ons need a combined or distributed process")
		return a.addonHostOps.Broker.Register(hostops.Spec{Op: contract.ConnectorInboundOp, Scope: hostops.ScopeInbound, Handler: refuseConnectorInbound})
	}
	return hostops.RegisterConnector(a.addonHostOps.Broker, a.studioConnectors)
}

// refuseConnectorInbound is connector.inbound on a dedicated add-on owner.
func refuseConnectorInbound(context.Context, hostops.Call) (json.RawMessage, error) {
	return nil, hostops.ErrForbidden
}

// addonOAuthDescriptor is the OAuth descriptor that the running add-on's
// verified manifest declares. Only a process that runs the add-ons reads
// manifests; elsewhere the OAuth login and refresh are unavailable, and the
// tool calls that need them are forwarded to the owner anyway.
func (a *Application) addonOAuthDescriptor(ctx context.Context, addonID string) (contract.OAuthDescriptor, error) {
	m := a.addonManager
	if m == nil {
		return contract.OAuthDescriptor{}, gateway.ErrUnavailable
	}
	inst, err := m.Runtime(ctx, addonID)
	if err != nil {
		return contract.OAuthDescriptor{}, err
	}
	caps, err := m.DeclaredCapabilities(ctx, addonID, inst.Generation)
	if err != nil {
		return contract.OAuthDescriptor{}, err
	}
	for _, slot := range caps.Credentials {
		if slot.Kind == contract.CredentialKindOAuth2 && slot.OAuth != nil {
			return *slot.OAuth, nil
		}
	}
	return contract.OAuthDescriptor{}, errors.New("the add-on declares no OAuth login")
}

// StudioConnectors returns the connector manager of the chats, or nil.
func (a *Application) StudioConnectors() *studio.Connectors { return a.studioConnectors }

// startStudioConnectors builds the connector manager and starts its loop. In a
// cluster the polling runs on the one frontend that holds the advisory lock. A
// dedicated add-on owner builds the manager but never starts the loop.
func (a *Application) startStudioConnectors(svc *studio.Service) {
	if a.studioConnStore == nil {
		return
	}
	if a.applicationConfig.DisableConnectors {
		xlog.Info("Connectors are turned off (LOCALAI_DISABLE_CONNECTORS)")
		return
	}
	var tr studio.ConnectorTransport = connectorTransport{a}
	if connectorTransportForTests != nil {
		tr = connectorTransportForTests
		// A test transport that can move files by handle gets the table of
		// the first application that uses it, so its add-ons declare the
		// files ability. Taking it again would point the transport at a table
		// with no blob store, and the files of the first application would
		// stop moving.
		if x, ok := tr.(interface {
			EnableTransfers(*hostops.Transfers) func()
		}); ok && !connectorTransportWired {
			connectorTransportWired = true
			// The servers it starts live as long as the test process.
			_ = x.EnableTransfers(a.transfers())
		}
	}
	// A token is checked with the platform when it is saved, so a typo is
	// refused at once instead of switching the binding off later.
	opts := studio.ConnectorOptions{Store: a.studioConnStore, Transport: tr, Poll: contract.MaxPollSeconds, VerifyToken: studio.VerifyTokenWithMe(tr), FileCeiling: a.studioConnectorFilesCeiling,
		OAuthDescriptor: a.addonOAuthDescriptor, Shares: connectionShares{a}}
	if a.Distributed() != nil && a.authDB != nil {
		opts.Lead = a.studioLeader(advisorylock.KeyChatsConnectors, "Connectors")
	}
	if connectorOptionsForTests != nil {
		connectorOptionsForTests(&opts)
	}
	c, err := studio.NewConnectors(svc, opts)
	if err != nil {
		xlog.Warn("Connectors disabled: failed to start", "error", err)
		return
	}
	svc.SetConnectors(c)
	// Connector chats may use a capability only as the permission model allows.
	svc.SetCapabilityGate(c.CapabilityGate())
	if a.applicationConfig.DisableConnectorTalkback {
		xlog.Info("Connector talk-back is turned off (LOCALAI_DISABLE_CONNECTOR_TALKBACK)")
		c.AddCapabilitySwitch(studio.TalkbackSwitch("turned off on this server (LOCALAI_DISABLE_CONNECTOR_TALKBACK)"))
	}
	if a.applicationConfig.DisableTriggers {
		c.AddCapabilitySwitch(studio.TriggersSwitch("triggers are turned off on this server (LOCALAI_DISABLE_TRIGGERS)"))
	}
	a.studioConnectorsHook.Store(c)
	if a.applicationConfig.SkipBindingCapsMigration {
		xlog.Info("Connectors: the grant of the usual permissions to old bindings is skipped (LOCALAI_SKIP_BINDING_CAPS_MIGRATION)")
	} else {
		go a.migrateBindingCaps(a.backgroundContext(), tr)
	}
	if a.ownsAddonsOnly() {
		// The manager stays: frontends get the stored bot credentials from it
		// and add-ons push messages to it. Only the polling is left to the
		// frontends, which have the models.
		a.studioConnectors = c
		return
	}
	ctx := a.backgroundContext()
	ctx, cancel := context.WithCancel(ctx)
	a.studioConnectors, a.studioConnectorsStop = c, cancel
	go c.Run(ctx)
}

// transfers is the table of live connector file handles of this process. The
// gateway checks and ends them; the broker serves the bytes. Its blob store is
// the one of the workspace, set when the files start.
func (a *Application) transfers() *hostops.Transfers {
	a.addonTransfersOnce.Do(func() {
		t := hostops.NewTransfers(nil)
		t.Audit = func(o hostops.TransferOutcome) {
			xlog.Info("Connector file transfer", "direction", o.Direction, "binding", o.BindingID, "bytes", o.Bytes, "result", o.Result, "duration", o.Duration.Round(time.Millisecond))
		}
		a.addonTransfers = t
	})
	return a.addonTransfers
}

// bindingCapsRetry bounds how long a start waits for the connector kinds, which
// come from add-ons that may still be starting.
const (
	bindingCapsRetryEvery = 5 * time.Second
	bindingCapsRetryFor   = 10 * time.Minute
)

// migrateBindingCaps adds loops.wait and gives the usual permissions, once, to the connector
// bindings saved before permissions were enforced. The store decides who runs
// it, so any number of processes on one store may call this at start. It needs
// the kinds that add-ons serve, to leave out a binding whose kind declares no
// permissions, so it waits for them. With none (add-ons off, or none installed)
// it does nothing and tries again at the next start.
func (a *Application) migrateBindingCaps(ctx context.Context, tr studio.ConnectorTransport) {
	// loops.wait needs no kind, so it goes first and does not wait for add-ons.
	if _, err := studio.MigrateLoopsWait(ctx, a.studioConnStore, time.Now(), nil); err != nil {
		xlog.Warn("Connectors: could not add loops.wait to old bindings; it is tried again at the next start", "error", err)
	}
	deadline := time.Now().Add(bindingCapsRetryFor)
	for {
		kinds, err := tr.Kinds(ctx)
		if err == nil && len(kinds) > 0 {
			rep, err := studio.MigrateBindingCaps(ctx, a.studioConnStore, kinds, time.Now(), nil)
			if err == nil {
				if !rep.Ran {
					xlog.Debug("Connectors: the permissions migration already ran")
				}
				return
			}
			xlog.Warn("Connectors: could not give the usual permissions to old bindings; it is tried again at the next start", "error", err)
			return
		}
		// No kinds yet, or the add-on gateway is not up: wait, as an add-on that
		// serves a kind may be about to start.
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(bindingCapsRetryEvery):
		}
	}
}
