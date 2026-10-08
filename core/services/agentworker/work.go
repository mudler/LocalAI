package agentworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// Work is the WorkConsumer of an agent worker on the tunnel. A handler that was
// written for a queue of a bus runs here unchanged: Consume registers it, and
// the request that a frontend sends for a run is the delivery.
//
// The differences from a queue group are all in who chooses. A frontend picks the
// worker, so a worker that is full answers busy (see workerctl.WriteBusy) and the
// frontend offers the run to another worker. On a queue group a full worker held
// the delivery until it had room.
type Work struct {
	mu     sync.Mutex
	runner map[string]*runner
}

// NewWork returns a consumer with nothing registered.
func NewWork() *Work { return &Work{runner: map[string]*runner{}} }

// verbOf maps a kind onto the verb that serves it. A plain task has none: no
// agent worker serves it.
func verbOf(kind messaging.WorkKind) (string, bool) {
	switch kind {
	case messaging.WorkAgentRun:
		return workerctl.VerbAgentExecute, true
	case messaging.WorkMCPCI:
		return workerctl.VerbMCPCIRun, true
	}
	return "", false
}

// runner is the registration of one kind.
type runner struct {
	verb    string
	parent  context.Context
	handler messaging.WorkHandler
	slots   chan struct{} // nil: unbounded

	mu      sync.Mutex
	closed  bool
	running sync.WaitGroup
}

// begin takes a slot, or reports that there is none. It never waits.
func (r *runner) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	if r.slots != nil {
		select {
		case r.slots <- struct{}{}:
		default:
			return false
		}
	}
	r.running.Add(1)
	return true
}

func (r *runner) end() {
	if r.slots != nil {
		<-r.slots
	}
	r.running.Done()
}

// Consume registers h for kind. maxInFlight bounds the runs at once: 0 and
// negative values are unbounded, as on the other carriers. Unsubscribe stops
// taking runs and waits for those in flight, so a handler must not call it.
func (w *Work) Consume(ctx context.Context, kind messaging.WorkKind, maxInFlight int, h messaging.WorkHandler) (messaging.Subscription, error) {
	verb, ok := verbOf(kind)
	if !ok {
		return nil, fmt.Errorf("no agent worker verb serves the work kind %q", kind)
	}
	r := &runner{verb: verb, parent: ctx, handler: h}
	if maxInFlight > 0 {
		r.slots = make(chan struct{}, maxInFlight)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, taken := w.runner[verb]; taken {
		return nil, fmt.Errorf("the %s verb already has a consumer", verb)
	}
	w.runner[verb] = r
	return &subscription{work: w, r: r}, nil
}

type subscription struct {
	work *Work
	r    *runner
}

func (s *subscription) Unsubscribe() error {
	s.r.mu.Lock()
	s.r.closed = true
	s.r.mu.Unlock()
	s.work.mu.Lock()
	if s.work.runner[s.r.verb] == s.r {
		delete(s.work.runner, s.r.verb)
	}
	s.work.mu.Unlock()
	s.r.running.Wait()
	return nil
}

func (w *Work) lookup(verb string) *runner {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runner[verb]
}

// register mounts the verbs of the runs. A verb with no consumer answers busy and
// not 404: the worker serves it and has nobody to run it, which is a worker that
// is starting or stopping.
func (w *Work) register(mux *http.ServeMux) {
	for _, kind := range []messaging.WorkKind{messaging.WorkAgentRun, messaging.WorkMCPCI} {
		verb, _ := verbOf(kind)
		mux.HandleFunc(workerctl.PathOf(verb), w.serve(verb))
	}
}

func (w *Work) serve(verb string) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		body, ok := workerctl.ReadRequestBody(rw, req)
		if !ok {
			return
		}
		r := w.lookup(verb)
		if r == nil || !r.begin() {
			workerctl.WriteBusy(rw)
			return
		}
		defer r.end()

		// The run ends when the caller leaves and when the worker stops: either
		// way nobody is left to read its result.
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		stop := context.AfterFunc(r.parent, cancel)
		defer stop()

		stream := &stream{w: rw}
		err := runHandler(ctx, r.handler, body, stream)
		if err != nil {
			if !stream.committed() {
				failToServe(rw, verb, err)
				return
			}
			// The status is spent. A body with no reply line is a link that broke,
			// which is the only honest thing to tell the caller: this worker did
			// not answer.
			xlog.Warn("An agent worker run failed after it had streamed events, so its response ends with no reply line",
				"verb", verb, "error", err)
			return
		}
		stream.finish(verb)
	}
}

// runHandler calls h and turns a panic into an error, so one bad run does not
// take the worker down with it.
func runHandler(ctx context.Context, h messaging.WorkHandler, payload []byte, events messaging.Publisher) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the run panicked: %v", p)
		}
	}()
	return h(ctx, payload, events)
}

// stream writes the lines of one streaming response. It is the Publisher that a
// handler gets as the place for its events.
//
// A handler may publish from several goroutines, so the mutex is not optional:
// without it a progress line could interleave with the reply and put a torn line
// on the wire. done keeps the reply the last line.
type stream struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	header bool
	done   bool
	result *workerctl.RunReply
}

// Publish writes one event as a progress line that names its subject. The result
// of a job is also kept: it becomes the reply line.
func (s *stream) Publish(subject string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding an event: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	if messaging.SubjectMatches(messaging.SubjectJobResultWildcard, subject) {
		var res workerctl.RunReply
		if json.Unmarshal(raw, &res) == nil {
			s.result = &res
		}
	}
	s.write(workerctl.Envelope{Subject: subject, Progress: raw})
	return nil
}

func (s *stream) committed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// finish writes the reply line: the result the run published, or an empty reply
// for a run that publishes none.
func (s *stream) finish(verb string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	reply := workerctl.RunReply{}
	if s.result != nil {
		reply = *s.result
	}
	raw, err := json.Marshal(reply)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	s.write(workerctl.Envelope{Reply: raw})
	s.done = true
	xlog.Debug("agent worker run finished", "verb", verb)
}

// write encodes one line and flushes it. The caller holds s.mu.
func (s *stream) write(env workerctl.Envelope) {
	if !s.header {
		s.w.Header().Set("Content-Type", workerctl.ContentTypeStream)
		s.w.Header().Set("X-Content-Type-Options", "nosniff")
		s.w.WriteHeader(http.StatusOK)
		s.header = true
	}
	if err := json.NewEncoder(s.w).Encode(env); err != nil {
		xlog.Debug("agent worker stream line could not be written", "error", err)
		return
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}
