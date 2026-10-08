package carrier

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// TunnelOptions is what NewTunnelSet needs.
//
// The set has seven members. Five of them belong to this carrier and are built
// here: the control verbs, the file stager, the client factory, the dialer of the
// HTTP server of a worker and the agent RPC, which picks an agent worker with the
// selector and sends the request over the same control client as the other verbs.
// The fan-out comes from NewPgbusFanout. The queue is provided by the caller
// (jobs.ClaimQueue), because it lives in the database and reaches no worker.
type TunnelOptions struct {
	// Epoch is the epoch of the cluster row the set is built for.
	Epoch int64

	// Dialer opens streams to workers, through the replica that holds the tunnel
	// of each one.
	Dialer *tunnel.WorkerDialer

	// Fanout carries the broadcasts.
	Fanout *Fanout
	// WorkQueue is the queue of the carrier.
	WorkQueue messaging.WorkQueue
	// AgentSelector picks the agent worker that gets an MCP request.
	AgentSelector *nodes.AgentSelector

	// Registry finds the nodes that hold a model, for the verbs that fan out.
	Registry nodes.ModelLocator
	// InstallTimeout and UpgradeTimeout bound the install and upgrade verbs.
	InstallTimeout, UpgradeTimeout time.Duration
	// Token is the registration token. A worker checks it on its control plane,
	// its file routes and its backend processes.
	Token string

	// S3Staging selects the stager over shared object storage: the frontend puts
	// a file in the store and sends the worker the file verb, over the control
	// plane of the tunnel. Without it the files go over the bulk lane directly.
	// FileManager is the shared store, and it is required with S3Staging.
	S3Staging   bool
	FileManager *storage.FileManager

	// Claims makes the set claim queued work while it is in use or draining, and
	// hand over what is still queued when it is released. It may be nil for a set
	// that is built to be checked and thrown away.
	Claims *ClaimWork
}

// NewTunnelSet builds the set of seam implementations that reach workers through
// their tunnels. The set is complete or it is an error, as for NATS.
func NewTunnelSet(o TunnelOptions) (*Set, error) {
	if o.Dialer == nil {
		return nil, errors.New("tunnel set needs a dialer")
	}
	if o.Fanout == nil {
		return nil, errors.New("tunnel set needs a fan-out")
	}
	if o.WorkQueue == nil {
		return nil, errors.New("tunnel set needs a work queue")
	}
	if o.AgentSelector == nil {
		return nil, errors.New("tunnel set needs an agent selector")
	}

	// The control verbs, the logs and the health of a worker go over the
	// inference lane. The file transfer goes over the bulk lane and never falls
	// back to the other one.
	httpDial := httpDialerFor(o.Dialer)
	bulkDial := httpDialerFor(o.Dialer, tunnel.WithBulkLane())

	control := nodes.NewControlClient(httpDial, o.Token)
	var (
		files      FileCarrier
		forgetFile func(string)
		closeFiles func()
	)
	if o.S3Staging {
		if o.FileManager == nil {
			return nil, errors.New("tunnel set needs a file manager for S3 staging")
		}
		// The bytes go through the object store, and only the verbs go through
		// the tunnel, so the lane of the files is not needed.
		files = nodes.NewS3TunnelFileStager(o.FileManager, control)
	} else {
		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			// The host is never dialled: the dialer opens a stream on the tunnel of
			// the node. It names the node in a log line and in the URL.
			return nodes.WorkerHTTPHost(nodeID, ""), nil
		}, o.Token, bulkDial)
		files, forgetFile, closeFiles = stager, stager.ForgetNode, stager.Close
	}

	set := &Set{
		Name:        cluster.CarrierTunnel,
		Epoch:       o.Epoch,
		Broadcaster: o.Fanout.Broadcaster,
		OnReconnect: o.Fanout.OnReconnect,
		WorkQueue:   o.WorkQueue,
		Commands:    nodes.NewTunnelControl(o.Registry, control, o.InstallTimeout, o.UpgradeTimeout),
		Files:       files,
		Clients:     nodes.NewDialerClientFactory(o.Token, grpcDialerFor(o.Dialer)),
		Dialer:      httpDial,
		Agents:      nodes.NewAgentControlClient(o.AgentSelector, control),
		// The control client and the HTTP stager keep one client for each node.
		ForgetNode: func(nodeID string) {
			control.ForgetNode(nodeID)
			if forgetFile != nil {
				forgetFile(nodeID)
			}
		},
		// The streams that the clients keep idle belong to tunnels that are about to
		// be replaced, so they are given back with the set.
		Close: func() {
			control.Close()
			if closeFiles != nil {
				closeFiles()
			}
			if o.Fanout.Close != nil {
				o.Fanout.Close()
			}
		},
	}
	if o.Claims != nil {
		set.Start = o.Claims.start(func() *Set { return set }, o.AgentSelector, o.Token)
		set.Handoff = o.Claims.handoff()
	}
	if err := set.Validate(); err != nil {
		return nil, err
	}
	return set, nil
}

// httpDialerFor returns the dialer of the HTTP server of a worker.
func httpDialerFor(d *tunnel.WorkerDialer, opts ...tunnel.DialOption) nodes.WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		dial := d.DialerFor(nodeID, tunnel.StreamTagHTTP, opts...)
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dial(ctx, network, addr)
			return conn, mapDialError(err)
		}
	}
}

// grpcDialerFor returns the dialer of the backend processes of a worker.
func grpcDialerFor(d *tunnel.WorkerDialer) nodes.BackendDialerFor {
	return func(nodeID string) func(context.Context, string) (net.Conn, error) {
		dial := d.GRPCDialerFor(nodeID)
		return func(ctx context.Context, addr string) (net.Conn, error) {
			conn, err := dial(ctx, addr)
			return conn, mapDialError(err)
		}
	}
}

// mapDialError is the one place where the errors of the tunnel become the
// conditions that the rest of the frontend acts on. It keeps the chain of the
// cause, so that the specific condition stays matchable below the one that is
// acted on.
//
//   - A route that does not exist is nodes.ErrNoRoute, the one routing sentinel
//     that the scheduler, the health monitor and the upgrade fallback match on.
//     The sentinel of the tunnel package stays below it.
//   - A worker's own refusal that is evidence about a backend keeps its identity
//     and carries nodes.ErrNoRoute never. It is also marked as the answer of a
//     host about a backend (grpc.BackendAnswer), so the code that decides whether
//     a backend is dead can tell it from a failure of the transport.
//   - A peer that did not answer, the budget of the caller and a bulk lane that
//     is down are none of those. They stay plain errors.
func mapDialError(err error) error {
	switch {
	case err == nil:
		return nil
	case tunnel.IsWorkerAnswer(err):
		return &workerAnswerError{err: err}
	case errors.Is(err, tunnel.ErrNoRoute):
		return fmt.Errorf("%w: %w", nodes.ErrNoRoute, err)
	}
	return err
}

// workerAnswerError is a refusal that the worker wrote about a backend. It is a
// grpc.BackendAnswer.
type workerAnswerError struct{ err error }

func (e *workerAnswerError) Error() string { return e.err.Error() }

func (e *workerAnswerError) Unwrap() error { return e.err }

// IsBackendAnswer marks the error as evidence about the backend: the worker
// answered, and the backend it named is not there, like a refused connection on a
// direct dial.
func (e *workerAnswerError) IsBackendAnswer() bool { return true }
