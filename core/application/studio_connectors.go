package application

import (
	"context"
	"errors"

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

// UseConnectorTransportForTests makes the connectors of applications built after
// the call talk through t instead of the add-on manager, and returns a function
// that restores the real one. It exists so an end-to-end test can serve a
// connector without an installed add-on.
func UseConnectorTransportForTests(t studio.ConnectorTransport) (restore func()) {
	prev := connectorTransportForTests
	connectorTransportForTests = t
	return func() { connectorTransportForTests = prev }
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
// function as a message the manager pulls itself.
func (a *Application) RegisterAddonConnector() error {
	if a.addonHostOps == nil || a.studioConnectors == nil {
		return nil
	}
	return hostops.RegisterConnector(a.addonHostOps.Broker, a.studioConnectors)
}

// StudioConnectors returns the connector manager of the chats, or nil.
func (a *Application) StudioConnectors() *studio.Connectors { return a.studioConnectors }

// startStudioConnectors builds the connector manager and starts its loop. In a
// cluster the polling runs on the one frontend that holds the advisory lock.
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
	}
	// A token is checked with the platform when it is saved, so a typo is
	// refused at once instead of switching the binding off later.
	opts := studio.ConnectorOptions{Store: a.studioConnStore, Transport: tr, Poll: contract.MaxPollSeconds, VerifyToken: studio.VerifyTokenWithMe(tr)}
	if a.Distributed() != nil && a.authDB != nil {
		opts.Lead = a.studioLeader(advisorylock.KeyChatsConnectors, "Connectors")
	}
	c, err := studio.NewConnectors(svc, opts)
	if err != nil {
		xlog.Warn("Connectors disabled: failed to start", "error", err)
		return
	}
	svc.SetConnectors(c)
	ctx := a.backgroundContext()
	ctx, cancel := context.WithCancel(ctx)
	a.studioConnectors, a.studioConnectorsStop = c, cancel
	a.studioConnectorsHook.Store(c)
	go c.Run(ctx)
}
