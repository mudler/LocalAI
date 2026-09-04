package agentworker_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// The agent worker's control plane is exercised over a REAL listener with a
// real http.Client, never against a bare ServeMux.
//
// The bearer check, the 404 for an unmounted verb and the status a failing
// handler answers with are all things the transport carries, and a double that
// calls a handler function directly cannot fail the way production fails: it
// never goes through the mount that applies the check, so a spec written that
// way stays green with the check deleted.

const controlToken = "agent-control-token"

// serveConfig mounts cfg the way the agent worker mounts it, on loopback behind
// the bearer check, and returns the base URL.
func serveConfig(cfg agentworker.Config) string {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	srv, err := nodes.StartControlOnlyServer(lis, controlToken, nil, &nodes.AuthenticatedRoutes{
		Prefix:   workerctl.Prefix,
		Register: cfg.Register,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { nodes.ShutdownFileTransferServer(srv) })
	return "http://" + lis.Addr().String()
}

// post issues one control request with the bearer token.
func post(base, path, body string) *http.Response {
	GinkgoHelper()
	return postWithToken(base, path, body, controlToken)
}

func postWithToken(base, path, body, token string) *http.Response {
	GinkgoHelper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = resp.Body.Close() })
	return resp
}

func bodyOf(resp *http.Response) string {
	GinkgoHelper()
	raw, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	return string(raw)
}

// echoHandler answers with the bytes it was given, so a spec can tell the
// handler's own answer apart from anything the server invented.
func echoHandler(reply string) agentworker.UnaryHandler {
	return func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(reply), nil
	}
}

// failingHandler is a verb this worker could not serve.
func failingHandler(err error) agentworker.UnaryHandler {
	return func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, err }
}

// fullConfig serves every verb this task implements.
func fullConfig() agentworker.Config {
	return agentworker.Config{
		MCPTool:      echoHandler(`{"result":"tool-ran"}`),
		MCPDiscovery: echoHandler(`{"servers":[]}`),
		AgentCancel:  echoHandler(`{"cancelled":true}`),
		BackendStop:  func(context.Context, messaging.BackendStopRequest) error { return nil },
	}
}

var _ = Describe("The agent worker's control verbs", func() {
	It("answers each verb with the handler's own bytes, on a 200", func() {
		base := serveConfig(fullConfig())

		tool := post(base, workerctl.PathMCPToolExecute, `{"tool_name":"x"}`)
		Expect(tool.StatusCode).To(Equal(http.StatusOK))
		Expect(bodyOf(tool)).To(Equal(`{"result":"tool-ran"}`))

		disc := post(base, workerctl.PathMCPDiscovery, `{"model_name":"m"}`)
		Expect(disc.StatusCode).To(Equal(http.StatusOK))
		Expect(bodyOf(disc)).To(Equal(`{"servers":[]}`))

		// The cancel is unary and its answer is the worker's own: a 200 body
		// saying whether THIS worker cancelled the run. Anything else the
		// frontend reads as an answer it did not obtain.
		cancel := post(base, workerctl.PathAgentCancel, `{"message_id":"m"}`)
		Expect(cancel.StatusCode).To(Equal(http.StatusOK))
		Expect(bodyOf(cancel)).To(Equal(`{"cancelled":true}`))
	})

	It("answers backend.stop with the 204 a BACKEND worker answers on that same path", func() {
		// One path, two implementations, one caller. The frontend must not have
		// to know which kind of worker it reached to read the answer, which is
		// what lets its carrier split for this verb die.
		stopped := make(chan messaging.BackendStopRequest, 1)
		cfg := fullConfig()
		cfg.BackendStop = func(_ context.Context, req messaging.BackendStopRequest) error {
			stopped <- req
			return nil
		}
		base := serveConfig(cfg)

		resp := post(base, workerctl.PathBackendStop, `{"backend":"llama-cpp","force":true}`)
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		Expect(bodyOf(resp)).To(BeEmpty())
		var got messaging.BackendStopRequest
		Eventually(stopped).Should(Receive(&got))
		// The DTO reaches the handler decoded, so the agent's implementation
		// reads the same request the backend worker's does.
		Expect(got.Backend).To(Equal("llama-cpp"))
		Expect(got.Force).To(BeTrue())
	})

	It("refuses a request with no bearer token", func() {
		base := serveConfig(fullConfig())
		resp := postWithToken(base, workerctl.PathMCPToolExecute, `{}`, "")
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a request with the wrong bearer token", func() {
		// Non-empty and wrong, which a check that merely tested for the
		// header's presence would let through.
		base := serveConfig(fullConfig())
		resp := postWithToken(base, workerctl.PathMCPToolExecute, `{}`, "not-the-token")
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	DescribeTable("answers a verb it does not serve with the catch-all 404",
		// 404 and NOT 500. The frontend reads exactly this status as "this
		// worker does not serve that verb" (nodes.ErrWorkerControlUnsupported)
		// rather than as absence, which is what makes adding a handler later a
		// purely additive change: an older worker and a newer one differ only
		// in which paths are mounted.
		func(path string) {
			base := serveConfig(fullConfig())
			resp := post(base, path, `{}`)
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
			Expect(bodyOf(resp)).To(ContainSubstring("unknown worker control path"))
		},
		Entry("agent execute, whose handler is nil until it is wired", workerctl.PathAgentExecute),
		Entry("mcp ci run, whose handler is nil until it is wired", workerctl.PathMCPCIRun),
		Entry("a backend worker's verb, which an agent worker never serves", workerctl.PathBackendInstall),
		Entry("a path no build has ever named", workerctl.Prefix+"nothing/here"),
	)

	DescribeTable("mounts nothing for a nil handler, so the catch-all answers",
		// The mirror of the table above: it is the NIL FIELD that produces the
		// 404, not the path being unknown to the package.
		func(path string, blank func(*agentworker.Config)) {
			cfg := fullConfig()
			blank(&cfg)
			base := serveConfig(cfg)
			Expect(post(base, path, `{}`).StatusCode).To(Equal(http.StatusNotFound))
		},
		Entry("mcp tool execute", workerctl.PathMCPToolExecute, func(c *agentworker.Config) { c.MCPTool = nil }),
		Entry("mcp discovery", workerctl.PathMCPDiscovery, func(c *agentworker.Config) { c.MCPDiscovery = nil }),
		Entry("backend stop", workerctl.PathBackendStop, func(c *agentworker.Config) { c.BackendStop = nil }),
		Entry("agent cancel", workerctl.PathAgentCancel, func(c *agentworker.Config) { c.AgentCancel = nil }),
	)

	// A rule stated at every verb has to be pinned at every verb. The exit
	// helper is one function, and that is exactly why one spec here would not
	// prove the others use it.
	DescribeTable("answers a handler's failure with a non-2xx and never a 200",
		func(path string, breaks func(*agentworker.Config)) {
			cfg := fullConfig()
			breaks(&cfg)
			base := serveConfig(cfg)

			resp := post(base, path, `{}`)
			// The property, stated as the frontend reads it: a 2xx body is the
			// WORKER'S OWN ANSWER, which a reap guard may act on, and a failure
			// to serve is not an answer about anything. A 200 carrying an error
			// field would put this in the wrong bucket.
			Expect(resp.StatusCode).ToNot(Equal(http.StatusOK))
			Expect(resp.StatusCode).ToNot(Equal(http.StatusNoContent))
			Expect(resp.StatusCode).To(BeNumerically(">=", 400))
			// And not the 404 that means "older worker", which would send the
			// frontend down a version-skew fallback for a transient failure.
			Expect(resp.StatusCode).ToNot(Equal(http.StatusNotFound))
			Expect(bodyOf(resp)).To(ContainSubstring("could not be served"))
		},
		Entry("mcp tool execute", workerctl.PathMCPToolExecute, func(c *agentworker.Config) {
			c.MCPTool = failingHandler(errors.New("the tool plane is down"))
		}),
		Entry("mcp discovery", workerctl.PathMCPDiscovery, func(c *agentworker.Config) {
			c.MCPDiscovery = failingHandler(errors.New("discovery is down"))
		}),
		Entry("backend stop", workerctl.PathBackendStop, func(c *agentworker.Config) {
			c.BackendStop = func(context.Context, messaging.BackendStopRequest) error {
				return errors.New("the session cache is wedged")
			}
		}),
		Entry("agent cancel", workerctl.PathAgentCancel, func(c *agentworker.Config) {
			c.AgentCancel = failingHandler(errors.New("the cancel registry is unreadable"))
		}),
		Entry("agent execute, before it has published anything", workerctl.PathAgentExecute, func(c *agentworker.Config) {
			c.AgentExecute = func(context.Context, json.RawMessage, messaging.Publisher) (json.RawMessage, error) {
				return nil, errors.New("no agent to run")
			}
		}),
	)

	It("refuses a GET on a mounted verb, because a control verb is a command", func() {
		base := serveConfig(fullConfig())
		req, err := http.NewRequest(http.MethodGet, base+workerctl.PathMCPToolExecute, nil)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+controlToken)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	It("refuses a body larger than the shared control cap", func() {
		base := serveConfig(fullConfig())
		oversized := `{"tool_name":"` + strings.Repeat("a", workerctl.MaxRequestBytes) + `"}`
		resp := post(base, workerctl.PathMCPToolExecute, oversized)
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("answers a body the verb could not decode with a non-2xx", func() {
		base := serveConfig(fullConfig())
		resp := post(base, workerctl.PathBackendStop, `{"backend":`)
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("mounts no file-staging routes, because an agent worker stages nothing", func() {
		// StartControlOnlyServer exists so that a worker with no staging
		// directories cannot end up serving upload and download handlers
		// against a path that resolves to its working directory.
		base := serveConfig(fullConfig())
		req, err := http.NewRequest(http.MethodGet, base+"/v1/files/anything", nil)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+controlToken)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})
})

var _ = Describe("The agent worker's streaming verbs", func() {
	// The shape both dispatched verbs answer with. PathAgentExecute and
	// PathMCPCIRun are now set by the CLI's agentWorkerControlHandlers, and the
	// claiming replica reads exactly this: zero or more progress lines followed
	// by exactly one reply line, the reply last, which is what lets it persist
	// the terminal state before it releases the claim.

	// lines reads an NDJSON body into its envelopes.
	lines := func(resp *http.Response) []workerctl.Envelope {
		GinkgoHelper()
		var out []workerctl.Envelope
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "" {
				continue
			}
			var env workerctl.Envelope
			Expect(json.Unmarshal(sc.Bytes(), &env)).To(Succeed())
			out = append(out, env)
		}
		Expect(sc.Err()).ToNot(HaveOccurred())
		return out
	}

	It("puts every progress line before the one reply, and the reply last", func() {
		cfg := fullConfig()
		cfg.AgentExecute = func(_ context.Context, raw json.RawMessage, pub messaging.Publisher) (json.RawMessage, error) {
			Expect(pub.Publish("agent.progress", map[string]string{"step": "one"})).To(Succeed())
			Expect(pub.Publish("agent.progress", map[string]string{"step": "two"})).To(Succeed())
			return json.RawMessage(`{"done":true}`), nil
		}
		base := serveConfig(cfg)

		resp := post(base, workerctl.PathAgentExecute, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(Equal(workerctl.ContentTypeStream))

		got := lines(resp)
		Expect(got).To(HaveLen(3))
		Expect(string(got[0].Progress)).To(ContainSubstring(`"one"`))
		Expect(string(got[1].Progress)).To(ContainSubstring(`"two"`))
		// The contract the caller stops reading on: exactly one reply, and it
		// is the last thing on the body.
		Expect(got[2].Reply).ToNot(BeEmpty())
		Expect(string(got[2].Reply)).To(Equal(`{"done":true}`))
	})

	It("drops a progress line published after the reply, rather than appending it", func() {
		// Without this the reply is not the last line, and a caller that stops
		// reading at the reply leaves bytes in the stream that the next read on
		// a pooled connection would see.
		late := make(chan error, 1)
		cfg := fullConfig()
		cfg.MCPCIRun = func(_ context.Context, _ json.RawMessage, pub messaging.Publisher) (json.RawMessage, error) {
			go func() {
				// Published from another goroutine, which is what a handler
				// with a debounce timer does, and what the mutex is for.
				<-late
				_ = pub.Publish("job.progress", map[string]string{"step": "too-late"})
				late <- nil
			}()
			return json.RawMessage(`{"status":"completed"}`), nil
		}
		base := serveConfig(cfg)

		resp := post(base, workerctl.PathMCPCIRun, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		got := lines(resp)
		// The body ended after the reply, so the late publish had nowhere to
		// go. Released only now, so the ordering is scripted rather than raced.
		late <- nil
		Eventually(late).Should(Receive())
		Expect(got).To(HaveLen(1))
		Expect(string(got[0].Reply)).To(Equal(`{"status":"completed"}`))
	})

	It("ends the body with NO reply line when a handler fails after streaming", func() {
		// The status is already on the wire by then, so the failure cannot be a
		// non-2xx. It must also not be a reply, because a reply is the worker's
		// own answer. A body that ends without one is "this worker did not
		// answer", which is the only honest thing left to say.
		cfg := fullConfig()
		cfg.AgentExecute = func(_ context.Context, _ json.RawMessage, pub messaging.Publisher) (json.RawMessage, error) {
			Expect(pub.Publish("agent.progress", map[string]string{"step": "started"})).To(Succeed())
			return nil, errors.New("the agent died mid-run")
		}
		base := serveConfig(cfg)

		resp := post(base, workerctl.PathAgentExecute, `{}`)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		got := lines(resp)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Reply).To(BeEmpty())
		Expect(string(got[0].Progress)).To(ContainSubstring("started"))
	})
})

// fakeFrontend is the far side of an agent worker's tunnel: the real WebSocket
// upgrade and the real yamux server handshake, so these specs exercise the
// wire rather than a mock of it.
type fakeFrontend struct {
	srv      *httptest.Server
	sessions chan *yamux.Session
}

func newFakeFrontend() *fakeFrontend {
	f := &fakeFrontend{sessions: make(chan *yamux.Session, 8)}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cluster.ConnectPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := yamux.Server(cluster.WebsocketConn(ws), nil, nil)
		if err != nil {
			_ = ws.Close()
			return
		}
		select {
		case f.sessions <- sess:
		default:
			_ = sess.Close()
		}
	}))
	return f
}

func (f *fakeFrontend) close() {
	for {
		select {
		case sess := <-f.sessions:
			_ = sess.Close()
		default:
			f.srv.Close()
			return
		}
	}
}

// awaitErr runs fn on its own goroutine and reports its result on a channel.
//
// Every blocking read below goes through it. Asserting "the worker refused this
// stream" with a read deadline is satisfied just as well by a stream the worker
// never answered at all; reading with NO deadline and requiring the channel to
// deliver inverts that.
func awaitErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

var _ = Describe("An agent worker on the tunnel", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		frontend *fakeFrontend
		rt       *agentworker.Runtime
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		frontend = newFakeFrontend()
	})

	AfterEach(func() {
		if rt != nil {
			Expect(rt.Close()).To(Succeed())
			rt = nil
		}
		cancel()
		frontend.close()
	})

	start := func(url string) {
		GinkgoHelper()
		var err error
		rt, err = agentworker.Start(ctx, agentworker.Options{
			FrontendURL:  url,
			NodeID:       "agent-node-1",
			TunnelToken:  func() string { return "tunnel-secret" },
			ControlToken: controlToken,
			Handlers:     fullConfig(),
		})
		Expect(err).ToNot(HaveOccurred())
	}

	session := func() *yamux.Session {
		GinkgoHelper()
		var sess *yamux.Session
		Eventually(frontend.sessions, "10s").Should(Receive(&sess))
		return sess
	}

	It("binds only loopback, so it opens no inbound port", func() {
		start(frontend.srv.URL)
		host, _, err := net.SplitHostPort(rt.Addr())
		Expect(err).ToNot(HaveOccurred())
		Expect(host).To(Equal("127.0.0.1"))
	})

	It("serves its control verbs through the tunnel and nothing else", func() {
		start(frontend.srv.URL)
		sess := session()

		// The http tag, which is the one an agent worker offers. Driven as a
		// real HTTP request over a real yamux stream, so what this proves is
		// that the frontend can reach the verbs, not that a map has a key.
		stream, err := sess.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagHTTP, "ignored:1")).To(Succeed())
		accepted := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
		Eventually(accepted, "10s").Should(Receive(BeNil()))

		req, err := http.NewRequest(http.MethodPost, "http://agent-node-1"+workerctl.PathMCPToolExecute,
			strings.NewReader(`{"tool_name":"x"}`))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+controlToken)
		Expect(req.Write(stream)).To(Succeed())

		read := make(chan *http.Response, 1)
		go func() {
			resp, rerr := http.ReadResponse(bufio.NewReader(stream), req)
			if rerr == nil {
				read <- resp
			}
			close(read)
		}()
		var resp *http.Response
		Eventually(read, "10s").Should(Receive(&resp))
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(Equal(`{"result":"tool-ran"}`))
	})

	It("refuses a stream tagged for gRPC, which it runs nothing to serve", func() {
		start(frontend.srv.URL)
		sess := session()

		stream, err := sess.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:50051")).To(Succeed())

		reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
		var got error
		Eventually(reply, "10s").Should(Receive(&got))
		// The refusal names the tag, which tells a frontend a retry is
		// pointless, rather than "unavailable", which tells it to retry
		// something that can never work on this kind of worker.
		Expect(got).To(MatchError(cluster.ErrStreamTagUnknown))
		Expect(got).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
	})

	It("reports itself not ready while it holds no tunnel session", func() {
		// The gate FAILS OPEN when nothing arms it, so a worker with a dead
		// tunnel would answer 200 forever and nothing in the process would say
		// a word. That is the whole symptom of the arming line going missing.
		start("http://127.0.0.1:1")

		resp, err := http.Get("http://" + rt.Addr() + "/readyz") //nolint:noctx // loopback probe in a spec
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = resp.Body.Close() })
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
		Expect(bodyOf(resp)).To(ContainSubstring("tunnel"))
		Expect(rt.Connected()).To(BeFalse())
	})

	It("reports itself ready once its tunnel is up", func() {
		// The negative control for the spec above: a gate wired to a probe that
		// always failed would satisfy it.
		start(frontend.srv.URL)
		session()

		Eventually(func() int {
			resp, err := http.Get("http://" + rt.Addr() + "/readyz") //nolint:noctx // loopback probe in a spec
			if err != nil {
				return 0
			}
			defer func() { _ = resp.Body.Close() }()
			return resp.StatusCode
		}, "10s").Should(Equal(http.StatusOK))
	})

	It("refuses to start with no routes to serve", func() {
		_, err := nodes.StartControlOnlyServer(mustListen(), controlToken, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no routes"))
	})

	DescribeTable("refuses to start on a route set it cannot mount",
		func(routes *nodes.AuthenticatedRoutes, want string) {
			_, err := nodes.StartControlOnlyServer(mustListen(), controlToken, nil, routes)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(want))
		},
		Entry("no prefix", &nodes.AuthenticatedRoutes{Register: func(*http.ServeMux) {}}, "no prefix"),
		Entry("no registrar", &nodes.AuthenticatedRoutes{Prefix: workerctl.Prefix}, "no registrar"),
	)
})

func mustListen() net.Listener {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = lis.Close() })
	return lis
}
