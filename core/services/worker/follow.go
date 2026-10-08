package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
)

// tunnelFollower attaches a worker to a tunnel, and detaches it, while the worker
// runs.
//
// A worker that booted on NATS has no tunnel and no control plane on its HTTP
// server. When the cluster moves to the tunnel the worker has to gain both
// without a restart, and this is the part that does it. What decides to attach is
// not here: the worker follows the carrier that the frontend names, and that
// logic is the caller's.
//
// The control plane is a slot on the HTTP server that the worker already runs.
// While the follower is detached the slot is empty and the path answers as a path
// that does not exist, which is what a worker on NATS has always answered. The
// verbs are claimed on the plane once, on the first attach: a plane can claim a
// verb only once, and a second attach must not fail on it.
type tunnelFollower struct {
	cfg         *Config
	nodeID      string
	httpAddr    string
	token       func() string
	reauthorize func(ctx context.Context) error
	slot        *nodes.ControlSlot
	register    func(controlServer) error
	control     *httpControlServer

	mu         sync.Mutex
	tunnel     *Tunnel
	registered bool
}

// tunnelFollowerOptions are the inputs of newTunnelFollower.
type tunnelFollowerOptions struct {
	Config *Config
	NodeID string
	// HTTPAddr is where the HTTP server of this worker listens. The tunnel routes
	// the http tag to it.
	HTTPAddr string
	// Token returns the own tunnel credential of the node, and Reauthorize mints
	// a new one. See TunnelConfig.
	Token       func() string
	Reauthorize func(ctx context.Context) error
	// Slot is the control plane of the HTTP server. It is filled on attach and
	// emptied on detach.
	Slot *nodes.ControlSlot
	// Register claims the verbs of the worker on a control plane.
	Register func(controlServer) error
}

func newTunnelFollower(o tunnelFollowerOptions) *tunnelFollower {
	return &tunnelFollower{
		cfg:         o.Config,
		nodeID:      o.NodeID,
		httpAddr:    o.HTTPAddr,
		token:       o.Token,
		reauthorize: o.Reauthorize,
		slot:        o.Slot,
		register:    o.Register,
		control:     newHTTPControlServer(),
	}
}

// UseCredentials sets where the tunnel reads its credential, and how it gets a
// new one. A credential is new after every registration, so the follower is given
// it again for each attach.
func (f *tunnelFollower) UseCredentials(token func() string, reauthorize func(ctx context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = token
	f.reauthorize = reauthorize
}

// CanAttach reports whether this worker has what a tunnel needs: a frontend URL
// that can be turned into the endpoint of the tunnel. A worker that cannot attach
// must not mount a control plane at all.
func (f *tunnelFollower) CanAttach() bool {
	if f == nil || f.cfg == nil {
		return false
	}
	_, err := tunnelEndpoint(f.cfg.RegisterTo, f.nodeID)
	return err == nil
}

// Attached reports whether the tunnel is started.
func (f *tunnelFollower) Attached() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tunnel != nil
}

// Attach claims the verbs on the control plane if it has not, fills the slot, and
// starts the tunnel. It does nothing when the tunnel is started. The tunnel keeps
// trying to connect in the background, so a frontend that is down is not an
// error here.
func (f *tunnelFollower) Attach(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tunnel != nil {
		return nil
	}
	if !f.CanAttach() {
		return errors.New("attaching to the tunnel: this worker has no frontend URL to dial")
	}
	if !f.registered {
		if err := f.register(f.control); err != nil {
			return fmt.Errorf("serving the worker verbs over HTTP: %w", err)
		}
		f.registered = true
	}
	f.slot.Set(f.control)
	t, err := StartTunnel(ctx, tunnelConfig(f.cfg, f.nodeID, f.httpAddr, f.token, f.reauthorize))
	if err != nil {
		f.slot.Set(nil)
		return fmt.Errorf("starting the worker tunnel: %w", err)
	}
	f.tunnel = t
	xlog.Info("The worker attached to the tunnel", "node", f.nodeID)
	return nil
}

// Detach stops the tunnel and empties the slot. The verbs stay claimed on the
// plane for the next attach. It does nothing when the tunnel is not started.
func (f *tunnelFollower) Detach() error {
	f.mu.Lock()
	t := f.tunnel
	f.tunnel = nil
	f.mu.Unlock()
	if t == nil {
		return nil
	}
	f.slot.Set(nil)
	err := t.Close()
	xlog.Info("The worker detached from the tunnel", "node", f.nodeID)
	return err
}

// tunnelConfig is the configuration of the tunnel of a worker. A worker that
// boots on the tunnel and one that attaches later start it the same way.
func tunnelConfig(cfg *Config, nodeID, httpAddr string, token func() string, reauthorize func(ctx context.Context) error) TunnelConfig {
	return TunnelConfig{
		FrontendURL: cfg.RegisterTo,
		NodeID:      nodeID,
		Token:       token,
		// A frontend that refuses the credential is told who this worker is
		// again, so that a rotated credential does not leave it unreachable.
		Reauthorize: reauthorize,
		// Built by tunnelServices and not inline, so that the routing table,
		// which is the security boundary of the tunnel, can be reached from a
		// spec without starting a worker.
		Services: tunnelServices(cfg, httpAddr),
	}
}
