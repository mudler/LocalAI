package agentworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// How a verb's failure reaches the frontend, written once.
//
// Phase 3 found this same rule written at three sites in one file and pinned at
// one, so every exit below goes through writeFailure and every verb has its own
// spec for it. The rule: a non-2xx means this worker FAILED TO SERVE the
// request, which the frontend maps onto "no route to that worker" and nobody
// may act on; a 2xx body is the WORKER'S OWN ANSWER, which a reap guard may.
// A handler's error is the first, always, and never the second.

// writeFailure is the ONE place a verb's failure becomes a status.
func writeFailure(w http.ResponseWriter, status int, verb string, err error) {
	http.Error(w, "the "+verb+" verb could not be served: "+err.Error(), status)
}

// failToServe answers a verb this worker could not serve. 500 rather than 404,
// which the frontend reads as a verb the worker does not implement, and rather
// than any 2xx, which it reads as an answer.
func failToServe(w http.ResponseWriter, verb string, err error) {
	writeFailure(w, http.StatusInternalServerError, verb, err)
}

// refuseRequest answers a request this worker could not read. 400 because the
// fault is the caller's, and non-2xx for the same reason as above: a body this
// worker could not decode is not an answer about anything.
func refuseRequest(w http.ResponseWriter, verb string, err error) {
	writeFailure(w, http.StatusBadRequest, verb, err)
}

// serveUnary answers one unary verb: the handler's bytes, whole, on a 200.
func serveUnary(verb string, h UnaryHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		reply, err := h(r.Context(), json.RawMessage(body))
		if err != nil {
			failToServe(w, verb, err)
			return
		}
		if len(reply) == 0 {
			// The shape a verb with nothing to say takes, and the same one the
			// backend worker's fire-and-forget verbs take, so one frontend
			// client handles both without knowing which kind of worker answered.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(reply); err != nil {
			xlog.Debug("agent worker control reply could not be written", "verb", verb, "error", err)
		}
	}
}

// serveBackendStop answers backend.stop on the agent worker.
//
// It answers 204 with no body, which is exactly what a BACKEND worker answers
// on this same path. The two implementations share nothing else, and that is
// the point: the frontend issues one RPC and reads one answer without knowing
// which kind of worker it reached.
func serveBackendStop(h func(ctx context.Context, req messaging.BackendStopRequest) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		var req messaging.BackendStopRequest
		if err := json.Unmarshal(body, &req); err != nil {
			refuseRequest(w, workerctl.PathBackendStop, err)
			return
		}
		// The caller's context is dropped, for the reason the backend worker's
		// backend.stop drops it: this is fire-and-forget, so there is no answer
		// the caller is still waiting on, and abandoning the cleanup half way
		// would leave sessions cached for a backend that is gone.
		if err := h(context.WithoutCancel(r.Context()), req); err != nil {
			failToServe(w, workerctl.PathBackendStop, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// serveStream answers one streaming verb: zero or more progress lines followed
// by exactly one reply line, NDJSON, the reply last.
//
// The status is written on the FIRST line rather than at entry, and that is
// what lets a handler that fails before publishing anything answer a non-2xx
// like every other verb here. Once a line is on the wire the status is spent:
// such a handler's failure ends the body with NO reply line, which the frontend
// reads as no answer rather than as a failed one. Both are "this worker did not
// answer", which is the only honest thing to say and the only thing nobody may
// act on.
func serveStream(verb string, h StreamHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		stream := &ndjsonStream{w: w}
		reply, err := h(r.Context(), json.RawMessage(body), stream)
		if err != nil {
			if !stream.committed() {
				failToServe(w, verb, err)
				return
			}
			xlog.Warn("An agent worker control verb failed after it had streamed progress, so its response ends with no reply line",
				"verb", verb, "error", err)
			return
		}
		stream.writeReply(verb, reply)
	}
}

// ndjsonStream writes the Envelope lines of one streaming control response.
//
// The mutex is not optional: a handler may publish progress from a goroutine of
// its own, so without serialization a progress write can interleave with the
// terminal reply write and put a torn line on the wire. done is what enforces
// the other half of the contract: once the reply is written, a late progress
// line is dropped rather than appended after it.
type ndjsonStream struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	header bool
	done   bool
}

// Publish writes one progress line. It is messaging.Publisher, which is what
// lets a handler written against the bus keep its shape.
//
// The subject is DISCARDED, and that is a statement about the carrier rather
// than a shortcut. On the bus a subject was how a progress message found the
// one caller waiting for it; here the response body the caller is already
// reading IS that correlation, so there is nothing left for a subject to
// select. It is kept in the signature because it is the interface the handlers
// implement.
func (n *ndjsonStream) Publish(_ string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding a progress line: %w", err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return nil
	}
	n.write(workerctl.Envelope{Progress: raw})
	return nil
}

// committed reports whether anything has been written, which is whether the
// status is still this handler's to choose.
func (n *ndjsonStream) committed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.header
}

// writeReply writes the single terminal reply line and closes the stream to any
// further progress.
func (n *ndjsonStream) writeReply(verb string, reply json.RawMessage) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	if len(reply) == 0 {
		// A handler that answers nothing still owes the caller a terminal line,
		// or the caller reads to EOF without one and cannot tell that from a
		// truncated response.
		reply = json.RawMessage(`null`)
	}
	n.write(workerctl.Envelope{Reply: reply})
	n.done = true
	xlog.Debug("agent worker control stream finished", "verb", verb)
}

// write encodes one envelope and flushes it. Callers hold n.mu.
func (n *ndjsonStream) write(env workerctl.Envelope) {
	if !n.header {
		n.w.Header().Set("Content-Type", workerctl.ContentTypeStream)
		// A streaming body must not be buffered into a guessed content type:
		// the caller reads line by line and the first line may be minutes
		// before the last.
		n.w.Header().Set("X-Content-Type-Options", "nosniff")
		n.w.WriteHeader(http.StatusOK)
		n.header = true
	}
	if err := json.NewEncoder(n.w).Encode(env); err != nil {
		xlog.Debug("agent worker control stream line could not be written", "error", err)
		return
	}
	if f, ok := n.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Options is everything Start needs to put an agent worker on the tunnel.
type Options struct {
	// FrontendURL is the value the worker registered against
	// (LOCALAI_REGISTER_TO).
	FrontendURL string
	// NodeID is the identity registration assigned this worker.
	NodeID string
	// TunnelToken supplies the node's own tunnel credential. A function because
	// the credential rotates on every re-registration and is read at DIAL time;
	// see worker.TunnelConfig.Token.
	TunnelToken func() string
	// ControlToken is the bearer token guarding the loopback control server.
	// It is the deployment's registration token, the same one a backend worker
	// puts in front of its control plane and the same one the frontend's
	// control client presents.
	ControlToken string
	// Handlers are the verbs this worker serves.
	Handlers Config
}

// Runtime is a running agent worker control plane: one loopback HTTP server and
// one tunnel to the frontend.
type Runtime struct {
	server *http.Server
	tunnel *worker.Tunnel
	addr   string
}

// Addr is the loopback address the control server bound.
//
// It exists for specs and for a log line. It is deliberately NOT registered
// anywhere: nothing outside this process may learn it, because the only way in
// is meant to be the tunnel.
func (r *Runtime) Addr() string { return r.addr }

// Connected reports whether the tunnel currently holds a live session. It is a
// statement about REACHABILITY and nothing else; nothing may read it as the
// worker being gone.
func (r *Runtime) Connected() bool { return r != nil && r.tunnel.Connected() }

// Close stops the tunnel and the control server. It is safe on a nil receiver
// so a caller can defer it beside a failed Start.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	err := r.tunnel.Close()
	nodes.ShutdownFileTransferServer(r.server)
	return err
}

// Start binds the control server on loopback, mounts the agent's verbs behind
// one bearer check, and holds a tunnel to the frontend that carries them.
//
// One function rather than a run of statements in a CLI command, and that is
// the point rather than tidiness. The pieces are one fact: a control server
// nothing can reach is a worker that looks healthy and answers nothing, and a
// tunnel with no server behind it refuses every stream. Started as separate
// lines in a Run body, each is a line whose loss has no symptom and which no
// spec can reach without starting a whole worker process.
//
// The listener binds 127.0.0.1:0 and the port is never advertised: an agent
// worker needs no inbound port, which is the whole reason it is being moved off
// a bus it had to be able to dial.
//
// A failure to START the tunnel is returned, unlike a failure to CONNECT: the
// first means the frontend URL or this node's identity is unusable, and the
// second is a frontend that is down or an admin who has not approved this node
// yet, which the tunnel retries through with backoff.
func Start(ctx context.Context, opts Options) (*Runtime, error) {
	lis, err := net.Listen("tcp", loopbackBind)
	if err != nil {
		return nil, fmt.Errorf("binding the agent worker control server: %w", err)
	}
	addr := lis.Addr().String()

	// Created here and armed only once the tunnel exists, below. Until then
	// /readyz reports ready, which is correct: reaching this line means the
	// worker has already registered with the frontend, so it is mid-startup
	// rather than broken.
	readiness := &nodes.WorkerReadiness{}
	server, err := nodes.StartControlOnlyServer(lis, opts.ControlToken, readiness, &nodes.AuthenticatedRoutes{
		Prefix:   workerctl.Prefix,
		Register: opts.Handlers.Register,
	})
	if err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("starting the agent worker control server: %w", err)
	}

	tunnel, err := worker.StartTunnel(ctx, worker.TunnelConfig{
		FrontendURL: opts.FrontendURL,
		NodeID:      opts.NodeID,
		Token:       opts.TunnelToken,
		// Built by worker.HTTPOnlyServices rather than inline, so the routing
		// table, which is this feature's security boundary, is reachable from a
		// spec without starting a worker. An agent worker runs no backend
		// processes, so the grpc tag is not offered at all.
		Services: worker.HTTPOnlyServices(addr),
	})
	if err != nil {
		nodes.ShutdownFileTransferServer(server)
		return nil, fmt.Errorf("starting the agent worker tunnel: %w", err)
	}
	// Armed here rather than at the call site. /readyz means "the frontend can
	// reach me", and a live tunnel session is the only thing that makes that
	// true for a process that binds loopback and advertises nothing. As a
	// separate statement elsewhere its loss has no symptom: the gate fails
	// open, so the worker answers 200 forever with no session.
	readiness.Set(nodes.TunnelReadiness(tunnel))

	xlog.Info("Agent worker control plane serving over its tunnel", "node", opts.NodeID, "addr", addr)
	return &Runtime{server: server, tunnel: tunnel, addr: addr}, nil
}

// loopbackBind is where the control server binds.
//
// A constant so that "an agent worker opens no inbound port" is a fact about
// the code rather than a claim about its configuration: there is no flag, no
// environment variable and no argument that moves it.
const loopbackBind = "127.0.0.1:0"
