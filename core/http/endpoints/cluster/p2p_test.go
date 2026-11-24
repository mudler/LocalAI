package cluster_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/worker"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newP2PHost starts a host on loopback with an allowlist gate.
func newP2PHost(gate *p2pnet.Allowlist) *p2pnet.Host {
	GinkgoHelper()
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         gate,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

var _ = Describe("The tunnel over P2P", func() {
	const nodeToken = "own-tunnel-token-of-the-p2p-worker"

	var (
		ctx      context.Context
		nodeReg  *nodes.NodeRegistry
		tunnels  *tunnel.Registry
		frontend *p2pnet.Host
		workerH  *p2pnet.Host
		addrs    []p2pnet.Multiaddr
	)

	// register adds a node pinned to peer, with its tunnel credential.
	register := func(name, peer string, approve bool) *nodes.BackendNode {
		GinkgoHelper()
		node := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeBackend, PeerID: peer}
		Expect(nodeReg.Register(ctx, node, approve)).To(Succeed())
		Expect(nodeReg.SetTunnelTokenHash(ctx, node.ID, hashOf(nodeToken))).To(Succeed())
		return node
	}

	// dial opens the stream of a lane from the worker and runs the hello, as a
	// worker that joined with an invite does.
	dial := func(lane tunnel.Lane, nodeID, token string) (net.Conn, tunnel.HelloReply) {
		GinkgoHelper()
		proto := p2pnet.ProtoTunnelInference
		if lane == tunnel.LaneBulk {
			proto = p2pnet.ProtoTunnelBulk
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conn, err := workerH.Dial(dctx, frontend.ID(), proto, p2pnet.WithAddrs(addrs...))
		Expect(err).ToNot(HaveOccurred())
		Expect(tunnel.WriteHello(conn, tunnel.Hello{V: tunnel.HelloVersion, NodeID: nodeID, Lane: lane, Token: token})).To(Succeed())
		reply, err := tunnel.ReadHelloReply(conn)
		Expect(err).ToNot(HaveOccurred())
		return conn, reply
	}

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR := cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test")).To(Succeed())
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		tunnels = tunnel.NewRegistry(clusterR, "replica-a")

		gate := p2pnet.NewAllowlist()
		frontend = newP2PHost(gate)
		workerH = newP2PHost(p2pnet.NewAllowlist())
		gate.Allow(workerH.ID())
		addrs = frontend.Addrs()

		srv := clusterapi.P2PTunnelServer{Registry: nodeReg, Tunnels: tunnels}
		for proto, lane := range map[p2pnet.Protocol]tunnel.Lane{
			p2pnet.ProtoTunnelInference: tunnel.LaneInference,
			p2pnet.ProtoTunnelBulk:      tunnel.LaneBulk,
		} {
			ln, err := frontend.Listen(proto, nil)
			Expect(err).ToNot(HaveOccurred())
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					go srv.ServeConn(ctx, c, lane)
				}
			}()
		}
	})

	It("attaches the session of a worker pinned to the peer that dials, and carries a stream", func() {
		node := register("p2p-w1", workerH.ID().String(), true)
		conn, reply := dial(tunnel.LaneInference, node.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusOK))
		sess, err := tunnel.ClientSessionConn(conn, tunnel.LaneInference)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(sess.Close)
		Eventually(func() bool { return tunnels.Holds(node.ID) }).Should(BeTrue())

		go func() {
			for {
				st, err := sess.AcceptStream()
				if err != nil {
					return
				}
				go func() { defer func() { _ = st.Close() }(); _, _ = io.Copy(st, st) }()
			}
		}()
		st, err := tunnels.Open(ctx, node.ID, tunnel.LaneInference)
		Expect(err).ToNot(HaveOccurred())
		_, err = st.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		got := make([]byte, 4)
		_, err = io.ReadFull(st, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("ping"))

		// The bulk lane goes to the replica that holds the inference lane.
		bulk, reply := dial(tunnel.LaneBulk, node.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusOK))
		bsess, err := tunnel.ClientSessionConn(bulk, tunnel.LaneBulk)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bsess.Close)

		Expect(sess.Close()).To(Succeed())
		Eventually(func() bool { return tunnels.Holds(node.ID) }, 5*time.Second).Should(BeFalse())
	})

	It("refuses a node that is pinned to another peer, and a node that is pinned to none", func() {
		other := register("p2p-other", frontend.ID().String(), true)
		_, reply := dial(tunnel.LaneInference, other.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusUnauthorized))

		plain := register("token-node", "", true)
		_, reply = dial(tunnel.LaneInference, plain.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusUnauthorized))
		Expect(tunnels.Holds(other.ID) || tunnels.Holds(plain.ID)).To(BeFalse())
	})

	It("refuses a wrong token, an unknown node and an empty token with 401", func() {
		node := register("p2p-w2", workerH.ID().String(), true)
		_, reply := dial(tunnel.LaneInference, node.ID, "wrong")
		Expect(reply.Status).To(Equal(http.StatusUnauthorized))
		_, reply = dial(tunnel.LaneInference, "nobody", nodeToken)
		Expect(reply.Status).To(Equal(http.StatusUnauthorized))
		_, reply = dial(tunnel.LaneInference, node.ID, "")
		Expect(reply.Status).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a node that waits for approval with 403", func() {
		node := register("p2p-pending", workerH.ID().String(), false)
		Expect(node.Status).To(Equal(nodes.StatusPending))
		_, reply := dial(tunnel.LaneInference, node.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusForbidden))
	})

	It("answers 409 to a bulk dial while no inference lane is held", func() {
		node := register("p2p-w3", workerH.ID().String(), true)
		_, reply := dial(tunnel.LaneBulk, node.ID, nodeToken)
		Expect(reply.Status).To(Equal(http.StatusConflict))
	})

	It("refuses the websocket door to a node that is pinned to a peer", func() {
		node := register("p2p-ws", workerH.ID().String(), true)
		srv := serveConnect(nodeReg, tunnels)
		req, _ := http.NewRequest(http.MethodGet, srv.URL+tunnel.ConnectPath+"?id="+node.ID, nil)
		req.Header.Set("Authorization", "Bearer "+nodeToken)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("lets a worker tunnel connect over P2P, register again on a refusal, and serve a stream", func() {
		node := register("p2p-w4", workerH.ID().String(), true)
		token := "stale"
		w, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: "http://" + frontend.ID().String(),
			NodeID:      node.ID,
			Token:       func() string { return token },
			Services: map[string]worker.LocalService{
				tunnel.StreamTagHTTP: func(context.Context, string) (net.Conn, error) {
					a, b := net.Pipe()
					go func() { defer func() { _ = b.Close() }(); _, _ = io.Copy(b, b) }()
					return a, nil
				},
			},
			Reauthorize: func(context.Context) error {
				token = nodeToken
				return nil
			},
			Dial: func(ctx context.Context, lane tunnel.Lane, nodeID, tok string) (net.Conn, error) {
				conn, reply := dial(lane, nodeID, tok)
				if !reply.Accepted() {
					_ = conn.Close()
					return nil, worker.TunnelRefusal(reply.Status, reply.Error)
				}
				return conn, nil
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(w.Close)
		Eventually(w.Connected, 20*time.Second).Should(BeTrue())
		Eventually(w.BulkConnected, 20*time.Second).Should(BeTrue())

		st, err := tunnels.Open(ctx, node.ID, tunnel.LaneInference)
		Expect(err).ToNot(HaveOccurred())
		Expect(tunnel.WriteStreamRequest(st, tunnel.StreamTagHTTP, "")).To(Succeed())
		Expect(tunnel.ReadStreamReply(st)).To(Succeed())
		_, err = st.Write([]byte("echo"))
		Expect(err).ToNot(HaveOccurred())
		got := make([]byte, 4)
		_, err = io.ReadFull(st, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("echo"))
	})
})

// serveConnect serves the websocket door of the tunnel.
func serveConnect(registry *nodes.NodeRegistry, tunnels *tunnel.Registry) *httptest.Server {
	GinkgoHelper()
	e := echo.New()
	e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(registry, tunnels))
	s := httptest.NewServer(e)
	DeferCleanup(s.Close)
	return s
}
