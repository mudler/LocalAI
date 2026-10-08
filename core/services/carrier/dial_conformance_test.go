package carrier_test

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dialHarness gives one carrier the same service to dial: a TCP server that
// reads its connection until the other side stops writing, and then answers with
// what it read and closes.
type dialHarness struct {
	// dial is the dial function of the carrier for the node under test.
	dial func(ctx context.Context) (net.Conn, error)
	// unrouted is the same dial for a node that no worker holds.
	unrouted func(ctx context.Context) (net.Conn, error)
	// routesMissing says whether the carrier knows a node that is not connected.
	routesMissing bool
}

// Every carrier of WorkerNetDialerFor must pass these. The dialer serves the
// file stager, the control client and the backend-logs proxy, and each of them
// relies on the same behaviour of the connection that comes back.
var _ = Describe("WorkerNetDialerFor conformance", func() {
	// echoService answers "got:<data>" after the end of the request.
	echoService := func() net.Listener {
		GinkgoHelper()
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = lis.Close() })
		go func() {
			for {
				c, err := lis.Accept()
				if err != nil {
					return
				}
				go func() {
					defer func() { _ = c.Close() }()
					data, _ := io.ReadAll(c)
					_, _ = c.Write([]byte("got:" + string(data)))
				}()
			}
		}()
		return lis
	}

	harnesses := map[string]func() dialHarness{
		"the direct dialer of the NATS carrier": func() dialHarness {
			lis := echoService()
			set, err := carrier.NewNATSSet(carrier.NATSOptions{
				Client: &closingBus{FakeBus: testutil.NewFakeBus()}, Registry: noModels{},
				HTTPAddrFor: func(string) (string, error) { return lis.Addr().String(), nil },
			})
			Expect(err).ToNot(HaveOccurred())
			dial := set.Dialer("n")
			return dialHarness{
				dial:     func(ctx context.Context) (net.Conn, error) { return dial(ctx, "tcp", lis.Addr().String()) },
				unrouted: func(ctx context.Context) (net.Conn, error) { return dial(ctx, "tcp", "127.0.0.1:1") },
			}
		},
		"the tunnel dialer on the replica that holds the tunnel": func() dialHarness {
			fx := newDialFixture(echoService())
			return dialHarness{dial: fx.dialVia(fx.owner), unrouted: fx.unroutedVia(fx.owner), routesMissing: true}
		},
		"the tunnel dialer through the replica that does not": func() dialHarness {
			fx := newDialFixture(echoService())
			return dialHarness{dial: fx.dialVia(fx.other), unrouted: fx.unroutedVia(fx.other), routesMissing: true}
		},
	}

	for name, build := range harnesses {
		Describe(name, func() {
			var h dialHarness
			BeforeEach(func() { h = build() })

			It("carries the end of the request to the service, and the whole answer back", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				conn, err := h.dial(ctx)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() { _ = conn.Close() })

				_, err = conn.Write([]byte("request"))
				Expect(err).ToNot(HaveOccurred())
				closer, ok := conn.(interface{ CloseWrite() error })
				Expect(ok).To(BeTrue(), "the connection must half-close, or a request that ends with EOF waits for ever")
				Expect(closer.CloseWrite()).To(Succeed())

				Expect(conn.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
				got, err := io.ReadAll(conn)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(got)).To(Equal("got:request"))
			})

			It("honours the deadline that the caller puts on the connection", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				conn, err := h.dial(ctx)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() { _ = conn.Close() })

				// The service answers after the request ends, and this one does not
				// end it.
				Expect(conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))).To(Succeed())
				begun := time.Now()
				_, err = conn.Read(make([]byte, 1))
				var netErr net.Error
				Expect(errors.As(err, &netErr)).To(BeTrue(), "%v", err)
				Expect(netErr.Timeout()).To(BeTrue())
				Expect(time.Since(begun)).To(BeNumerically("<", 5*time.Second))
			})

			It("leaves no deadline armed on the connection it hands back", func() {
				short, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
				defer cancel()
				conn, err := h.dial(short)
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() { _ = conn.Close() })

				time.Sleep(700 * time.Millisecond)
				_, err = conn.Write([]byte("later"))
				Expect(err).ToNot(HaveOccurred(), "the budget of the dial is not the budget of the conversation")
			})

			It("fails a dial whose context is already over, without blaming a route", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := h.dial(ctx)
				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeFalse(), "the caller gave up; no route was tried")
			})

			It("fails a dial that finds nothing", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err := h.unrouted(ctx)
				Expect(err).To(HaveOccurred())
				if h.routesMissing {
					Expect(errors.Is(err, nodes.ErrNoRoute)).To(BeTrue(), "a node that no worker holds has no route: %v", err)
				}
			})
		})
	}
})

// dialFixture is two replicas, one of which holds the tunnel of a worker whose
// http service is the echo service.
type dialFixture struct {
	owner, other *replica
	nodeID       string
}

func newDialFixture(service net.Listener) *dialFixture {
	GinkgoHelper()
	ctx := context.Background()
	db, dsn := testutil.SetupTestDBWithDSN()
	Expect(cluster.Migrate(ctx, db)).To(Succeed())
	return buildDialFixture(ctx, db, dsn, service)
}

func buildDialFixture(ctx context.Context, db *gorm.DB, dsn string, service net.Listener) *dialFixture {
	GinkgoHelper()
	fx := &dialFixture{}
	clusterR := cluster.NewRegistry(db)
	nodeReg, err := nodes.NewNodeRegistry(db)
	Expect(err).ToNot(HaveOccurred())
	node := &nodes.BackendNode{Name: "w1", NodeType: nodes.NodeTypeBackend, TokenHash: hashOf(registrationToken)}
	Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
	fx.nodeID = node.ID
	Expect(nodeReg.SetTunnelTokenHash(ctx, node.ID, hashOf(workerToken))).To(Succeed())

	fx.owner = startReplica(ctx, "replica-a", db, dsn, clusterR, nodeReg)
	fx.other = startReplica(ctx, "replica-b", db, dsn, clusterR, nodeReg)

	stop := dialInferenceOnly(fx.owner.url, fx.nodeID, workerToken, service.Addr().String())
	DeferCleanup(stop)
	Eventually(func() bool { return fx.owner.tunnels.Holds(fx.nodeID) }, "15s", "50ms").Should(BeTrue())
	return fx
}

func (fx *dialFixture) dialVia(r *replica) func(context.Context) (net.Conn, error) {
	dial := r.set.Dialer(fx.nodeID)
	return func(ctx context.Context) (net.Conn, error) { return dial(ctx, "tcp", fx.nodeID+".worker.invalid:80") }
}

func (fx *dialFixture) unroutedVia(r *replica) func(context.Context) (net.Conn, error) {
	dial := r.set.Dialer("a-node-that-no-worker-holds")
	return func(ctx context.Context) (net.Conn, error) { return dial(ctx, "tcp", "nowhere.worker.invalid:80") }
}

var _ = Describe("Forgetting a node", func() {
	It("is offered by the NATS set for its HTTP stager, and not by the one for shared storage", func() {
		natsSet, err := carrier.NewNATSSet(carrier.NATSOptions{
			Client: &closingBus{FakeBus: testutil.NewFakeBus()}, Registry: noModels{},
			HTTPAddrFor: func(string) (string, error) { return "127.0.0.1:1", nil },
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(natsSet.ForgetNode).ToNot(BeNil())
		natsSet.ForgetNode("n")

		s3Set, err := carrier.NewNATSSet(carrier.NATSOptions{
			Client: &closingBus{FakeBus: testutil.NewFakeBus()}, Registry: noModels{}, S3Staging: true,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(s3Set.ForgetNode).To(BeNil(), "the shared-storage stager keeps no client for each node")
	})
})
