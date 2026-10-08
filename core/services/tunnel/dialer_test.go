package tunnel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// scriptedWorker answers the request frame of every stream of a session with a
// script, and then echoes what it reads. It stands for the worker side of a
// tunnel; the session under it is a real websocket with a real yamux session.
type scriptedWorker struct {
	mu       sync.Mutex
	requests []workerRequest
	refuse   error
	hold     time.Duration
}

type workerRequest struct{ tag, target string }

func (w *scriptedWorker) serve(sess *tunnel.Session) {
	go func() {
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = st.Close() }()
				tag, target, err := tunnel.ReadStreamRequest(st)
				if err != nil {
					return
				}
				w.mu.Lock()
				w.requests = append(w.requests, workerRequest{tag, target})
				refuse, hold := w.refuse, w.hold
				w.mu.Unlock()
				if hold > 0 {
					time.Sleep(hold)
				}
				if refuse != nil {
					_ = tunnel.WriteStreamRefusal(st, refuse)
					return
				}
				if err := tunnel.WriteStreamAccepted(st); err != nil {
					return
				}
				_, _ = io.Copy(st, st)
			}()
		}
	}()
}

func (w *scriptedWorker) seen() []workerRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]workerRequest(nil), w.requests...)
}

var _ = Describe("The worker dialer", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		clusterR *cluster.Registry
		tunnels  *tunnel.Registry
		worker   *scriptedWorker
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		Expect(clusterR.Register(ctx, "replica-b", "test", 0, "")).To(Succeed())
		tunnels = tunnel.NewRegistry(clusterR, "replica-a")
		worker = &scriptedWorker{}
	})

	// hold attaches a worker to this replica, with a bulk session when asked.
	hold := func(bulk bool) (inference, bulkSess *tunnel.Session) {
		GinkgoHelper()
		front, back := sessionPair(tunnel.LaneInference)
		worker.serve(back)
		_, err := tunnels.Attach(ctx, "w1", tunnel.LaneInference, front)
		Expect(err).ToNot(HaveOccurred())
		if bulk {
			bf, bb := sessionPair(tunnel.LaneBulk)
			worker.serve(bb)
			_, err = tunnels.Attach(ctx, "w1", tunnel.LaneBulk, bf)
			Expect(err).ToNot(HaveOccurred())
			return front, bf
		}
		return front, nil
	}

	roundTrip := func(conn net.Conn, msg string) {
		GinkgoHelper()
		_, err := conn.Write([]byte(msg))
		Expect(err).ToNot(HaveOccurred())
		got := make([]byte, len(msg))
		Expect(conn.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, err = io.ReadFull(conn, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal(msg))
	}

	Describe("when this replica holds the tunnel", func() {
		It("opens a stream down it, names the service, and hands back a conn that carries the protocol", func() {
			hold(false)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)

			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "hello")
			Expect(worker.seen()).To(Equal([]workerRequest{{tunnel.StreamTagGRPC, "10.0.0.5:50051"}}))
		})

		It("leaves no deadline on the conn, so that a quiet inference is not cut", func() {
			hold(false)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			shortCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()
			conn, err := dialer.Dial(shortCtx, "w1", tunnel.StreamTagHTTP, "")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })

			time.Sleep(500 * time.Millisecond)
			roundTrip(conn, "still here")
		})

		It("reports a refusal of the worker as the refusal of the worker, with no ErrNoRoute and no absence", func() {
			hold(false)
			worker.refuse = tunnel.ErrStreamTargetUnavailable
			dialer := tunnel.NewWorkerDialer(tunnels, nil)

			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(err).To(HaveOccurred())
			Expect(tunnel.IsWorkerAnswer(err)).To(BeTrue())
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "a worker that answers is present")
			Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse())
		})

		It("reports that the worker learned nothing as ErrNoRoute and not as evidence", func() {
			hold(false)
			worker.refuse = tunnel.ErrStreamNotServed
			dialer := tunnel.NewWorkerDialer(tunnels, nil)

			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
		})

		It("blames the budget of the caller and not the worker when the handshake ends on its deadline", func() {
			hold(false)
			worker.hold = 2 * time.Second
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()

			_, err := dialer.Dial(short, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "an expired budget is not a missing route")
			Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
		})

		It("reports a session that ended under a held entry as a route that does not exist now", func() {
			front, _ := hold(false)
			Expect(front.Close()).To(Succeed())
			dialer := tunnel.NewWorkerDialer(tunnels, nil)

			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse(), "the tunnel is held here; looking elsewhere would come back to this replica")
		})
	})

	Describe("the bulk lane", func() {
		It("carries a stream on the bulk session when the dial asks for it", func() {
			hold(true)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagHTTP, "", tunnel.WithBulkLane())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "payload")
		})

		It("fails, and does not use the inference lane, when the worker has no bulk session", func() {
			hold(false)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagHTTP, "", tunnel.WithBulkLane())
			Expect(errors.Is(err, tunnel.ErrNoBulkSession)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "a lane that is down must not make a scheduler demote a worker whose model calls work")
			Expect(worker.seen()).To(BeEmpty(), "nothing may have crossed the inference lane")
		})
	})

	Describe("when another replica holds the tunnel", func() {
		// The other replica is real: a registry of its own, a relay, and a worker
		// behind it. The peer link between the two is a pair of pipes.
		var (
			other *tunnel.Registry
			peers *fakePeers
		)

		BeforeEach(func() {
			other = tunnel.NewRegistry(clusterR, "replica-b")
			front, back := sessionPair(tunnel.LaneInference)
			worker.serve(back)
			_, err := other.Attach(ctx, "w1", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())
			bf, bb := sessionPair(tunnel.LaneBulk)
			worker.serve(bb)
			_, err = other.Attach(ctx, "w1", tunnel.LaneBulk, bf)
			Expect(err).ToNot(HaveOccurred())
			peers = &fakePeers{relay: tunnel.NewRelay(other), from: "replica-a"}
		})

		It("relays through the owner and carries bytes to the worker", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "through a peer")
			Expect(peers.opened()).To(Equal([]string{"replica-b"}))
		})

		It("asks the owner for the bulk lane and for no fallback", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagHTTP, "", tunnel.WithBulkLane())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "file bytes")
			Expect(peers.requested()).To(ContainElement(ContainSubstring("bulk-only")))
		})

		It("gets the refusal of the owner when the owner has no bulk session, as that and not as a missing route", func() {
			Expect(other.Disconnect("w1")).To(BeTrue())
			front, back := sessionPair(tunnel.LaneInference)
			worker.serve(back)
			_, err := other.Attach(ctx, "w1", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())

			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			_, err = dialer.Dial(ctx, "w1", tunnel.StreamTagHTTP, "", tunnel.WithBulkLane())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, tunnel.ErrNoBulkSession)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "the tunnel is held and carries model calls; only a lane is down")
			Expect(worker.seen()).To(BeEmpty(), "nothing may have crossed the inference lane")
		})

		It("gives up on an owner that took the relay frame and never answers, when the budget of the caller runs out", func() {
			wedged := &wedgedPeers{}
			dialer := tunnel.NewWorkerDialer(tunnels, wedged)
			short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()

			done := make(chan error, 1)
			go func() {
				_, err := dialer.Dial(short, "w1", tunnel.StreamTagGRPC, "x:1")
				done <- err
			}()
			var err error
			Eventually(done, 2*time.Second).Should(Receive(&err), "a wedged owner must not hold the caller past its budget")
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "an expired budget is not a missing route")
			Eventually(wedged.farClosed, 2*time.Second).Should(BeTrue(), "the stream to the owner must be closed")
		})

		It("closes the stream to the owner when the caller cancels and stated no deadline", func() {
			wedged := &wedgedPeers{}
			dialer := tunnel.NewWorkerDialer(tunnels, wedged)
			cancelable, cancel := context.WithCancel(ctx)

			done := make(chan error, 1)
			go func() {
				_, err := dialer.Dial(cancelable, "w1", tunnel.StreamTagGRPC, "x:1")
				done <- err
			}()
			time.Sleep(200 * time.Millisecond)
			cancel()
			var err error
			Eventually(done, 2*time.Second).Should(Receive(&err))
			Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "%v", err)
			Eventually(wedged.farClosed, 2*time.Second).Should(BeTrue())
		})

		It("states the remaining budget of the caller in the relay request", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			budgeted, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			conn, err := dialer.Dial(budgeted, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })

			frames := peers.requested()
			Expect(frames).To(HaveLen(1))
			Expect(frames[0]).To(MatchRegexp(`^w1 inference (1\d{4}|20000)$`), "a budget of about 20 seconds, in milliseconds")
		})

		It("states no budget for a caller that set no deadline", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			Expect(peers.requested()).To(Equal([]string{"w1 inference 0"}))
		})

		It("passes a stale ownership refusal back as a routing fact", func() {
			// The table names replica-b, but the registry behind its relay holds no
			// session for the worker: the tunnel went away, and the row is a moment
			// old.
			peers.relay = tunnel.NewRelay(tunnel.NewRegistry(clusterR, "replica-b"))
			dialer := tunnel.NewWorkerDialer(tunnels, peers)
			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeTrue(), "the cause is kept for a caller that can act on it")
			Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse(), "absence must not reach the caller")
		})

		It("reports an unreachable peer as unreachable, with no ErrNoRoute and no absence", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, &downPeers{err: tunnelUnreachable()})
			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse(), "a link between two frontends says nothing about the worker")
			Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
			Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse())
		})

		It("keeps an owner that was swept in the middle of the dial out of the chain", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, &downPeers{err: cluster.ErrInstanceNotFound})
			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse())
		})

		It("reports having no way to relay as its own condition", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			_, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRelayPath)).To(BeTrue())
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeFalse(), "no peer was dialled")
		})

		It("does not relay to itself when the table names this replica", func() {
			own := tunnel.NewRegistry(clusterR, "replica-a")
			front, _ := sessionPair(tunnel.LaneInference)
			_, err := own.Attach(ctx, "w1", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())
			// A registry that does not hold the session but whose replica owns the row.
			dialer := tunnel.NewWorkerDialer(tunnel.NewRegistry(clusterR, "replica-a"), peers)
			_, err = dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(peers.opened()).To(BeEmpty(), "relaying would send the request into this same process")
		})
	})

	Describe("when no live replica holds the tunnel", func() {
		It("answers ErrNoRoute and never absence, for a worker with no connection row", func() {
			dialer := tunnel.NewWorkerDialer(tunnels, &fakePeers{})
			_, err := dialer.Dial(ctx, "nobody", tunnel.StreamTagGRPC, "x:1")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
			Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse())
		})
	})

	Describe("the dial functions it hands to the transports", func() {
		It("binds one node and one tag, and passes the address through as the target", func() {
			hold(false)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			conn, err := dialer.DialerFor("w1", tunnel.StreamTagHTTP)(ctx, "tcp", "w1.worker.invalid:80")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "x")
			Expect(worker.seen()).To(Equal([]workerRequest{{tunnel.StreamTagHTTP, "w1.worker.invalid:80"}}))
		})

		It("gives gRPC a dialer fixed on the grpc tag", func() {
			hold(false)
			dialer := tunnel.NewWorkerDialer(tunnels, nil)
			conn, err := dialer.GRPCDialerFor("w1")(ctx, "10.0.0.5:50051")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			roundTrip(conn, "x")
			Expect(worker.seen()).To(Equal([]workerRequest{{tunnel.StreamTagGRPC, "10.0.0.5:50051"}}))
		})
	})
})

// fakePeers opens a stream to the relay of another replica over a pipe, and
// records what the dialling side wrote first.
type fakePeers struct {
	relay *tunnel.Relay
	from  string

	mu     sync.Mutex
	peers  []string
	frames []string
}

func (f *fakePeers) Open(_ context.Context, peerID string) (net.Conn, error) {
	f.mu.Lock()
	f.peers = append(f.peers, peerID)
	f.mu.Unlock()
	if f.relay == nil {
		return nil, errors.New("no relay behind this fake")
	}
	near, far := net.Pipe()
	// The relay sees the far end. The near end is wrapped to record the frame
	// that the dialler writes first.
	go f.relay.Stream(f.from, far)
	return &recordingConn{Conn: near, onFirstWrite: func(b []byte) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// A frame is a two byte length and the payload.
		if len(b) > 2 {
			f.frames = append(f.frames, string(b[2:]))
		}
	}}, nil
}

func (f *fakePeers) opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.peers...)
}

func (f *fakePeers) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.frames...)
}

type recordingConn struct {
	net.Conn
	once         sync.Once
	onFirstWrite func([]byte)
}

func (c *recordingConn) Write(b []byte) (int, error) {
	c.once.Do(func() { c.onFirstWrite(bytes.Clone(b)) })
	return c.Conn.Write(b)
}

type downPeers struct{ err error }

func (d *downPeers) Open(context.Context, string) (net.Conn, error) { return nil, d.err }

// tunnelUnreachable returns an error that satisfies ErrPeerUnreachable the way
// the peer pool does: through a real pool dialling an address nobody listens on.
func tunnelUnreachable() error {
	GinkgoHelper()
	ctx := context.Background()
	db := testutil.SetupTestDB()
	Expect(cluster.Migrate(ctx, db)).To(Succeed())
	reg := cluster.NewRegistry(db)
	Expect(reg.RegisterPeer(ctx, "ghost", "test", 0, "", "127.0.0.1:1", cluster.NewPeerCredential().Hash())).To(Succeed())
	pool := tunnel.NewPeerPool("replica-a", cluster.NewPeerCredential(), reg)
	DeferCleanup(pool.Close)
	_, err := pool.Open(ctx, "ghost")
	Expect(err).To(MatchError(tunnel.ErrPeerUnreachable))
	return err
}

// wedgedPeers hands out a stream to an owner that reads the relay frame and
// then says nothing, the way a replica does when its session still answers the
// keepalive and its handler is stuck.
type wedgedPeers struct {
	mu  sync.Mutex
	end bool
}

func (w *wedgedPeers) Open(context.Context, string) (net.Conn, error) {
	near, far := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, far)
		w.mu.Lock()
		w.end = true
		w.mu.Unlock()
	}()
	return near, nil
}

func (w *wedgedPeers) farClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.end
}
