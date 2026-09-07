package nodes

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// WorkerReadiness is the gate behind a worker's /readyz probe.
//
// It exists because the worker's HTTP file-transfer server is started before
// the worker's tunnel is up, and must keep serving after that tunnel drops.
// The probe is therefore installed after the fact rather than passed as a
// value, and must be safe to read from HTTP handler goroutines while the
// startup goroutine is still installing it.
type WorkerReadiness struct {
	probe atomic.Pointer[func() error]
}

// Set installs (or replaces) the readiness probe.
func (r *WorkerReadiness) Set(fn func() error) {
	if r == nil {
		return
	}
	r.probe.Store(&fn)
}

// Check reports whether the worker can accept work. A nil receiver, or one with
// no probe installed, fails open: callers that never wire readiness (the
// frontend's own file-transfer server, tests, embedders) keep the historical
// always-ready behaviour rather than being wedged out of rotation forever.
func (r *WorkerReadiness) Check() error {
	if r == nil {
		return nil
	}
	fn := r.probe.Load()
	if fn == nil || *fn == nil {
		return nil
	}
	return (*fn)()
}

// tunnelConn is the slice of *worker.Tunnel the readiness probe needs. Kept as
// a local interface so this package does not import the worker package (which
// would be an import cycle) and so tests can supply a fake.
type tunnelConn interface {
	Connected() bool
}

// ErrTunnelDisconnected is reported by TunnelReadiness when the worker holds no
// tunnel session.
var ErrTunnelDisconnected = errors.New("worker tunnel is down: the frontend cannot reach this worker")

// TunnelReadiness builds the worker's readiness probe.
//
// A worker's real health is not "a port is open" — that is precisely the
// failure mode of issue #10987, where a process that serves nothing still
// answered 200. All of a worker's actual work (backend install/start/stop,
// model lifecycle, file staging, and every inference stream) arrives over its
// tunnel to the frontend, so a worker with no tunnel session is up and
// unreachable. It binds only loopback and advertises no address, so there is no
// second way in. Registration is already implied by the probe being reachable
// at all: the file-transfer server is only started after the worker has
// successfully registered with the frontend.
//
// This is deliberately something the LOCAL supervisor cannot already see. The
// node registry's status/last_heartbeat is fed by an HTTP heartbeat to the
// frontend, a completely different network path, so a worker can keep
// heartbeating happily while its tunnel is dead and look healthy in the
// registry. The local probe closes that gap.
//
// It is a readiness answer and nothing more. The frontend decides whether a
// worker is GONE from the tunnel session it holds, aged against
// LOCALAI_WORKER_RECONNECT_GRACE; a 503 here is one container's own report that
// it cannot serve right now.
func TunnelReadiness(conn tunnelConn) func() error {
	return func() error {
		if conn == nil || !conn.Connected() {
			return ErrTunnelDisconnected
		}
		return nil
	}
}

// ErrBackendUnreachable is reported when a worker holds a backend process it
// can no longer reach.
var ErrBackendUnreachable = errors.New("backend process is unreachable")

// BackendAddressLister reports the gRPC addresses of the backend processes a
// worker currently believes it is running.
type BackendAddressLister interface {
	LoadedBackendAddresses() []string
}

// CompositeReadiness reports the first failure among its probes, so /readyz
// names the specific reason rather than a generic not-ready.
func CompositeReadiness(probes ...func() error) func() error {
	return func() error {
		for _, p := range probes {
			if p == nil {
				continue
			}
			if err := p(); err != nil {
				return err
			}
		}
		return nil
	}
}

// BackendDataPathReadiness closes the gap NATSReadiness leaves open: a worker
// whose NATS link is fine but whose backend processes have died is up and
// useless, and the scheduler cannot see the difference. It kept routing loads
// to a node that answered 200 while its backend port refused connections.
//
// A worker holding no backends is ready. That is the normal idle state, not a
// fault, and failing it would take every idle worker out of rotation.
func BackendDataPathReadiness(lister BackendAddressLister, dial func(string) error) func() error {
	return func() error {
		if lister == nil || dial == nil {
			return nil
		}
		for _, addr := range lister.LoadedBackendAddresses() {
			if addr == "" {
				continue
			}
			if err := dial(addr); err != nil {
				return fmt.Errorf("%w: %s: %v", ErrBackendUnreachable, addr, err)
			}
		}
		return nil
	}
}
