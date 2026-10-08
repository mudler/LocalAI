package agentworker

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
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
	// Subject and Queue route the runs of agents on NATS.
	Subject, Queue string
	// APIURL and APIToken are how a run calls back to the frontend.
	APIURL, APIToken string
	// Tool and Discovery answer the MCP requests of the frontend.
	Tool      func(context.Context, mcpRemote.MCPToolRequest) mcpRemote.MCPToolResponse
	Discovery func(context.Context, mcpRemote.MCPDiscoveryRequest) mcpRemote.MCPDiscoveryResponse
	// BackendStop drops the sessions cached for a backend that goes away.
	BackendStop func(backend string)
	// Jobs registers the consumers of the queued jobs on the consumer of one
	// carrier, which it is told. It is called for each attachment.
	Jobs func(ctx context.Context, on cluster.Carrier, consumer messaging.WorkConsumer) error
}

// counted counts the runs that are in flight on one attachment.
type counted struct {
	messaging.WorkConsumer
	n *atomic.Int32
}

func (c counted) Consume(ctx context.Context, kind messaging.WorkKind, maxInFlight int, h messaging.WorkHandler) (messaging.Subscription, error) {
	return c.WorkConsumer.Consume(ctx, kind, maxInFlight, func(ctx context.Context, payload []byte, events messaging.Publisher) error {
		c.n.Add(1)
		defer c.n.Add(-1)
		return h(ctx, payload, events)
	})
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
	runs      *atomic.Int32
	failed    *atomic.Bool
	close     func() error
}

func (a *agentAttachment) Connected() bool { return a.connected() }
func (a *agentAttachment) InFlight() int   { return int(a.runs.Load()) }
func (a *agentAttachment) Failed() bool    { return a.failed != nil && a.failed.Load() }
func (a *agentAttachment) Close() error    { return a.close() }

func (c FollowConfig) natsAttacher(ctx context.Context, h worker.Handover) (worker.Attachment, error) {
	actx, cancel := context.WithCancel(ctx)
	failed := &atomic.Bool{}
	client, err := worker.ConnectNATS(actx, c.Local, h, func() { failed.Store(true) })
	if err != nil {
		cancel()
		return nil, err
	}
	runs := &atomic.Int32{}
	var undo []func()
	fail := func(err error) (worker.Attachment, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		client.Close()
		cancel()
		return nil, err
	}

	// The events of a run go back over the bus the run came on.
	bridge := agents.NewEventBridge(client, nil, "agent-worker-"+c.NodeID)
	cancelSub, err := bridge.StartCancelListener()
	if err != nil {
		xlog.Warn("Failed to start cancel listener", "error", err)
	} else {
		undo = append(undo, func() { _ = cancelSub.Unsubscribe() })
	}
	// One consumer serves both queued kinds; the route option only moves the
	// agent-run subject and group, which operators may set.
	consumer := counted{messaging.NewNATSWorkConsumer(client, messaging.WithAgentRunRoute(c.Subject, c.Queue)), runs}
	dispatcher := agents.NewNATSDispatcher(consumer, bridge, nil, c.APIURL, c.APIToken, 0)
	if err := dispatcher.Start(actx); err != nil {
		return fail(fmt.Errorf("starting dispatcher: %w", err))
	}
	undo = append(undo, func() { _ = dispatcher.Stop() })

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
	if c.Jobs != nil {
		if err := c.Jobs(actx, cluster.CarrierNATS, consumer); err != nil {
			return fail(err)
		}
	}
	if c.BackendStop != nil {
		if err := rpc.ServeBackendStop(c.BackendStop); err != nil {
			return fail(err)
		}
	}
	return &agentAttachment{
		connected: client.IsConnected, runs: runs, failed: failed,
		close: func() error {
			cancel()
			client.Close()
			// A subscription waits for the runs that are in flight, and the carrier
			// is already closed, so nothing waits for it.
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
	runs := &atomic.Int32{}
	work := NewWork()
	consumer := counted{work, runs}
	// No bus, so no broadcaster: the events of a run are published on the
	// publisher of its delivery, which writes them to the frontend that drives
	// the run. The cancel of a run is its request: the frontend ends the stream.
	bridge := agents.NewEventBridge(nil, nil, "agent-worker-"+c.NodeID)
	dispatcher := agents.NewNATSDispatcher(consumer, bridge, nil, c.APIURL, c.APIToken, 0)
	// The consumers are registered before the tunnel starts, so that a frontend
	// which connects at once finds a worker that can take its first run.
	if err := dispatcher.Start(ctx); err != nil {
		return nil, fmt.Errorf("starting dispatcher: %w", err)
	}
	if c.Jobs != nil {
		if err := c.Jobs(ctx, cluster.CarrierTunnel, consumer); err != nil {
			_ = dispatcher.Stop()
			return nil, err
		}
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
		Handler:      Handler(cfg, work),
	})
	if err != nil {
		_ = dispatcher.Stop()
		return nil, err
	}
	return &agentAttachment{
		connected: rt.Connected, runs: runs,
		close: func() error {
			err := rt.Close()
			go func() { _ = dispatcher.Stop() }()
			return err
		},
	}, nil
}
