package agentworker

import (
	"context"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// FollowConfig is what an agent worker needs to serve its work on either carrier.
// The handlers are the same on both: a worker follows a change of carrier by
// attaching them to the other one.
type FollowConfig struct {
	NodeID string
	// FrontendURL and ControlToken are for the tunnel. See Options.
	FrontendURL  string
	ControlToken string
	// Local is what the operator set for NATS.
	Local worker.NATSLocal
	// Tool and Discovery answer the MCP requests of the frontend.
	Tool      func(context.Context, mcpRemote.MCPToolRequest) mcpRemote.MCPToolResponse
	Discovery func(context.Context, mcpRemote.MCPDiscoveryRequest) mcpRemote.MCPDiscoveryResponse
	// BackendStop drops the sessions cached for a backend that goes away.
	BackendStop func(backend string)
}

// Attachers returns the attachers of an agent worker for each carrier. They are
// what the follower of the worker calls at start and when the cluster changes.
func (c FollowConfig) Attachers() map[cluster.Carrier]worker.Attacher {
	return map[cluster.Carrier]worker.Attacher{
		cluster.CarrierNATS:   c.natsAttacher,
		cluster.CarrierTunnel: c.tunnelAttacher,
	}
}

// Cannot says why an agent worker cannot attach to a carrier. It opens no port,
// so the only thing it can lack for NATS is a certificate that cannot be handed
// over.
func (c FollowConfig) Cannot() map[cluster.Carrier]func(worker.CarrierView) string {
	return map[cluster.Carrier]func(worker.CarrierView) string{
		cluster.CarrierNATS: func(v worker.CarrierView) string {
			if v.NATSClientTLS && (c.Local.TLS.Cert == "" || c.Local.TLS.Key == "") {
				return "the NATS server asks for a client certificate, and this worker has none: set LOCALAI_NATS_TLS_CERT and LOCALAI_NATS_TLS_KEY"
			}
			return ""
		},
	}
}

type agentAttachment struct {
	connected func() bool
	failed    *atomic.Bool
	close     func() error
}

func (a *agentAttachment) Connected() bool { return a.connected() }

// InFlight is zero: an MCP request is short, and the frontend sends the next
// one on the carrier that is active, so nothing holds an attachment open.
func (a *agentAttachment) InFlight() int { return 0 }
func (a *agentAttachment) Failed() bool  { return a.failed != nil && a.failed.Load() }
func (a *agentAttachment) Close() error  { return a.close() }

func (c FollowConfig) natsAttacher(ctx context.Context, h worker.Handover) (worker.Attachment, error) {
	actx, cancel := context.WithCancel(ctx)
	failed := &atomic.Bool{}
	client, err := worker.ConnectNATS(actx, c.Local, h, func() { failed.Store(true) })
	if err != nil {
		cancel()
		return nil, err
	}
	var undo []func()
	fail := func(err error) (worker.Attachment, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		client.Close()
		cancel()
		return nil, err
	}

	rpc := nodes.NewNATSAgentRPCServer(client, c.NodeID)
	if c.Tool != nil {
		if err := rpc.ServeMCPTool(c.Tool); err != nil {
			return fail(err)
		}
	}
	if c.Discovery != nil {
		if err := rpc.ServeMCPDiscovery(c.Discovery); err != nil {
			return fail(err)
		}
	}
	if c.BackendStop != nil {
		if err := rpc.ServeBackendStop(c.BackendStop); err != nil {
			return fail(err)
		}
	}
	return &agentAttachment{
		connected: client.IsConnected, failed: failed,
		close: func() error {
			cancel()
			client.Close()
			// The carrier is already closed, so nothing waits for the
			// subscriptions to end.
			go func() {
				for i := len(undo) - 1; i >= 0; i-- {
					undo[i]()
				}
			}()
			return nil
		},
	}, nil
}

func (c FollowConfig) tunnelAttacher(ctx context.Context, h worker.Handover) (worker.Attachment, error) {
	if h.Creds == nil {
		return nil, worker.ErrNoCredentials
	}
	cfg := Config{}
	if c.Tool != nil {
		cfg.MCPTool = Unary(c.Tool)
	}
	if c.Discovery != nil {
		cfg.MCPDiscovery = Unary(c.Discovery)
	}
	if c.BackendStop != nil {
		stop := c.BackendStop
		cfg.BackendStop = func(_ context.Context, req workerctl.BackendStopRequest) error {
			if req.Backend != "" {
				stop(req.Backend)
			}
			return nil
		}
	}
	rt, err := Start(ctx, Options{
		FrontendURL: c.FrontendURL,
		NodeID:      c.NodeID,
		// The credential is new after every registration, so it is read at every
		// dial and not once.
		TunnelToken:  h.Creds.TunnelToken,
		Reauthorize:  h.Creds.Reregister,
		ControlToken: c.ControlToken,
		Handler:      Handler(cfg),
	})
	if err != nil {
		return nil, err
	}
	return &agentAttachment{connected: rt.Connected, close: rt.Close}, nil
}
