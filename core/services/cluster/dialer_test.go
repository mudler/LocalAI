// SPDX-License-Identifier: MIT

package cluster_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// stubPeers is a PeerOpener that hands back streams on one session, or one
// error. It stands in for the replica-to-replica link so a spec can decide what
// a peer does without standing up a second frontend.
type stubPeers struct {
	sess *yamux.Session
	err  error
}

func (s *stubPeers) Open(ctx context.Context, _ string) (net.Conn, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.sess.OpenStream(ctx)
}

// dialResult carries what a Dial produced, so a spec can wait on a channel
// rather than on a clock.
type dialResult struct {
	conn net.Conn
	err  error
}

// dialAsync runs one Dial on its own goroutine. Dial talks to a worker that a
// spec drives by hand, so the spec has to be free to answer while the dial is
// still in flight.
func dialAsync(d *cluster.WorkerDialer, ctx context.Context, nodeID, tag, target string) chan dialResult {
	done := make(chan dialResult, 1)
	go func() {
		defer GinkgoRecover()
		conn, err := d.Dial(ctx, nodeID, tag, target)
		done <- dialResult{conn: conn, err: err}
	}()
	return done
}

// relayRequest is what an owning replica saw in the frame that opened a
// relayed stream.
type relayRequest struct {
	nodeID string
	budget time.Duration
	err    error
}

// servedRequest is what a worker saw on a stream opened through the tunnel.
type servedRequest struct {
	tag    string
	target string
	stream net.Conn
	err    error
}

// serveOneStream accepts one stream on the worker's half, reads the tunnel
// request frame and accepts it, then echoes the four bytes it is sent. It is
// how a spec proves the dial produced a stream that carries the tunnelled
// protocol, and that the frame the worker sees is the one the dialer wrote.
func serveOneStream(worker *yamux.Session) chan servedRequest {
	seen := make(chan servedRequest, 1)
	go func() {
		defer GinkgoRecover()
		stream, err := worker.AcceptStream()
		if err != nil {
			seen <- servedRequest{err: err}
			return
		}
		tag, target, err := cluster.ReadStreamRequest(stream)
		if err != nil {
			seen <- servedRequest{err: err}
			return
		}
		if err := cluster.WriteStreamAccepted(stream); err != nil {
			seen <- servedRequest{err: err}
			return
		}
		seen <- servedRequest{tag: tag, target: target, stream: stream}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(stream, buf); err != nil {
			return
		}
		_, _ = stream.Write(buf)
	}()
	return seen
}

// refuseOneStream accepts one stream and refuses it with reason.
func refuseOneStream(worker *yamux.Session, reason error) {
	go func() {
		defer GinkgoRecover()
		stream, err := worker.AcceptStream()
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		if _, _, err := cluster.ReadStreamRequest(stream); err != nil {
			return
		}
		_ = cluster.WriteStreamRefusal(stream, reason)
	}()
}

// expectNotAbsence asserts an error is none of the sentinels a caller is
// entitled to act on as "this worker has gone away".
//
// It is the assertion this whole phase turns on. core/services/nodes reclaims a
// worker's models when it concludes the worker is absent, so an unreachable
// peer or a refusing worker arriving as absence would evict healthy work.
func expectNotAbsence(err error) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(err).ToNot(MatchError(cluster.ErrNoConnection))
	Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
}

var _ = Describe("The worker dialer", func() {
	var (
		db   *gorm.DB
		reg  *cluster.Registry
		mine *cluster.TunnelRegistry
		ctx  context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		mine = cluster.NewTunnelRegistry(reg, "me")
	})

	// ownerRelay stands up a SECOND replica that holds w1's tunnel and relays
	// for it, and returns the peer opener this replica reaches it through plus
	// the worker's own half of the tunnel.
	ownerRelay := func(nodeID string) (*stubPeers, *yamux.Session) {
		GinkgoHelper()
		Expect(reg.Register(ctx, "owner", "10.0.0.2:8080", "v1")).To(Succeed())
		ownerTunnels := cluster.NewTunnelRegistry(reg, "owner")
		frontend, worker := workerTunnel()
		_, err := ownerTunnels.Attach(ctx, nodeID, frontend)
		Expect(err).ToNot(HaveOccurred())

		store := cluster.NewSessionStore(cluster.NewRelay(ownerTunnels).Stream)
		DeferCleanup(store.CloseAll)
		dialling, accepted := yamuxPair()
		store.Accept("me", accepted)
		return &stubPeers{sess: dialling}, worker
	}

	Describe("when this replica holds the tunnel", func() {
		It("opens a stream straight down it, naming the service the caller asked for", func() {
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			seen := serveOneStream(worker)

			d := cluster.NewWorkerDialer(mine, nil)
			result := dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000")

			var req servedRequest
			Eventually(seen, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			Expect(req.tag).To(Equal(cluster.StreamTagGRPC))
			// The TARGET is what tells the worker which backend process the
			// stream is for. A dialer that dropped it would send every request
			// to whichever port the worker guessed.
			Expect(req.target).To(Equal("127.0.0.1:41000"))

			var out dialResult
			Eventually(result, "10s").Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = out.conn.Close() })

			// Bytes, not a handle. A dial that returns a stream the tunnelled
			// protocol cannot use is worse than one that fails.
			_, err = out.conn.Write([]byte("ping"))
			Expect(err).ToNot(HaveOccurred())
			echoed := make([]byte, 4)
			Eventually(readInto(out.conn, echoed), "10s").Should(Receive(BeNil()))
			Expect(string(echoed)).To(Equal("ping"))
		})

		It("leaves no read deadline armed on the stream it hands back", func() {
			// The handshake is bounded; the request that follows it is the
			// caller's business and may be a generation that is quiet for
			// minutes. A deadline left armed here would abort it.
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			seen := serveOneStream(worker)

			deadlined, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()
			d := cluster.NewWorkerDialer(mine, nil)
			result := dialAsync(d, deadlined, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000")
			Eventually(seen, "10s").Should(Receive())

			var out dialResult
			Eventually(result, "10s").Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = out.conn.Close() })

			// The dial context's deadline has now passed. A stream still
			// carrying it would fail this write and this read.
			Eventually(func() error {
				_, err := out.conn.Write([]byte("ping"))
				return err
			}, "10s").Should(Succeed())
			echoed := make([]byte, 4)
			Eventually(readInto(out.conn, echoed), "10s").Should(Receive(BeNil()))
			Expect(string(echoed)).To(Equal("ping"))
		})

		It("reports a worker's refusal as the worker's refusal, never as absence", func() {
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			refuseOneStream(worker, cluster.ErrStreamTagUnknown)

			d := cluster.NewWorkerDialer(mine, nil)
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", "nonsense", ""), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrStreamTagUnknown))
			// A refusal is PROOF the worker is connected and answered.
			expectNotAbsence(out.err)
		})

		It("reports a broken tunnel held here as itself, not as a routing fact", func() {
			// ErrNotOwner tells a caller to look for the worker elsewhere. For
			// a tunnel held right here that sends it back to this replica, and
			// the loop is only broken by the request failing anyway.
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			Expect(worker.Close()).To(Succeed())

			d := cluster.NewWorkerDialer(mine, nil)
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(HaveOccurred())
			Expect(out.err).ToNot(MatchError(cluster.ErrNotOwner))
			expectNotAbsence(out.err)
		})
	})

	Describe("when another replica holds the tunnel", func() {
		It("relays through the owner and carries bytes to the worker", func() {
			peers, worker := ownerRelay("w1")
			seen := serveOneStream(worker)

			d := cluster.NewWorkerDialer(mine, peers)
			result := dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000")

			var req servedRequest
			Eventually(seen, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			// The relay consumed its own frame and forwarded nothing of it, so
			// the worker sees only the tunnel's request.
			Expect(req.tag).To(Equal(cluster.StreamTagGRPC))
			Expect(req.target).To(Equal("127.0.0.1:41000"))

			var out dialResult
			Eventually(result, "10s").Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = out.conn.Close() })

			_, err := out.conn.Write([]byte("ping"))
			Expect(err).ToNot(HaveOccurred())
			echoed := make([]byte, 4)
			Eventually(readInto(out.conn, echoed), "10s").Should(Receive(BeNil()))
			Expect(string(echoed)).To(Equal("ping"))
		})

		It("states the caller's remaining budget in the relay request", func() {
			// The owning replica's own open bound is a backstop nobody can set
			// correctly: the number that matters is how long the ORIGINAL
			// client will wait, and this replica is the only one that holds it.
			// This spec plays the owner by hand so it can read the frame rather
			// than infer it from a timing.
			Expect(reg.Register(ctx, "owner", "10.0.0.2:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "owner")
			Expect(err).ToNot(HaveOccurred())
			dialling, ownerSide := yamuxPair()
			DeferCleanup(func() { _ = ownerSide.Close() })

			requests := make(chan relayRequest, 1)
			go func() {
				defer GinkgoRecover()
				stream, err := ownerSide.AcceptStream()
				if err != nil {
					return
				}
				defer func() { _ = stream.Close() }()
				nodeID, budget, err := cluster.ReadRelayRequest(stream)
				requests <- relayRequest{nodeID: nodeID, budget: budget, err: err}
			}()

			budgeted, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			d := cluster.NewWorkerDialer(mine, &stubPeers{sess: dialling})
			dialAsync(d, budgeted, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000")

			var req relayRequest
			Eventually(requests, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			Expect(req.nodeID).To(Equal("w1"))
			// Whatever is left of the four seconds, and nothing invented: a
			// dialer that stated its own constant would satisfy neither bound.
			Expect(req.budget).To(BeNumerically(">", 2*time.Second))
			Expect(req.budget).To(BeNumerically("<=", 4*time.Second))
		})

		It("states no budget at all for a caller that set no deadline", func() {
			// Zero on the wire would be read by the owner as a caller with
			// nothing left, and it would refuse traffic that is perfectly
			// healthy. "Not stated" has to stay distinguishable from "expired".
			Expect(reg.Register(ctx, "owner", "10.0.0.2:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "owner")
			Expect(err).ToNot(HaveOccurred())
			dialling, ownerSide := yamuxPair()
			DeferCleanup(func() { _ = ownerSide.Close() })

			requests := make(chan relayRequest, 1)
			go func() {
				defer GinkgoRecover()
				stream, err := ownerSide.AcceptStream()
				if err != nil {
					return
				}
				defer func() { _ = stream.Close() }()
				nodeID, budget, err := cluster.ReadRelayRequest(stream)
				requests <- relayRequest{nodeID: nodeID, budget: budget, err: err}
			}()

			d := cluster.NewWorkerDialer(mine, &stubPeers{sess: dialling})
			dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000")

			var req relayRequest
			Eventually(requests, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			Expect(req.budget).To(BeZero())
		})

		It("reports an unreachable peer as unreachable, NEVER as absence", func() {
			// The catastrophe this phase exists to prevent. A scheduler ACTS on
			// absence: told a connected worker is gone, it reclaims every model
			// the worker is running.
			Expect(reg.Register(ctx, "owner", "127.0.0.1:1", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "owner")
			Expect(err).ToNot(HaveOccurred())

			pool := cluster.NewPeerPool("me", "tok", reg)
			DeferCleanup(pool.Close)
			d := cluster.NewWorkerDialer(mine, pool)

			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "20s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrPeerUnreachable))
			expectNotAbsence(out.err)
		})

		It("passes a stale ownership refusal back as the routing fact", func() {
			// The owner's table row survives a tunnel that has gone. The relay
			// answers ErrNotOwner, and only that answer tells this replica to
			// resolve the owner again rather than give up on the worker.
			Expect(reg.Register(ctx, "owner", "10.0.0.2:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "owner")
			Expect(err).ToNot(HaveOccurred())
			ownerTunnels := cluster.NewTunnelRegistry(reg, "owner")
			store := cluster.NewSessionStore(cluster.NewRelay(ownerTunnels).Stream)
			DeferCleanup(store.CloseAll)
			dialling, accepted := yamuxPair()
			store.Accept("me", accepted)

			d := cluster.NewWorkerDialer(mine, &stubPeers{sess: dialling})
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrNotOwner))
			expectNotAbsence(out.err)
		})

		It("refuses rather than relaying to itself when the table names this replica", func() {
			// The row says this replica owns the tunnel and the registry says
			// it does not hold it. Relaying would send the request to this same
			// process, which would resolve the same owner and relay again.
			_, err := reg.Claim(ctx, "w1", "me")
			Expect(err).ToNot(HaveOccurred())

			d := cluster.NewWorkerDialer(mine, &stubPeers{err: errors.New("no peer should be dialled")})
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrNotOwner))
			expectNotAbsence(out.err)
		})

		It("reports having no way to relay as its own condition", func() {
			Expect(reg.Register(ctx, "owner", "10.0.0.2:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "owner")
			Expect(err).ToNot(HaveOccurred())

			d := cluster.NewWorkerDialer(mine, nil)
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrNoRelayPath))
			Expect(out.err).ToNot(MatchError(cluster.ErrNotOwner))
			Expect(out.err).ToNot(MatchError(cluster.ErrPeerUnreachable))
			expectNotAbsence(out.err)
		})
	})

	Describe("when no live replica holds the tunnel", func() {
		It("reports absence when the worker has no connection row at all", func() {
			d := cluster.NewWorkerDialer(mine, &stubPeers{err: errors.New("no peer should be dialled")})
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrNoConnection))
		})

		It("reports absence when the row's owner has stopped heartbeating", func() {
			// End to end over the join Owner does: the row is there, the owner
			// is not. A dialer built on the unjoined read would dial a corpse
			// and report the worker as unreachable rather than as absent.
			Expect(reg.Register(ctx, "ghost", "10.0.0.9:8080", "v1")).To(Succeed())
			_, err := reg.Claim(ctx, "w1", "ghost")
			Expect(err).ToNot(HaveOccurred())
			Expect(db.Exec(
				`UPDATE instances SET last_seen = now() - make_interval(secs => ?) WHERE id = ?`,
				cluster.InstanceLiveness.Seconds()*4, "ghost").Error).To(Succeed())

			owner, _, err := reg.OwnerRow(ctx, "w1")
			Expect(err).ToNot(HaveOccurred())
			Expect(owner).To(Equal("ghost"))

			d := cluster.NewWorkerDialer(mine, &stubPeers{err: errors.New("no peer should be dialled")})
			var out dialResult
			Eventually(dialAsync(d, ctx, "w1", cluster.StreamTagGRPC, "127.0.0.1:41000"), "10s").Should(Receive(&out))
			Expect(out.err).To(MatchError(cluster.ErrNoConnection))
		})
	})

	Describe("the dialer functions it hands to the transports", func() {
		It("binds one node and one tag, and passes the address through as the target", func() {
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			seen := serveOneStream(worker)

			d := cluster.NewWorkerDialer(mine, nil)
			dial := d.DialerFor("w1", cluster.StreamTagHTTP)
			done := make(chan dialResult, 1)
			go func() {
				defer GinkgoRecover()
				conn, err := dial(ctx, "tcp", "10.0.0.3:9090")
				done <- dialResult{conn: conn, err: err}
			}()

			var req servedRequest
			Eventually(seen, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			Expect(req.tag).To(Equal(cluster.StreamTagHTTP))
			Expect(req.target).To(Equal("10.0.0.3:9090"))

			var out dialResult
			Eventually(done, "10s").Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = out.conn.Close() })
		})

		It("gives gRPC a dialer fixed on the grpc tag", func() {
			frontend, worker := workerTunnel()
			_, err := mine.Attach(ctx, "w1", frontend)
			Expect(err).ToNot(HaveOccurred())
			seen := serveOneStream(worker)

			d := cluster.NewWorkerDialer(mine, nil)
			dial := d.GRPCDialerFor("w1")
			done := make(chan dialResult, 1)
			go func() {
				defer GinkgoRecover()
				conn, err := dial(ctx, "127.0.0.1:41000")
				done <- dialResult{conn: conn, err: err}
			}()

			var req servedRequest
			Eventually(seen, "10s").Should(Receive(&req))
			Expect(req.err).ToNot(HaveOccurred())
			Expect(req.tag).To(Equal(cluster.StreamTagGRPC))
			Expect(req.target).To(Equal("127.0.0.1:41000"))

			var out dialResult
			Eventually(done, "10s").Should(Receive(&out))
			Expect(out.err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = out.conn.Close() })
		})
	})
})

var _ = Describe("The relay request frame", func() {
	It("carries a stated budget and reads it back", func() {
		frame := &bytes.Buffer{}
		Expect(cluster.WriteRelayRequest(frame, "node-7", 2500*time.Millisecond)).To(Succeed())
		nodeID, budget, err := cluster.ReadRelayRequest(frame)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeID).To(Equal("node-7"))
		Expect(budget).To(Equal(2500 * time.Millisecond))
	})

	It("writes no budget at all when none is stated", func() {
		// Zero must not reach the wire as the number zero: on the far side that
		// is a caller with no time left, and the relay would refuse healthy
		// traffic instead of falling back to its ceiling.
		frame := &bytes.Buffer{}
		Expect(cluster.WriteRelayRequest(frame, "node-7", 0)).To(Succeed())
		Expect(frame.Len()).To(Equal(2 + len("node-7")))
		nodeID, budget, err := cluster.ReadRelayRequest(frame)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeID).To(Equal("node-7"))
		Expect(budget).To(BeZero())
	})

	It("refuses a node id that would split across the separator", func() {
		Expect(cluster.WriteRelayRequest(&bytes.Buffer{}, "node 7", time.Second)).ToNot(Succeed())
		Expect(cluster.WriteRelayRequest(&bytes.Buffer{}, "", time.Second)).ToNot(Succeed())
	})

	It("rejects a budget that is not a number of milliseconds", func() {
		// The writer cannot produce this; a mismatched peer can, and reading it
		// as "not stated" would silently restore the ceiling this frame exists
		// to replace.
		var raw bytes.Buffer
		writeRawFrame(&raw, "node-7 soon")
		_, _, err := cluster.ReadRelayRequest(&raw)
		Expect(err).To(HaveOccurred())
	})

	It("treats an expired stated budget as an error rather than as silence", func() {
		var raw bytes.Buffer
		writeRawFrame(&raw, "node-7 0")
		_, _, err := cluster.ReadRelayRequest(&raw)
		Expect(err).To(HaveOccurred())
		Expect(fmt.Sprint(err)).To(ContainSubstring("expired"))
	})
})
