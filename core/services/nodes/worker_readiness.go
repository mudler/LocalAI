package nodes

import (
	"errors"
	"sync/atomic"
)

// WorkerReadiness is the gate behind a worker's /readyz probe.
//
// It exists because the worker's HTTP file-transfer server is started before
// the worker has connected to NATS, and must keep serving after NATS drops.
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

// natsConn is the slice of *messaging.Client the readiness probe needs. Kept
// as a local interface so this package does not import messaging (which would
// be an import cycle) and so tests can supply a fake.
type natsConn interface {
	IsConnected() bool
}

// ErrNATSDisconnected is reported by NATSReadiness when the worker has lost its
// NATS connection.
var ErrNATSDisconnected = errors.New("NATS connection is down: worker cannot receive work")

// NATSReadiness builds the worker's readiness probe.
//
// A worker's real health is not "a port is open" — that is precisely the
// failure mode of issue #10987, where a process that serves nothing still
// answered 200. All of a worker's actual work (backend install/start/stop
// events, inference dispatch, file-staging notifications) arrives over NATS, so
// a worker with a dead NATS link is up and useless. Registration is already
// implied by the probe being reachable at all: the file-transfer server is only
// started after the worker has successfully registered with the frontend.
//
// This is deliberately something the controller cannot already see. The node
// registry's status/last_heartbeat is fed by an HTTP heartbeat to the frontend,
// a completely different network path — a worker can keep heartbeating happily
// while its NATS connection is dead, and look healthy in the registry. The
// local probe closes that gap.
func NATSReadiness(conn natsConn) func() error {
	return func() error {
		if conn == nil || !conn.IsConnected() {
			return ErrNATSDisconnected
		}
		return nil
	}
}
