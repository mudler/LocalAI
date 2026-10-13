package agentworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// UnaryHandler answers a verb with one reply and no progress. The bytes it
// returns are the whole body of the response; nil is a 204.
//
// An error is the worker failing to serve the verb, never the bad news of the
// verb. A body on a 200 is the answer of the worker, and everything else is a
// failure that the frontend must not read as evidence about anything. An MCP
// tool that ran and failed is an answer, and travels as bytes with its Error
// field set. A request that cannot be decoded is ErrUndecodable.
type UnaryHandler func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error)

// ErrUndecodable marks a request body that is not a request of the verb. It is
// a 400: a body that could not be read is not an answer about anything.
var ErrUndecodable = errors.New("the request body cannot be decoded")

// Unary adapts a typed handler to a UnaryHandler. The typed handler returns no
// error, because every failure of an MCP request is an answer that the reply
// carries in its Error field, so the requester never waits out its budget on
// silence.
func Unary[Req, Rep any](h func(ctx context.Context, req Req) Rep) UnaryHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var req Req
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUndecodable, err)
		}
		out, err := json.Marshal(h(ctx, req))
		if err != nil {
			return nil, fmt.Errorf("encoding the reply: %w", err)
		}
		return out, nil
	}
}

// Config is the part of the control plane that answers with one reply.
//
// A nil field means that this worker does not serve the verb, and nothing is
// mounted for it, so the catch-all under the control prefix answers 404. That is
// the answer of an older build, which the frontend reads as a worker that does
// not serve the verb, and not as a worker that is gone.
type Config struct {
	// MCPTool answers workerctl.VerbMCPToolExecute.
	MCPTool UnaryHandler
	// MCPDiscovery answers workerctl.VerbMCPDiscovery.
	MCPDiscovery UnaryHandler
	// BackendStop drops the MCP sessions cached for a backend that goes away. It
	// answers workerctl.VerbBackendStop on the same path that a backend worker
	// serves with another implementation, so the frontend sends one request to
	// either kind of worker.
	BackendStop func(ctx context.Context, req workerctl.BackendStopRequest) error
}

// Handler returns the control plane of an agent worker: the verbs of cfg, the
// runs that work consumes, and the catch-all for every other path under the
// prefix. It does no authentication. Start puts one check in front of it.
func Handler(cfg Config, work *Work) http.Handler {
	mux := http.NewServeMux()
	if cfg.MCPTool != nil {
		mux.HandleFunc(workerctl.PathOf(workerctl.VerbMCPToolExecute), serveUnary(workerctl.VerbMCPToolExecute, cfg.MCPTool))
	}
	if cfg.MCPDiscovery != nil {
		mux.HandleFunc(workerctl.PathOf(workerctl.VerbMCPDiscovery), serveUnary(workerctl.VerbMCPDiscovery, cfg.MCPDiscovery))
	}
	if cfg.BackendStop != nil {
		mux.HandleFunc(workerctl.PathOf(workerctl.VerbBackendStop), serveBackendStop(cfg.BackendStop))
	}
	if work != nil {
		work.register(mux)
	}
	// Mounted last and always: a path under the prefix that no verb claims is a
	// frontend newer than this worker.
	mux.HandleFunc(workerctl.Prefix, workerctl.WriteUnknownPath)
	return mux
}

// failToServe answers a verb that this worker could not serve. 500 and not 404,
// which the frontend reads as a verb the worker does not implement, and not a
// 2xx, which it reads as an answer.
func failToServe(w http.ResponseWriter, verb string, err error) {
	http.Error(w, "the "+verb+" verb could not be served: "+err.Error(), http.StatusInternalServerError)
}

// refuseRequest answers a request that this worker could not read. The fault is
// the caller's, and the status is not 2xx for the reason above.
func refuseRequest(w http.ResponseWriter, verb string, err error) {
	http.Error(w, "the "+verb+" verb could not be served: "+err.Error(), http.StatusBadRequest)
}

func serveUnary(verb string, h UnaryHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		reply, err := h(r.Context(), json.RawMessage(body))
		switch {
		case errors.Is(err, ErrUndecodable):
			refuseRequest(w, verb, err)
			return
		case err != nil:
			failToServe(w, verb, err)
			return
		case len(reply) == 0:
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(reply); err != nil {
			xlog.Debug("agent worker reply could not be written", "verb", verb, "error", err)
		}
	}
}

// serveBackendStop answers 204 with no body, which is what a backend worker
// answers on the same path.
func serveBackendStop(h func(ctx context.Context, req workerctl.BackendStopRequest) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		var req workerctl.BackendStopRequest
		if err := json.Unmarshal(body, &req); err != nil {
			refuseRequest(w, workerctl.VerbBackendStop, err)
			return
		}
		// The context of the caller is dropped, as a backend worker drops it: the
		// stop has no answer that anyone waits for, and a cleanup abandoned half
		// way would leave sessions cached for a backend that is gone.
		if err := h(context.WithoutCancel(r.Context()), req); err != nil {
			failToServe(w, workerctl.VerbBackendStop, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
