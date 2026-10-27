package application

import (
	"context"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/studio"
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
	m := t.a.addonManager
	if m == nil {
		return nil, nil
	}
	kinds, err := m.ConnectorKinds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]studio.ConnectorKind, len(kinds))
	for i, k := range kinds {
		out[i] = studio.ConnectorKind{AddonID: k.AddonID, ConnectorKind: k.ConnectorKind}
	}
	return out, nil
}

func (t connectorTransport) Call(ctx context.Context, addonID string, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	if t.a.addonManager == nil {
		return contract.ConnectorResponse{}, addonruntime.ErrRuntime
	}
	return t.a.addonManager.Connector(ctx, addonID, req)
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
	var tr studio.ConnectorTransport = connectorTransport{a}
	if connectorTransportForTests != nil {
		tr = connectorTransportForTests
	}
	opts := studio.ConnectorOptions{Store: a.studioConnStore, Transport: tr, Poll: contract.MaxPollSeconds}
	if a.Distributed() != nil && a.authDB != nil {
		lock := advisorylock.NewHeldLock(a.authDB, advisorylock.KeyChatsConnectors)
		opts.Lead = func(ctx context.Context) bool {
			if lock.Held() {
				return lock.Verify(ctx)
			}
			ok, err := lock.TryAcquire(ctx)
			if err != nil {
				xlog.Warn("Connectors: could not try the leader lock", "error", err)
			}
			return ok
		}
	}
	c, err := studio.NewConnectors(svc, opts)
	if err != nil {
		xlog.Warn("Connectors disabled: failed to start", "error", err)
		return
	}
	svc.SetConnectors(c)
	ctx := a.applicationConfig.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	a.studioConnectors, a.studioConnectorsStop = c, cancel
	go c.Run(ctx)
}
