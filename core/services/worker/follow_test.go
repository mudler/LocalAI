package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/workerctl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A worker that booted on NATS can be told, while it runs, to attach to a
// tunnel and serve its control verbs over it. These specs run the follower
// against the real connect endpoint, the real registry of tunnels and a real
// PostgreSQL. What decides to attach is not part of them.
var _ = Describe("A worker that attaches to a tunnel at run time", func() {
	const (
		nodeToken         = "own-tunnel-token"
		registrationToken = "registration-token"
	)

	var (
		ctx      context.Context
		registry *tunnel.Registry
		frontend *httptest.Server
		nodeID   string
		httpAddr string
		slot     *nodes.ControlSlot
		follower *tunnelFollower
	)

	freeAddr := func() string {
		GinkgoHelper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := l.Addr().String()
		Expect(l.Close()).To(Succeed())
		return addr
	}

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR := cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		nodeReg, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		registry = tunnel.NewRegistry(clusterR, "replica-a")
		node := &nodes.BackendNode{Name: "w1", NodeType: nodes.NodeTypeBackend}
		Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
		nodeID = node.ID
		sum := sha256.Sum256([]byte(nodeToken))
		Expect(nodeReg.SetTunnelTokenHash(ctx, nodeID, hex.EncodeToString(sum[:]))).To(Succeed())
		e := echo.New()
		e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(nodeReg, registry))
		frontend = httptest.NewServer(e)
		DeferCleanup(frontend.Close)

		// The HTTP server of the worker, started as a worker on NATS starts it: the
		// control plane is a slot with nothing in it.
		httpAddr = freeAddr()
		dir := GinkgoT().TempDir()
		slot = &nodes.ControlSlot{}
		srv, err := nodes.StartFileTransferServerWithControl(httpAddr, dir, dir, dir, registrationToken, 1<<20, nil, nil, slot)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { nodes.ShutdownFileTransferServer(srv) })
		Eventually(func() error {
			c, err := net.DialTimeout("tcp", httpAddr, time.Second)
			if err == nil {
				_ = c.Close()
			}
			return err
		}).Should(Succeed())

		ss := newLifecycleTestSupervisor(make(chan os.Signal, 1))
		follower = newTunnelFollower(tunnelFollowerOptions{
			Config:   &Config{RegisterTo: frontend.URL, ServeAddr: "127.0.0.1:50051"},
			NodeID:   nodeID,
			HTTPAddr: httpAddr,
			Token:    func() string { return nodeToken },
			Slot:     slot,
			Register: ss.registerLifecycleVerbs,
		})
		DeferCleanup(func() { _ = follower.Detach() })
	})

	direct := func() int {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+workerctl.PathOf(workerctl.VerbBackendList), strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+registrationToken)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	// viaTunnel sends the control verb the way a frontend does: a stream with the
	// http tag, and an HTTP request on it.
	viaTunnel := func(token string) (int, string) {
		GinkgoHelper()
		dial := tunnel.NewWorkerDialer(registry, nil).DialerFor(nodeID, tunnel.StreamTagHTTP)
		client := &http.Client{Transport: &http.Transport{DialContext: dial}, Timeout: 10 * time.Second}
		DeferCleanup(client.CloseIdleConnections)
		req, err := http.NewRequest(http.MethodPost, "http://"+nodeID+".worker.invalid"+workerctl.PathOf(workerctl.VerbBackendList), strings.NewReader("{}"))
		Expect(err).ToNot(HaveOccurred())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	It("serves no control path and holds no tunnel until it is told to attach", func() {
		Expect(direct()).To(Equal(http.StatusNotFound), "a worker on NATS answers as it did before")
		Expect(follower.Attached()).To(BeFalse())
		Expect(registry.Holds(nodeID)).To(BeFalse())
	})

	It("holds a tunnel and serves its control verbs over it once attached", func() {
		Expect(follower.Attach(ctx)).To(Succeed())
		Expect(follower.Attached()).To(BeTrue())
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeTrue())

		status, body := viaTunnel(registrationToken)
		Expect(status).To(Equal(http.StatusOK), body)
		Expect(direct()).To(Equal(http.StatusOK))
	})

	It("refuses a control request over the tunnel that carries no token", func() {
		Expect(follower.Attach(ctx)).To(Succeed())
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeTrue())
		status, _ := viaTunnel("")
		Expect(status).To(Equal(http.StatusUnauthorized))
	})

	It("is idempotent: a second attach changes nothing", func() {
		Expect(follower.Attach(ctx)).To(Succeed())
		Expect(follower.Attach(ctx)).To(Succeed())
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeTrue())
	})

	It("lets go of the tunnel and the control path on detach, and can attach again", func() {
		Expect(follower.Attach(ctx)).To(Succeed())
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeTrue())

		Expect(follower.Detach()).To(Succeed())
		Expect(follower.Attached()).To(BeFalse())
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeFalse())
		Expect(direct()).To(Equal(http.StatusNotFound))

		Expect(follower.Attach(ctx)).To(Succeed(), "the verbs are claimed once and not twice")
		Eventually(func() bool { return registry.Holds(nodeID) }, "15s").Should(BeTrue())
		status, _ := viaTunnel(registrationToken)
		Expect(status).To(Equal(http.StatusOK))
	})

	It("refuses to attach a worker that has no frontend to attach to", func() {
		f := newTunnelFollower(tunnelFollowerOptions{Config: &Config{}, NodeID: nodeID, HTTPAddr: httpAddr, Token: func() string { return "" }, Slot: slot, Register: func(controlServer) error { return nil }})
		Expect(f.CanAttach()).To(BeFalse())
		Expect(f.Attach(ctx)).To(HaveOccurred())
		Expect(f.Attached()).To(BeFalse())
	})
})
