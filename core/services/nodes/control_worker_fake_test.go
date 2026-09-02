package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// controlKey names one control verb on one node.
//
// It replaces the NATS subject the scripted double used to key on, and it is
// the same pair the real client addresses: the node picks the tunnel and the
// path picks the verb.
func controlKey(nodeID, path string) string { return nodeID + " " + path }

// scriptedControlWorkers is a fleet of fake workers reachable only the way a
// real one is: through a per-node dialer, over HTTP, on the control paths.
//
// One HTTP server serves every node, and which node a request is FOR is read
// off the Host header, because that is what ControlClient puts there. That is
// not a convenience: it means a client addressing the wrong node's URL through
// the right node's tunnel would be visible here, which a per-node server could
// not see.
type scriptedControlWorkers struct {
	mu  sync.Mutex
	srv *httptest.Server

	replies     map[string][]byte
	unsupported map[string]bool
	matched     map[string][]matchedControlReply
	progress    map[string][]messaging.BackendInstallProgressEvent

	// unreachable and expired are keyed by NODE, not by verb, because they are
	// failures of the ROUTE and a route belongs to a node. They are what the
	// dialer answers with; see scriptUnroutable and scriptTimeout.
	unreachable map[string]bool
	expired     map[string]bool

	// hangs names the verbs whose handler never answers, so the only thing
	// that ends the call is the caller's own budget.
	hangs map[string]bool

	// calls records every request that reached a worker, in order.
	calls []requestCall
}

// matchedControlReply is a canned reply that fires only for a request matching
// pred. It exists so a spec can tell "install with Force=true" (the legacy
// upgrade fallback) from an ordinary install on the same verb.
type matchedControlReply struct {
	pred  func(messaging.BackendInstallRequest) bool
	reply []byte
}

func newScriptedControlWorkers() *scriptedControlWorkers {
	s := &scriptedControlWorkers{
		replies:     map[string][]byte{},
		unsupported: map[string]bool{},
		matched:     map[string][]matchedControlReply{},
		progress:    map[string][]messaging.BackendInstallProgressEvent{},
		unreachable: map[string]bool{},
		expired:     map[string]bool{},
		hangs:       map[string]bool{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(workerctl.Prefix, s.serve)
	s.srv = httptest.NewServer(mux)
	DeferCleanup(s.srv.Close)
	return s
}

// dialer hands ControlClient the per-node transport it expects.
//
// A node scripted unreachable or expired never reaches the server, which is
// what a real route failure looks like: nothing is asked of the worker and
// nothing is learned about it.
func (s *scriptedControlWorkers) dialer() WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			s.mu.Lock()
			unreachable, expired := s.unreachable[nodeID], s.expired[nodeID]
			s.mu.Unlock()
			switch {
			case expired:
				return nil, context.DeadlineExceeded
			case unreachable:
				return nil, fmt.Errorf("reaching node %q: %w", nodeID, cluster.ErrNoRoute)
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", s.srv.Listener.Addr().String())
		}
	}
}

func (s *scriptedControlWorkers) controlClient() *ControlClient {
	return NewControlClient(s.dialer(), "test-token")
}

// nodeOf recovers the node a request was addressed to from its Host header.
func nodeOf(host string) string {
	return strings.TrimSuffix(host, unroutableHostSuffix)
}

func (s *scriptedControlWorkers) serve(w http.ResponseWriter, r *http.Request) {
	body, readErr := io.ReadAll(r.Body)
	Expect(readErr).ToNot(HaveOccurred())
	key := controlKey(nodeOf(r.Host), r.URL.Path)

	s.mu.Lock()
	s.calls = append(s.calls, requestCall{Subject: key, Data: body})
	unsupported := s.unsupported[key]
	hang := s.hangs[key]
	reply := s.replies[key]
	matchers := s.matched[key]
	ticks := s.progress[key]
	s.mu.Unlock()

	if hang {
		// The worker took the request and never answered. Only the caller's
		// own budget ends this, which is what makes the budget observable.
		<-r.Context().Done()
		return
	}
	if unsupported {
		http.Error(w, "unknown worker control path "+r.URL.Path, http.StatusNotFound)
		return
	}
	if len(matchers) > 0 {
		var req messaging.BackendInstallRequest
		_ = json.Unmarshal(body, &req)
		for _, m := range matchers {
			if m.pred(req) {
				reply = m.reply
				break
			}
		}
	}
	if reply == nil {
		// A verb no spec scripted. Answered LOUDLY rather than plausibly: a
		// forgotten script must be a red spec, never a worker that looks absent.
		http.Error(w, "this spec scripted no answer for "+key, http.StatusInternalServerError)
		return
	}

	if r.URL.Path != workerctl.PathBackendInstall && r.URL.Path != workerctl.PathBackendUpgrade {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(reply)
		return
	}

	// The two streaming verbs: zero or more progress lines, then exactly one
	// reply line, always last.
	w.Header().Set("Content-Type", workerctl.ContentTypeStream)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, ev := range ticks {
		raw, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		_ = enc.Encode(workerctl.Envelope{Progress: raw})
	}
	_ = enc.Encode(workerctl.Envelope{Reply: reply})
}

func (s *scriptedControlWorkers) scriptReply(key string, reply any) {
	raw, err := json.Marshal(reply)
	Expect(err).ToNot(HaveOccurred())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[key] = raw
}

// scriptRawReply scripts a reply byte for byte, so a spec can express what a
// worker on a DIFFERENT build would send rather than what today's struct
// marshals to.
func (s *scriptedControlWorkers) scriptRawReply(key string, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[key] = raw
}

// scriptUnsupported makes the worker answer 404 for one verb, which is what a
// build older than this frontend does.
func (s *scriptedControlWorkers) scriptUnsupported(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsupported[key] = true
}

// scriptUnroutable makes every route to a node fail without reaching it. The
// worker is asked nothing, so nothing is learned about it.
func (s *scriptedControlWorkers) scriptUnroutable(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreachable[nodeID] = true
}

// scriptTimeout makes a node's RPCs end with the caller's budget spent.
//
// Injected at the dial rather than by making a handler sleep, and the
// difference is only in how long the spec takes: a real worker that answers too
// late ends the same way, with the caller's own deadline, because
// cluster.WorkerDialer reports a handshake that outlives the budget as exactly
// this. Answering it instantly keeps the adapter's real install timeout, which
// the retry-scheduling assertions read.
func (s *scriptedControlWorkers) scriptTimeout(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired[nodeID] = true
}

// clearTimeout lets a node answer again, as a worker that finished a long
// install in the background does.
func (s *scriptedControlWorkers) clearTimeout(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.expired, nodeID)
}

func (s *scriptedControlWorkers) scriptReplyMatching(key string, pred func(messaging.BackendInstallRequest) bool, reply messaging.BackendInstallReply) {
	raw, err := json.Marshal(reply)
	Expect(err).ToNot(HaveOccurred())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matched[key] = append(s.matched[key], matchedControlReply{pred: pred, reply: raw})
}

// scriptProgress queues the progress lines a streaming verb writes ahead of its
// reply. There is no window to miss them in and nothing to subscribe to: they
// are part of the response the caller is already reading.
func (s *scriptedControlWorkers) scriptProgress(key string, events []messaging.BackendInstallProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress[key] = events
}

// scriptHang makes one verb on one node accept the request and never answer,
// so the call ends only when the caller's budget does.
//
// It is how a spec observes which budget a verb was given. HTTP carries no
// deadline and the transport dials on a context of its own, so the budget is
// invisible from the far side; how long the client is willing to wait is the
// only thing that shows it.
func (s *scriptedControlWorkers) scriptHang(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hangs[key] = true
}

// callSubjects reports the (node, verb) pairs that reached a worker, in order.
func (s *scriptedControlWorkers) callSubjects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.Subject)
	}
	return out
}
