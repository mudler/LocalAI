package agentworker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/worker"
)

// Options is what Start needs to put an agent worker on the tunnel.
type Options struct {
	// FrontendURL is the URL the worker registered with.
	FrontendURL string
	// NodeID is the identity that registration gave to the worker.
	NodeID string
	// TunnelToken returns the own tunnel credential of the node. A function,
	// because the credential changes at every registration and the tunnel reads
	// it at every dial.
	TunnelToken func() string
	// Reauthorize registers the node again after the frontend refused the
	// credential. It may be nil.
	Reauthorize func(ctx context.Context) error
	// ControlToken guards the control server. It is the registration token of
	// the deployment, which the control client of the frontend presents. An empty
	// token leaves the server open to anything that can reach it, which is the
	// tunnel and this host.
	ControlToken string
	// Handler serves the verbs. See Handler.
	Handler http.Handler
}

// Runtime is a running control plane: one HTTP server on loopback and one tunnel
// to the frontend.
type Runtime struct {
	server *http.Server
	tunnel *worker.Tunnel
	addr   string

	closeOnce sync.Once
	closeErr  error
}

// Addr is the loopback address of the control server. It is never registered
// anywhere: the only way in is the tunnel.
func (r *Runtime) Addr() string {
	if r == nil {
		return ""
	}
	return r.addr
}

// Connected reports whether the tunnel holds a session. It is about reach and
// nothing else, and nothing may read it as the worker being gone.
func (r *Runtime) Connected() bool { return r != nil && r.tunnel.Connected() }

// Close stops the tunnel and the server. It is safe on nil and may be called
// more than once.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.closeErr = r.tunnel.Close()
		shutdown(r.server)
	})
	return r.closeErr
}

// loopbackBind is where the server binds. A constant, so that "an agent worker
// opens no inbound port" is a fact about the code and not about its
// configuration: no flag moves it.
const loopbackBind = "127.0.0.1:0"

// Start binds the control server on loopback, puts the bearer check in front of
// the handler, and holds a tunnel to the frontend that carries the requests.
//
// A tunnel that is refused to start is an error: the frontend URL or the
// identity of the node is unusable. A frontend that is down, or an admin who
// has not approved the node, is not: the tunnel retries with a backoff.
func Start(ctx context.Context, o Options) (*Runtime, error) {
	if o.Handler == nil {
		return nil, errors.New("starting the agent worker control plane: no handler")
	}
	if o.TunnelToken == nil {
		return nil, errors.New("starting the agent worker control plane: no way to read the tunnel credential")
	}
	lis, err := net.Listen("tcp", loopbackBind)
	if err != nil {
		return nil, fmt.Errorf("binding the agent worker control server: %w", err)
	}
	addr := lis.Addr().String()
	server := &http.Server{
		Handler:           nodes.RequireBearer(o.ControlToken, o.Handler),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			xlog.Error("The agent worker control server stopped", "error", err)
		}
	}()

	tun, err := worker.StartTunnel(ctx, worker.TunnelConfig{
		FrontendURL: o.FrontendURL,
		NodeID:      o.NodeID,
		Token:       o.TunnelToken,
		Reauthorize: o.Reauthorize,
		// An agent worker runs no backend process, so only the http tag is offered.
		Services: worker.HTTPOnlyServices(addr),
	})
	if err != nil {
		shutdown(server)
		return nil, fmt.Errorf("starting the agent worker tunnel: %w", err)
	}
	xlog.Info("Agent worker control plane serving over its tunnel", "node", o.NodeID)
	return &Runtime{server: server, tunnel: tun, addr: addr}, nil
}

func shutdown(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		xlog.Debug("The agent worker control server did not stop cleanly", "error", err)
	}
}
