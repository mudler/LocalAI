package distributed_test

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

	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
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

// ServeBackendLifecycle registers the three verbs every routing spec needs from
// a worker: install, the running-model list, and the backend list.
//
// The install reply NAMES the address of the backend process the fake worker
// started, and that is the half these suites used to leave out. Since phase 2 a
// worker advertises no address of its own, so this string is the only thing
// that tells the frontend WHICH process on that worker a model was loaded into,
// and installBackendOnNode refuses a success reply that omits it rather than
// substituting anything. Every one of these suites runs its mock gRPC backend
// on loopback and records where it listens in the node row it registers, so
// that row is where this reads it back from. It is the spec's own bookkeeping
// standing in for what a real worker reports about its own process; nothing in
// production reads BackendNode.Address any more.
func (c *ControlWorkers) ServeBackendLifecycle(registry *nodes.NodeRegistry) {
	c.On(AnyNode, workerctl.PathBackendInstall, func(nodeID string, _ []byte) any {
		node, err := registry.Get(context.Background(), nodeID)
		if err != nil {
			return messaging.BackendInstallReply{Success: false, Error: err.Error()}
		}
		return messaging.BackendInstallReply{Success: true, WorkerLocalAddress: node.Address}
	})
	c.On(AnyNode, workerctl.PathModelsRunning, func(string, []byte) any {
		return messaging.ModelsRunningReply{}
	})
	c.On(AnyNode, workerctl.PathBackendList, func(string, []byte) any {
		return messaging.BackendListReply{}
	})
}

// workerBackendDialerFor stands in for the worker tunnel on the gRPC path.
//
// A frontend no longer dials a backend process: it opens a stream on the
// worker's tunnel and names the process by its worker-local address. These
// suites run the process on loopback, so a TCP dial to that address is the
// stand-in, and NewTunnelClientFactory below is what makes the specs go through
// a dialer at all rather than through the direct dial the default factory now
// refuses.
//
// The refusal is translated, and that is the load-bearing half. A real worker
// whose backend process has died answers the stream with
// ErrStreamTargetUnavailable, which cluster.IsWorkerAnswer reads as the WORKER
// speaking about its backend, and every reap guard in core/services/nodes acts
// only on that. A bare ECONNREFUSED from net.Dialer carries no such thing and
// reaches those guards as "no route", which reaps nothing. A double that
// reported the raw syscall error could therefore never fail the way production
// fails, and the stale-record spec in router_tracking_test.go would be
// asserting against a transport that cannot produce the condition it is about.
// That is not a hypothetical: replacing this translation with the raw error
// reddens exactly that spec and nothing else.
func workerBackendDialerFor(_ string) func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", clustersvc.ErrStreamTargetUnavailable, err)
		}
		return conn, nil
	}
}

// tunnelBackendClients is the BackendClientFactory a SmartRouter needs in these
// suites.
//
// It is NewTunnelClientFactory and not a bespoke double on purpose: the default
// factory refuses every request now (see nodes.ErrNoWorkerDialer), so a spec
// that omitted this would fail at the first inference with a boot-time
// misconfiguration rather than testing anything, and a bespoke factory that
// dialled directly would put back the bypass the phase removed.
func tunnelBackendClients() nodes.BackendClientFactory {
	GinkgoHelper()
	factory, err := nodes.NewTunnelClientFactory("", workerBackendDialerFor)
	Expect(err).ToNot(HaveOccurred())
	return factory
}
