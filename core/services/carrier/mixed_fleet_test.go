package carrier_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	ggrpc "google.golang.org/grpc"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A frontend reaches a NATS worker, a websocket worker and a P2P worker at the
// same time, each on its own route, through one set of holders. Nothing in the
// holders names a carrier: the route table reads the routes of each node from
// what the worker reported (nodes.RoutesOf).
var _ = Describe("A mixed fleet through the route table", func() {
	var (
		ctx      context.Context
		nodeReg  *nodes.NodeRegistry
		a        *replica
		grpcAddr string
		httpAddr string
		control  *controlPlane
		bus      *testutil.FakeBus
		natsOps  atomic.Int32
		table    *carrier.RouteTable
	)

	register := func(name, address, peerID string) string {
		GinkgoHelper()
		n := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeBackend, Address: address, PeerID: peerID, TokenHash: hashOf(registrationToken)}
		Expect(nodeReg.Register(ctx, n, true)).To(Succeed())
		return n.ID
	}

	report := func(nodeID string, c string, t nodes.TunnelRoute) {
		GinkgoHelper()
		Expect(nodeReg.SetRouteReport(ctx, nodeID, nodes.RouteReport{
			Attached: []string{c}, Follow: []string{c}, Tunnel: t,
		})).To(Succeed())
	}

	services := func() map[string]worker.LocalService {
		return map[string]worker.LocalService{
			tunnel.StreamTagGRPC: func(ctx context.Context, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", grpcAddr)
			},
			tunnel.StreamTagHTTP: func(ctx context.Context, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", httpAddr)
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR := cluster.NewRegistry(db)
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		// One backend and one control plane serve every worker; the route that
		// carried each call is what the spec looks at.
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		srv := ggrpc.NewServer()
		pb.RegisterBackendServer(srv, &fakeBackend{loading: make(chan string, 1)})
		go func() { _ = srv.Serve(lis) }()
		DeferCleanup(srv.Stop)
		grpcAddr = lis.Addr().String()
		control = &controlPlane{address: grpcAddr}
		hl, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		httpAddr = hl.Addr().String()
		Expect(hl.Close()).To(Succeed())
		dir := GinkgoT().TempDir()
		httpSrv, err := nodes.StartFileTransferServerWithControl(httpAddr, dir, dir, dir, registrationToken, 1<<30, nil, nil, control.handler())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { nodes.ShutdownFileTransferServer(httpSrv) })

		a = startReplica(ctx, "replica-a", db, dsn, clusterR, nodeReg)

		// The direct route: NATS for the verbs, and a direct dial of the address.
		bus = testutil.NewFakeBus()
		natsOps.Store(0)
		direct, err := carrier.NewNATSRoutes(carrier.NATSOptions{
			Client: &closingBus{FakeBus: bus}, Registry: nodeReg, Token: registrationToken,
			InstallTimeout: time.Minute, UpgradeTimeout: time.Minute,
			HTTPAddrFor: func(string) (string, error) { return httpAddr, nil },
		})
		Expect(err).ToNot(HaveOccurred())

		table = carrier.NewRouteTable(func(ctx context.Context, nodeID string) ([]nodes.Route, error) {
			n, err := nodeReg.Get(ctx, nodeID)
			if err != nil {
				return nil, err
			}
			return nodes.RoutesOf(n), nil
		})
		table.Pin(a.set)
		table.Pin(direct)
	})

	It("reaches a NATS worker, a websocket worker and a P2P worker at once, each on its own route", func() {
		// The NATS worker: it advertises an address and answers on the bus.
		natsID := register("nats-w", grpcAddr, "")
		report(natsID, nodes.RouteNATS, nodes.TunnelRoute{})
		_, err := bus.SubscribeReply(messaging.SubjectNodeModelOp(natsID), func(data []byte, reply func([]byte)) {
			var req workerctl.OperationRequest
			_ = json.Unmarshal(data, &req)
			natsOps.Add(1)
			out, _ := json.Marshal(workerctl.OperationReply{Renewed: req.Renew})
			reply(out)
		})
		Expect(err).ToNot(HaveOccurred())

		// The websocket worker: no address, a tunnel to the replica.
		wsID := register("ws-w", "", "")
		Expect(nodeReg.SetTunnelTokenHash(ctx, wsID, hashOf(workerToken))).To(Succeed())
		report(wsID, nodes.RouteTunnelID, nodes.TunnelRoute{Transport: nodes.TunnelWebSocket})
		ws, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: a.url, NodeID: wsID, Token: func() string { return workerToken }, Services: services(),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(ws.Close)

		// The P2P worker: pinned to a peer, its lanes come through another door
		// than the websocket and land in the same tunnel registry. The admission
		// of the P2P door has its own specs; here its stream is a pipe.
		p2pID := register("p2p-w", "", "12D3KooWExamplePeerOfTheP2PWorker")
		report(p2pID, nodes.RouteTunnelID, nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "direct"})
		p2p, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: a.url, NodeID: p2pID, Token: func() string { return workerToken }, Services: services(),
			Dial: func(ctx context.Context, lane tunnel.Lane, nodeID, _ string) (net.Conn, error) {
				front, back := net.Pipe()
				go func() {
					sess, err := tunnel.ServerSessionConn(front, lane)
					if err != nil {
						_ = front.Close()
						return
					}
					if _, err := a.tunnels.Attach(ctx, nodeID, lane, sess); err != nil {
						_ = sess.Close()
					}
				}()
				return back, nil
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(p2p.Close)

		Eventually(func() bool { return a.tunnels.Holds(wsID) && a.tunnels.Holds(p2pID) }, "15s", "50ms").Should(BeTrue())
		for id, want := range map[string]nodes.RouteKind{natsID: nodes.RouteDirect, wsID: nodes.RouteTunnel, p2pID: nodes.RouteTunnel} {
			n, err := nodeReg.Get(ctx, id)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes.RoutesOf(n)).To(ConsistOf(HaveField("Kind", want)), n.Name)
		}

		commands, clients := carrier.NewCommands(table), carrier.NewClients(table)
		for _, id := range []string{natsID, wsID, p2pID} {
			reply, err := commands.OperationControl(id, workerctl.OperationRequest{Renew: []string{"load-" + id}})
			Expect(err).ToNot(HaveOccurred(), id)
			Expect(reply.Renewed).To(ConsistOf("load-"+id), id)

			out, err := clients.NewClient(id, grpcAddr, false).Predict(ctx, &pb.PredictOptions{Prompt: "hi"})
			Expect(err).ToNot(HaveOccurred(), id)
			Expect(string(out.Message)).To(Equal("echo: hi"), id)
		}
		Expect(natsOps.Load()).To(BeEquivalentTo(1), "only the NATS worker is asked on the bus")
		Expect(control.seenRenewals()).To(Equal(2), "the websocket and the P2P worker are asked through their tunnels")
		Expect(bus.PublishCount(messaging.SubjectNodeModelOp(wsID))+bus.PublishCount(messaging.SubjectNodeModelOp(p2pID))).To(BeZero(),
			"a tunnel worker never talks to NATS")
	})

	It("does not reach a P2P worker whose tunnel is gone, and never sends it to NATS", func() {
		p2pID := register("p2p-gone", "", "12D3KooWExamplePeerOfTheP2PWorker")
		report(p2pID, nodes.RouteTunnelID, nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "relayed", Limited: true})
		_, err := carrier.NewCommands(table).OperationControl(p2pID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(err).To(MatchError(nodes.ErrNoRoute))
		Expect(natsOps.Load()).To(BeZero())
	})
})

// The same fleet with workers that run the code of a real worker: the plane,
// the route holder, the registration that asks for one route at a time, and the
// heartbeat that reports the routes. Nothing is reported by hand. The NATS
// route needs a NATS server, which testcontainers starts.
var _ = Describe("A mixed fleet of real workers", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		nodeReg  *nodes.NodeRegistry
		a        *replica
		front    string
		natsURL  string
		commands *carrier.Commands
		clients  *carrier.Clients
	)

	type realWorker struct {
		nodeID   string
		grpcAddr string
		held     *worker.RouteHolder
		mu       sync.Mutex
		ops      map[worker.Route]int
	}
	opsOn := func(w *realWorker, r worker.Route) int {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.ops[r]
	}

	// start runs a worker the way worker.Run does, with the routes given and,
	// for NATS, the server at nats.
	start := func(name string, routes []worker.Route, nats string) *realWorker {
		GinkgoHelper()
		w := &realWorker{ops: map[worker.Route]int{}}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		gs := ggrpc.NewServer()
		pb.RegisterBackendServer(gs, &fakeBackend{loading: make(chan string, 1)})
		go func() { _ = gs.Serve(lis) }()
		DeferCleanup(gs.Stop)
		w.grpcAddr = lis.Addr().String()
		hl, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		httpAddr := hl.Addr().String()
		Expect(hl.Close()).To(Succeed())

		holdsNATS := slices.Contains(routes, worker.RouteNATS)
		client := &workerregistry.RegistrationClient{FrontendURL: front, RegistrationToken: registrationToken}
		body := func(rs ...worker.Route) map[string]any {
			b := map[string]any{"name": name, "token": registrationToken, "routable": holdsNATS, "routes": rs}
			if holdsNATS {
				b["address"], b["http_address"] = w.grpcAddr, httpAddr
			}
			return b
		}
		res, err := workerregistry.NewCredentialManager(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			return client.RegisterFull(ctx, body())
		}, false).Acquire(ctx)
		Expect(err).ToNot(HaveOccurred())
		w.nodeID = res.ID

		cfg := &worker.Config{RegisterTo: front, RegistrationToken: registrationToken, ServeAddr: w.grpcAddr, NatsURL: nats}
		plane := worker.NewBackendPlane(cfg, w.nodeID, httpAddr, worker.NATSLocal{URL: nats})
		plane.Serve(func(v worker.VerbServer) error {
			return v.Handle(workerctl.VerbModelOp, func(_ context.Context, raw []byte) (any, error) {
				var req workerctl.OperationRequest
				_ = json.Unmarshal(raw, &req)
				w.mu.Lock()
				w.ops[v.Route()]++
				w.mu.Unlock()
				return workerctl.OperationReply{Renewed: req.Renew}, nil
			})
		})
		dir := GinkgoT().TempDir()
		srv, err := nodes.StartFileTransferServerWithControl(httpAddr, dir, dir, dir, registrationToken, 1<<30, nil, nil, plane.Control())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { nodes.ShutdownFileTransferServer(srv) })

		w.held = worker.NewRouteHolder(worker.RouteHolderOptions{
			Routes:    routes,
			Attachers: plane.Attachers(),
			Credentials: func(r worker.Route) *workerregistry.CredentialManager {
				return workerregistry.NewCredentialManagerFor(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
					return client.RegisterFull(ctx, body(r))
				}, false, string(r))
			},
			Heartbeat: func(ctx context.Context, b map[string]any) error { return client.SendHeartbeat(ctx, w.nodeID, b) },
			Interval:  200 * time.Millisecond,
		})
		go w.held.Run(ctx)
		DeferCleanup(w.held.Close)
		return w
	}

	routesOf := func(nodeID string) []nodes.Route {
		n, err := nodeReg.Get(ctx, nodeID)
		if err != nil {
			return nil
		}
		return nodes.RoutesOf(n)
	}

	BeforeEach(func() {
		var err error
		natsURL, err = startTestNATS()
		if err != nil {
			if os.Getenv("CI") != "" && runtime.GOOS != "darwin" {
				Fail("testcontainers requires Docker and CI is set: " + err.Error())
			}
			Skip("testcontainers requires Docker: " + err.Error())
		}
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		db, dsn := testutil.SetupTestDBWithDSN()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR := cluster.NewRegistry(db)
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		a = startReplica(ctx, "replica-a", db, dsn, clusterR, nodeReg)

		// The routes a worker registers, heartbeats and dials its tunnel on, as
		// the application mounts them.
		e := echo.New()
		e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(nodeReg, a.tunnels))
		opts := []localai.RegisterOption{localai.WithTunnelDisconnector(a.tunnels), localai.WithNATSHandover(testHandover{url: natsURL})}
		e.POST("/api/node/register", localai.RegisterNodeEndpoint(nodeReg, registrationToken, true, nil, "", natsauth.Config{}, opts...))
		e.POST("/api/node/:id/heartbeat", localai.HeartbeatEndpoint(nodeReg, opts...))
		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)
		front = srv.URL

		bus, err := messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(bus.Close)
		Eventually(bus.IsConnected, "10s").Should(BeTrue())
		direct, err := carrier.NewNATSRoutes(carrier.NATSOptions{
			Client: bus, Registry: nodeReg, Token: registrationToken,
			InstallTimeout: time.Minute, UpgradeTimeout: time.Minute,
		})
		Expect(err).ToNot(HaveOccurred())
		table := carrier.NewRouteTable(func(ctx context.Context, nodeID string) ([]nodes.Route, error) {
			n, err := nodeReg.Get(ctx, nodeID)
			if err != nil {
				return nil, err
			}
			return nodes.RoutesOf(n), nil
		})
		table.Pin(a.set)
		table.Pin(direct)
		commands, clients = carrier.NewCommands(table), carrier.NewClients(table)
	})

	It("reaches a NATS worker, a websocket worker and a worker that holds both, each on a route it holds", func() {
		natsOnly := start("nats-only", []worker.Route{worker.RouteNATS}, natsURL)
		wsOnly := start("ws-only", []worker.Route{worker.RouteTunnel}, "")
		both := start("both", []worker.Route{worker.RouteNATS, worker.RouteTunnel}, natsURL)

		Eventually(func() []nodes.Route { return routesOf(natsOnly.nodeID) }, "20s", "50ms").Should(Equal([]nodes.Route{{Kind: nodes.RouteDirect}}))
		Eventually(func() []nodes.Route { return routesOf(wsOnly.nodeID) }, "20s", "50ms").Should(Equal([]nodes.Route{{Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket}}))
		Eventually(func() []nodes.Route { return routesOf(both.nodeID) }, "20s", "50ms").Should(Equal([]nodes.Route{
			{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket},
		}))

		n, err := nodeReg.Get(ctx, natsOnly.nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.TunnelTokenHash).To(BeEmpty(), "a worker that holds no tunnel never asked for a tunnel token")
		n, err = nodeReg.Get(ctx, wsOnly.nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(n.Address).To(BeEmpty(), "a worker on the tunnel only registers no address")

		for _, w := range []*realWorker{natsOnly, wsOnly, both} {
			reply, err := commands.OperationControl(w.nodeID, workerctl.OperationRequest{Renew: []string{"load-" + w.nodeID}})
			Expect(err).ToNot(HaveOccurred(), w.nodeID)
			Expect(reply.Renewed).To(ConsistOf("load-" + w.nodeID))
			out, err := clients.NewClient(w.nodeID, w.grpcAddr, false).Predict(ctx, &pb.PredictOptions{Prompt: "hi"})
			Expect(err).ToNot(HaveOccurred(), w.nodeID)
			Expect(string(out.Message)).To(Equal("echo: hi"))
		}
		Expect(opsOn(natsOnly, worker.RouteNATS)).To(Equal(1))
		Expect(opsOn(wsOnly, worker.RouteTunnel)).To(Equal(1))
		Expect(opsOn(wsOnly, worker.RouteNATS)).To(BeZero(), "a websocket worker never talks to NATS")
		Expect(opsOn(both, worker.RouteNATS)).To(Equal(1), "the direct route comes first for a worker that holds both")
	})

	It("keeps serving on the tunnel while the NATS route of a worker cannot connect, and reports why", func() {
		dead, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		deadURL := "nats://" + dead.Addr().String()
		Expect(dead.Close()).To(Succeed())

		w := start("nats-down", []worker.Route{worker.RouteNATS, worker.RouteTunnel}, deadURL)
		Eventually(func() []nodes.Route { return routesOf(w.nodeID) }, "20s", "50ms").Should(Equal([]nodes.Route{{Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket}}))
		Eventually(func() string {
			n, err := nodeReg.Get(ctx, w.nodeID)
			if err != nil {
				return ""
			}
			return n.FollowError
		}, "20s", "50ms").Should(HavePrefix("nats: "))
		Expect(w.held.Held()).To(Equal([]worker.Route{worker.RouteTunnel}))

		_, err = commands.OperationControl(w.nodeID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(opsOn(w, worker.RouteTunnel)).To(Equal(1))
	})
})
