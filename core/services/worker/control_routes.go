package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// The worker's control plane, served as ordinary HTTP on the loopback server
// the worker already runs, and reached only through the tunnel's `http` stream
// tag. It replaces ten NATS subscriptions.
//
// HTTP and not a new stream tag, and the reason is not convenience. Every one
// of correlation, per-request deadlines, unbounded payloads and a progress
// stream is something a new tag would have had to invent, and each is a place
// this branch has already put a defect. Riding the tag that already exists also
// adds nothing to the worker's stream-refusal vocabulary, which is the table a
// frontend decides what to reap on. And a control RPC to a worker another
// replica holds takes the SAME relay the inference path takes, which is the
// path that has been measured, rather than a second one that has not.
//
// A handler's own failure is a 200 carrying a reply with Error set, NOT a 5xx.
// The distinction is load-bearing: the frontend maps a transport failure onto
// "this frontend has no route to that worker", which nothing may reap on, and a
// worker's answer onto evidence a reap guard MAY act on. A handler that
// answered 500 for "the install failed" would put the worker's own verdict into
// the bucket reserved for a broken link. Only a failure to read or route the
// request is a non-2xx.

// maxControlRequestBytes bounds a control request body.
//
// The largest real body is BackendInstallRequest.BackendGalleries, a serialized
// gallery list of a few hundred kilobytes. Eight megabytes is therefore not a
// size the protocol needs: it is a defence against a body that never ends,
// arriving on a boundary this worker now serves.
const maxControlRequestBytes = 8 << 20

// maxEchoedPathBytes bounds how much of an unknown control path the 404 body
// repeats back. The path is caller-controlled and the answer exists to be read
// in a log line, so a caller cannot make this worker echo a request-sized
// string into one.
const maxEchoedPathBytes = 128

// installFunc and upgradeFunc are the shapes of the two long-running verbs.
// They exist as named types so the fields that override them below read as one
// thing rather than as two inline signatures.
type installFunc func(ctx context.Context, req messaging.BackendInstallRequest, force bool,
	onProgress func(messaging.BackendInstallProgressEvent)) (string, error)

type upgradeFunc func(ctx context.Context, req messaging.BackendUpgradeRequest,
	onProgress func(messaging.BackendInstallProgressEvent)) ([]string, error)

// installer and upgrader return the implementation the streaming verbs call.
//
// The override fields exist so the ROUTING can be specced without a gallery, a
// registry or a real download, which is the same argument tunnelServices was
// extracted under: the part of this file that can put a worker's verdict in the
// wrong bucket is the part that has nothing to do with installing anything.
func (s *backendSupervisor) installer() installFunc {
	if s.installFn != nil {
		return s.installFn
	}
	return s.installBackend
}

func (s *backendSupervisor) upgrader() upgradeFunc {
	if s.upgradeFn != nil {
		return s.upgradeFn
	}
	return s.upgradeBackend
}

// RegisterControlRoutes mounts every control verb on mux.
//
// The caller is responsible for putting mux behind authentication; see
// nodes.AuthenticatedRoutes, which is how the worker mounts this so the control
// plane shares one bearer check with the file routes rather than growing a
// second one.
func (s *backendSupervisor) RegisterControlRoutes(mux *http.ServeMux) {
	// post registers one JSON-in, JSON-out verb. reply may be nil, in which
	// case the verb answers 204: that is the shape the two former
	// publish-no-reply subjects take.
	post := func(path string, h func(ctx context.Context, body []byte) (any, error)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			body, ok := readControlBody(w, r)
			if !ok {
				return
			}
			reply, err := h(r.Context(), body)
			if err != nil {
				// Reaching here means the request could not be READ or routed,
				// which is this worker failing rather than answering. A verb's
				// own failure never reaches here: it comes back as a reply with
				// Error set, and a 200.
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if reply == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if encErr := json.NewEncoder(w).Encode(reply); encErr != nil {
				xlog.Debug("worker control reply could not be written", "path", path, "error", encErr)
			}
		})
	}

	post(workerctl.PathModelsRunning, func(context.Context, []byte) (any, error) {
		return messaging.ModelsRunningReply{Models: s.runningModels()}, nil
	})

	post(workerctl.PathModelStop, func(_ context.Context, body []byte) (any, error) {
		var req messaging.ModelStopRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid model.stop request: %w", err)
		}
		return s.stopModelExact(req), nil
	})

	post(workerctl.PathBackendList, func(context.Context, []byte) (any, error) {
		return s.backendList(), nil
	})

	post(workerctl.PathBackendDelete, func(_ context.Context, body []byte) (any, error) {
		var req messaging.BackendDeleteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid backend.delete request: %w", err)
		}
		return s.deleteBackend(req), nil
	})

	post(workerctl.PathModelUnload, func(ctx context.Context, body []byte) (any, error) {
		var req messaging.ModelUnloadRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid model.unload request: %w", err)
		}
		return s.unloadModel(ctx, req), nil
	})

	post(workerctl.PathModelDelete, func(_ context.Context, body []byte) (any, error) {
		var req messaging.ModelDeleteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("invalid model.delete request: %w", err)
		}
		return s.deleteModel(req), nil
	})

	post(workerctl.PathBackendStop, func(_ context.Context, body []byte) (any, error) {
		req, stopAll, err := decodeBackendStopRequest(body)
		if err != nil {
			return nil, fmt.Errorf("invalid backend.stop request: %w", err)
		}
		s.stopBackends(req, stopAll)
		return nil, nil
	})

	post(workerctl.PathNodeStop, func(context.Context, []byte) (any, error) {
		// The signal is sent before the 204 is written, and that ordering is
		// safe rather than lucky: sigCh is buffered and this send never blocks,
		// and the shutdown it starts is a graceful one that waits for this
		// request to finish before closing the listener.
		s.signalNodeStop()
		return nil, nil
	})

	// The two streaming verbs. They write NDJSON rather than one JSON object,
	// so they do not go through post.
	mux.HandleFunc(workerctl.PathBackendInstall, s.serveInstall)
	mux.HandleFunc(workerctl.PathBackendUpgrade, s.serveUpgrade)

	mux.HandleFunc(workerctl.Prefix, func(w http.ResponseWriter, r *http.Request) {
		// The catch-all. A path under the control prefix that no verb claims is
		// a frontend newer than this worker, and the body says so, because a
		// bare 404 through a tunnel is indistinguishable from a proxy fault.
		http.Error(w, "unknown worker control path "+truncate(r.URL.Path, maxEchoedPathBytes), http.StatusNotFound)
	})
}

// readControlBody enforces the two things every control verb requires of a
// request: that it is a POST, and that its body is bounded.
//
// A GET is refused rather than served because a control verb is a command, and
// a liveness probe, a link prefetch or a browser address bar must not be able
// to stop a node.
func readControlBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "control verbs are POST only", http.StatusMethodNotAllowed)
		return nil, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlRequestBytes))
	if err != nil {
		// A body this worker could not READ is not an answer about any
		// backend, so it must not look like one: 400 is what the frontend maps
		// onto "the request was rejected", never onto "that model is gone".
		http.Error(w, "reading the control request body: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// truncate bounds a caller-controlled string that is about to be echoed.
// It cuts on a rune boundary: a byte-wise cut can split a multi-byte rune, and
// the half-rune then travels as a replacement character through every log and
// UI that reads it.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// isRuneStart reports whether b begins a UTF-8 rune (i.e. is not a
// continuation byte).
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ndjsonStream writes the Envelope lines of one streaming control response.
//
// The mutex is not optional. A progress line can be emitted from the debounce
// timer's own goroutine, so without serialization a progress write can
// interleave with the terminal reply write and put a torn line on the wire,
// and the frontend's contract is that the reply line is the LAST thing on the
// body. done is what enforces that half: once the reply is written, a late
// progress line is dropped rather than appended after it.
type ndjsonStream struct {
	mu   sync.Mutex
	w    http.ResponseWriter
	enc  *json.Encoder
	done bool
}

func newNDJSONStream(w http.ResponseWriter) *ndjsonStream {
	w.Header().Set("Content-Type", workerctl.ContentTypeStream)
	// A streaming body must not be buffered into a guessed content type: the
	// caller reads line by line and the first line may be minutes before the
	// last.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	return &ndjsonStream{w: w, enc: json.NewEncoder(w)}
}

// progress writes one progress line. It is a no-op once the reply has been
// written.
func (n *ndjsonStream) progress(ev messaging.BackendInstallProgressEvent) {
	raw, err := json.Marshal(ev)
	if err != nil {
		xlog.Debug("worker control progress event could not be marshalled", "error", err)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	n.write(workerctl.Envelope{Progress: raw})
}

// reply writes the single terminal reply line and closes the stream to any
// further progress.
func (n *ndjsonStream) reply(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		// The caller is waiting for a terminal line and will otherwise read to
		// EOF without one, which it cannot tell apart from a truncated
		// response. Send a reply it can decode as a failure of THIS worker's
		// own making rather than sending nothing.
		xlog.Error("worker control reply could not be marshalled", "error", err)
		raw = json.RawMessage(`{"success":false,"error":"the worker could not encode its own reply"}`)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	n.write(workerctl.Envelope{Reply: raw})
	n.done = true
}

// write encodes one envelope and flushes it. Callers hold n.mu.
func (n *ndjsonStream) write(env workerctl.Envelope) {
	if err := n.enc.Encode(env); err != nil {
		xlog.Debug("worker control stream line could not be written", "error", err)
		return
	}
	if f, ok := n.w.(http.Flusher); ok {
		f.Flush()
	}
}

// serveInstall answers backend.install, streaming download progress ahead of
// the single terminal reply.
//
// The NATS handler this replaces ran its work on a fresh goroutine so a slow
// install could not head-of-line-block the one subscription every install
// arrived on. Over HTTP each request already has its own goroutine, so there is
// nothing left to block and no goroutine is started here. Per-backend
// serialization is unchanged and still comes from lockBackend, which is what
// actually prevented two requests racing the gallery directory.
func (s *backendSupervisor) serveInstall(w http.ResponseWriter, r *http.Request) {
	body, ok := readControlBody(w, r)
	if !ok {
		return
	}
	var req messaging.BackendInstallRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("invalid backend.install request: %v", err), http.StatusBadRequest)
		return
	}
	xlog.Info("Serving backend.install", "backend", req.Backend, "model", req.ModelID)

	stream := newNDJSONStream(w)

	release := s.lockBackend(req.Backend)
	defer release()

	// req.Force=true is the legacy path used by pre-2026-05-08 masters that
	// don't know about backend.upgrade. Honor it so a rolling update with new
	// worker + old master keeps working; new masters send to backend.upgrade
	// instead.
	addr, err := s.installer()(r.Context(), req, req.Force, stream.progress)
	if err != nil {
		xlog.Error("Failed to install backend", "error", err)
		stream.reply(messaging.BackendInstallReply{Success: false, Error: err.Error()})
		return
	}

	// The address goes back exactly as the process listens on it. It used to be
	// rewritten onto this worker's advertise host, which made the reply the
	// worker's third advertisement site; the frontend now reads only the port
	// out of it and dials nothing.
	stream.reply(messaging.BackendInstallReply{Success: true, WorkerLocalAddress: addr})
}

// serveUpgrade answers backend.upgrade, a force-reinstall, on the same
// streaming shape as install.
func (s *backendSupervisor) serveUpgrade(w http.ResponseWriter, r *http.Request) {
	body, ok := readControlBody(w, r)
	if !ok {
		return
	}
	var req messaging.BackendUpgradeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("invalid backend.upgrade request: %v", err), http.StatusBadRequest)
		return
	}
	xlog.Info("Serving backend.upgrade", "backend", req.Backend)

	stream := newNDJSONStream(w)

	release := s.lockBackend(req.Backend)
	defer release()

	// stopped is meaningful even on the error paths: it lists processes already
	// terminated (and ports already recycled) before the failure, so the
	// controller must drop those rows regardless of the outcome.
	stopped, err := s.upgrader()(r.Context(), req, stream.progress)
	if err != nil {
		xlog.Error("Failed to upgrade backend", "error", err)
		stream.reply(messaging.BackendUpgradeReply{
			Success:                 false,
			Error:                   err.Error(),
			StoppedProcessKeys:      stopped,
			ReportsStoppedProcesses: true,
		})
		return
	}
	stream.reply(messaging.BackendUpgradeReply{
		Success:                 true,
		StoppedProcessKeys:      stopped,
		ReportsStoppedProcesses: true,
	})
}
