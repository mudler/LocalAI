package carrier_test

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"time"

	ggrpc "google.golang.org/grpc"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

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

	report := func(nodeID string, c cluster.Carrier, t nodes.TunnelRoute) {
		GinkgoHelper()
		Expect(nodeReg.SetCarrierReport(ctx, nodeID, nodes.CarrierReport{
			Attached: []cluster.Carrier{c}, Follow: []cluster.Carrier{c}, Tunnel: t,
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
		table.Pin(a.set.WorkerRoute)
		table.Attach(direct)
	})

	It("reaches a NATS worker, a websocket worker and a P2P worker at once, each on its own route", func() {
		// The NATS worker: it advertises an address and answers on the bus.
		natsID := register("nats-w", grpcAddr, "")
		report(natsID, cluster.CarrierNATS, nodes.TunnelRoute{})
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
		report(wsID, cluster.CarrierTunnel, nodes.TunnelRoute{Transport: nodes.TunnelWebSocket})
		ws, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: a.url, NodeID: wsID, Token: func() string { return workerToken }, Services: services(),
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(ws.Close)

		// The P2P worker: pinned to a peer, its lanes come through another door
		// than the websocket and land in the same tunnel registry. The admission
		// of the P2P door has its own specs; here its stream is a pipe.
		p2pID := register("p2p-w", "", "12D3KooWExamplePeerOfTheP2PWorker")
		report(p2pID, cluster.CarrierTunnel, nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "direct"})
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
		report(p2pID, cluster.CarrierTunnel, nodes.TunnelRoute{Transport: nodes.TunnelP2P, Path: "relayed", Limited: true})
		_, err := carrier.NewCommands(table).OperationControl(p2pID, workerctl.OperationRequest{Renew: []string{"op"}})
		Expect(err).To(MatchError(nodes.ErrNoRoute))
		Expect(natsOps.Load()).To(BeZero())
	})
})
