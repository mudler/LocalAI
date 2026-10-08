package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// httpControlServer serves the control verbs of this worker as HTTP, under
// workerctl.Prefix. The worker mounts it on the HTTP server it already runs, and
// the frontend reaches that server through the tunnel with the http stream tag.
//
// It is the second carrier of the verbs that natsControlServer serves. The
// handlers are the same functions and the bodies are the same payloads of
// package workerctl, so a worker that is reached over NATS and one that is
// reached over a tunnel answer with the same bytes.
//
// The answer of a verb is a 200 with the reply, and the reply carries the
// worker's own refusal in its Error field. A non-2xx status is the worker
// failing to read or route the request, and it is never evidence about a
// backend: 400 for a body that cannot be read, 404 for a verb this worker does
// not serve, 405 for a method other than POST. The frontend maps each of them
// to a different condition, so a handler must not use them for its own result.
type httpControlServer struct {
	inflight
	mux *http.ServeMux

	mu     sync.Mutex
	served map[controlVerb]bool
}

func newHTTPControlServer() *httpControlServer {
	s := &httpControlServer{mux: http.NewServeMux(), served: map[controlVerb]bool{}}
	// Every path under the prefix that no verb claims. The body says what
	// happened, because a bare 404 through a tunnel cannot be told from a fault of
	// a proxy.
	s.mux.HandleFunc(workerctl.Prefix, workerctl.WriteUnknownPath)
	return s
}

// ServeHTTP implements http.Handler.
func (s *httpControlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// route claims the path of a verb. A verb outside the vocabulary or one claimed
// twice is an error that names the verb: the NATS server refuses a verb it has
// no subject for in the same way.
func (s *httpControlServer) route(v controlVerb) (string, error) {
	if !slices.Contains(workerctl.AllVerbs(), string(v)) {
		return "", fmt.Errorf("serving %s: no HTTP path for that control verb", v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.served[v] {
		return "", fmt.Errorf("serving %s: the verb is already served", v)
	}
	s.served[v] = true
	return workerctl.PathOf(string(v)), nil
}

// The handlers of a verb get a context that the caller going away does not
// cancel, as the NATS server does with context.Background. A verb bounds its own
// work, and an unload that stopped half way because the frontend gave up on the
// reply would leave a backend half freed.

// handle serves a verb that answers with one JSON body, or with 204 when the
// handler returns no reply. Requests of one verb are delivered concurrently:
// the NATS server delivers them one at a time, and the handlers are written to
// be called from several goroutines.
func (s *httpControlServer) handle(v controlVerb, h controlHandler) error {
	path, err := s.route(v)
	if err != nil {
		return err
	}
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		defer s.enter()()
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		reply, undecodable := h(context.WithoutCancel(r.Context()), body)
		if undecodable != nil {
			// The body is not a request of this verb. The typed refusal in reply
			// is what the NATS server sends. Here the request failed before it
			// was a request, and 400 says that without making it an answer of the
			// worker about any backend.
			http.Error(w, fmt.Sprintf("invalid %s request: %v", v, undecodable), http.StatusBadRequest)
			return
		}
		if reply == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(reply); err != nil {
			xlog.Debug("worker control reply could not be written", "verb", v, "error", err)
		}
	})
	return nil
}

// handleWithProgress serves a verb that runs for a long time and reports
// progress while it runs. The body is a stream of workerctl.Envelope lines: any
// number of progress lines and then exactly one reply line.
func (s *httpControlServer) handleWithProgress(v controlVerb, h progressControlHandler) error {
	path, err := s.route(v)
	if err != nil {
		return err
	}
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		defer s.enter()()
		body, ok := workerctl.ReadRequestBody(w, r)
		if !ok {
			return
		}
		stream := newNDJSONStream(w)
		reply, undecodable := h(context.WithoutCancel(r.Context()), body, stream.progress)
		if undecodable != nil {
			// Nothing was written yet unless a handler reported progress before it
			// decoded, which none does. The status is still open.
			stream.abort(fmt.Sprintf("invalid %s request: %v", v, undecodable))
			return
		}
		stream.reply(reply)
	})
	return nil
}

// ndjsonStream writes the lines of one streaming control response.
//
// The mutex is not optional. A progress line can come from the timer goroutine
// of a debounce, so without it a progress write could interleave with the reply
// and put a torn line on the wire. done keeps the contract that the reply is the
// last line: a progress event that arrives after it is dropped.
type ndjsonStream struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	enc     *json.Encoder
	done    bool
	started bool
}

func newNDJSONStream(w http.ResponseWriter) *ndjsonStream {
	return &ndjsonStream{w: w, enc: json.NewEncoder(w)}
}

// start writes the head of the response. It is delayed to the first line so
// that a request that fails before any line can still answer with a status.
func (n *ndjsonStream) start() {
	if n.started {
		return
	}
	n.started = true
	n.w.Header().Set("Content-Type", workerctl.ContentTypeStream)
	// The caller reads line by line, and the first line can come minutes before
	// the last.
	n.w.Header().Set("X-Content-Type-Options", "nosniff")
	n.w.WriteHeader(http.StatusOK)
}

func (n *ndjsonStream) progress(ev workerctl.BackendInstallProgressEvent) {
	raw, err := json.Marshal(ev)
	if err != nil {
		xlog.Debug("worker control progress event could not be encoded", "error", err)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	n.start()
	n.write(workerctl.Envelope{Progress: raw})
}

// reply writes the one reply line and closes the stream to later progress.
func (n *ndjsonStream) reply(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		// The caller waits for a reply line, and a body that ends without one
		// cannot be told from a link that broke. A reply that says the worker
		// could not encode its own answer can.
		xlog.Error("worker control reply could not be encoded", "error", err)
		raw = json.RawMessage(`{"success":false,"error":"the worker could not encode its own reply"}`)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		return
	}
	n.start()
	n.write(workerctl.Envelope{Reply: raw})
	n.done = true
}

// abort answers a request that failed before it started to stream.
func (n *ndjsonStream) abort(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.started || n.done {
		return
	}
	n.done = true
	http.Error(n.w, msg, http.StatusBadRequest)
}

// write encodes one line and flushes it. The caller holds n.mu.
func (n *ndjsonStream) write(env workerctl.Envelope) {
	if err := n.enc.Encode(env); err != nil {
		xlog.Debug("worker control stream line could not be written", "error", err)
		return
	}
	if f, ok := n.w.(http.Flusher); ok {
		f.Flush()
	}
}
