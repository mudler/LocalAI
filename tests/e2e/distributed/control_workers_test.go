package distributed_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// ControlWorkers is a fleet of fake workers serving the tunnelled control
// plane, which is where the frontend's backend and model lifecycle verbs go
// now that they have left the bus.
//
// It replaces the NATS SubscribeReply fakes these suites used to stand up. One
// HTTP server answers for every node and reads which node a request is for off
// the Host header, because that is what nodes.ControlClient puts there; a
// client addressing the wrong node would be visible rather than silently
// served.
//
// It is deliberately NOT a real worker: these suites are about what the
// frontend does with a worker's answer. The real handlers are exercised against
// the real client in core/services/worker.
type ControlWorkers struct {
	mu       sync.Mutex
	srv      *httptest.Server
	handlers map[string]func(nodeID string, body []byte) any
}

// controlHandlerKey is the (node, verb) a handler is registered for. AnyNode
// registers one handler for every node, which is how a suite fakes a fleet
// whose ids it does not know up front.
const AnyNode = "*"

func controlHandlerKey(nodeID, path string) string { return nodeID + " " + path }

// NewControlWorkers starts the fleet and stops it when the spec ends.
func NewControlWorkers() *ControlWorkers {
	c := &ControlWorkers{handlers: map[string]func(string, []byte) any{}}
	mux := http.NewServeMux()
	mux.HandleFunc(workerctl.Prefix, c.serve)
	c.srv = httptest.NewServer(mux)
	DeferCleanup(c.srv.Close)
	return c
}

// Client returns a control client that reaches this fleet.
func (c *ControlWorkers) Client() *nodes.ControlClient {
	addr := c.srv.Listener.Addr().String()
	return nodes.NewControlClient(func(string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	}, "")
}

// On registers what one node answers for one verb. Returning nil answers 204,
// which is the shape the fire-and-forget verbs take.
func (c *ControlWorkers) On(nodeID, path string, fn func(nodeID string, body []byte) any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[controlHandlerKey(nodeID, path)] = fn
}

func (c *ControlWorkers) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	Expect(err).ToNot(HaveOccurred())
	nodeID := strings.TrimSuffix(r.Host, ".worker.invalid:80")

	c.mu.Lock()
	fn, ok := c.handlers[controlHandlerKey(nodeID, r.URL.Path)]
	if !ok {
		fn, ok = c.handlers[controlHandlerKey(AnyNode, r.URL.Path)]
	}
	c.mu.Unlock()

	if !ok {
		// Loud, never plausible: a verb no spec scripted must be a red spec
		// rather than a worker that looks absent.
		http.Error(w, "no control handler registered for "+r.URL.Path+" on "+nodeID, http.StatusInternalServerError)
		return
	}

	reply := fn(nodeID, body)
	streaming := r.URL.Path == workerctl.PathBackendInstall || r.URL.Path == workerctl.PathBackendUpgrade
	if !streaming {
		if reply == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		Expect(json.NewEncoder(w).Encode(reply)).To(Succeed())
		return
	}

	raw, err := json.Marshal(reply)
	Expect(err).ToNot(HaveOccurred())
	w.Header().Set("Content-Type", workerctl.ContentTypeStream)
	w.WriteHeader(http.StatusOK)
	// The reply line, and it is the last thing on the body by contract.
	Expect(json.NewEncoder(w).Encode(workerctl.Envelope{Reply: raw})).To(Succeed())
}
