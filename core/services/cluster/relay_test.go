// SPDX-License-Identifier: MIT

package cluster_test

import (
	"bytes"
	"context"
	"io"
	"net"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// blockingRead runs one Read on its own goroutine with NO deadline set.
//
// The absence of the deadline is the point. A refusal and a stream left parked
// are indistinguishable to an assertion that waits for a deadline to expire:
// both produce an error at the same moment. Reading with no deadline at all
// means the channel only ever receives because the far side ANSWERED or ENDED
// the stream, so Eventually(...).Should(Receive()) is an assertion about the
// relay rather than about the clock.
func blockingRead(conn net.Conn) chan error {
	done := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		_, err := conn.Read(make([]byte, 1))
		done <- err
	}()
	return done
}

// relayReply reads the relay's answer, on its own goroutine and with no
// deadline, for the reason blockingRead gives.
func relayReply(conn net.Conn) chan error {
	done := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		done <- cluster.ReadRelayReply(conn)
	}()
	return done
}

// readInto reads exactly len(buf) bytes on its own goroutine, with no deadline.
func readInto(conn net.Conn, buf []byte) chan error {
	done := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		_, err := io.ReadFull(conn, buf)
		done <- err
	}()
	return done
}

// acceptOne hands back the next stream accepted on a session.
func acceptOne(sess *yamux.Session) chan net.Conn {
	accepted := make(chan net.Conn, 1)
	go func() {
		defer GinkgoRecover()
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		accepted <- stream
	}()
	return accepted
}

var _ = Describe("The inter-replica relay", func() {
	var (
		db  *gorm.DB
		reg *cluster.Registry
		tun *cluster.TunnelRegistry
		ctx context.Context

		// peer is the dialling replica's half of the peer link, the side a
		// relayed request arrives from.
		peer *yamux.Session
	)

	// openRelayStream opens a peer stream and names the node it is for.
	openRelayStream := func(nodeID string) net.Conn {
		GinkgoHelper()
		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		Expect(cluster.WriteRelayRequest(stream, nodeID)).To(Succeed())
		return stream
	}

	BeforeEach(func() {
		db = testutil.SetupTestDB()
		ctx = context.Background()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		tun = cluster.NewTunnelRegistry(reg, "me")

		store := cluster.NewSessionStore(cluster.NewRelay(tun).Stream)
		DeferCleanup(store.CloseAll)
		var accepted *yamux.Session
		peer, accepted = yamuxPair()
		store.Accept("peer-1", accepted)
	})

	It("splices a peer's stream onto a worker tunnel it holds, in both directions", func() {
		// This is the whole point of the relay: with one tunnel per worker
		// landing on ONE replica, every other replica reaches that worker only
		// by relaying through this path, so with N replicas it carries roughly
		// (N-1)/N of production traffic.
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		echoOnce(worker)

		stream := openRelayStream("w1")
		Eventually(relayReply(stream), "10s").Should(Receive(BeNil()))

		_, err = stream.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		echoed := make([]byte, 4)
		Eventually(readInto(stream, echoed), "10s").Should(Receive(BeNil()))
		Expect(string(echoed)).To(Equal("ping"))
	})

	It("does not forward the frame it consumed, so the worker sees only the tunnelled protocol", func() {
		// The relay request names the node for THIS hop and stops here. The
		// worker's own request frame is written by the dialling replica and
		// crosses untouched, so a relay that forwarded its own header would
		// make every relayed stream unparseable at the worker while every
		// locally-held one worked.
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		accepted := acceptOne(worker)

		stream := openRelayStream("w1")
		Eventually(relayReply(stream), "10s").Should(Receive(BeNil()))
		_, err = stream.Write([]byte("first"))
		Expect(err).ToNot(HaveOccurred())

		var workerSide net.Conn
		Eventually(accepted, "10s").Should(Receive(&workerSide))
		first := make([]byte, 5)
		Eventually(readInto(workerSide, first), "10s").Should(Receive(BeNil()))
		Expect(string(first)).To(Equal("first"))
	})

	It("tears down the worker's side when the peer's side closes", func() {
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		accepted := acceptOne(worker)

		stream := openRelayStream("w1")
		Eventually(relayReply(stream), "10s").Should(Receive(BeNil()))
		var workerSide net.Conn
		Eventually(accepted, "10s").Should(Receive(&workerSide))

		Expect(stream.Close()).To(Succeed())
		// A relay that copies but does not tear down leaves a backend
		// connection per abandoned request, and a worker runs out of them.
		Eventually(blockingRead(workerSide), "10s").Should(Receive(HaveOccurred()))
	})

	It("tears down the peer's side when the worker's side closes", func() {
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		accepted := acceptOne(worker)

		stream := openRelayStream("w1")
		Eventually(relayReply(stream), "10s").Should(Receive(BeNil()))
		var workerSide net.Conn
		Eventually(accepted, "10s").Should(Receive(&workerSide))

		Expect(workerSide.Close()).To(Succeed())
		Eventually(blockingRead(stream), "10s").Should(Receive(MatchError(io.EOF)))
	})

	It("refuses a node it does not hold with the routing fact, and ENDS the stream", func() {
		stream := openRelayStream("not-here")

		var reply error
		Eventually(relayReply(stream), "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(cluster.ErrNotOwner))
		// Four conditions this phase forbids collapsing. ErrNotOwner says
		// "ask the owner"; absence says "this worker is gone" and a scheduler
		// acts on that; unreachability says "retry".
		Expect(reply).ToNot(MatchError(cluster.ErrNoConnection))
		Expect(reply).ToNot(MatchError(cluster.ErrPeerUnreachable))
		Expect(reply).ToNot(MatchError(cluster.ErrInstanceNotFound))
		Expect(reply).ToNot(MatchError(cluster.ErrRelayUnavailable))

		// Answering is not enough. A relay that says why and leaves the stream
		// open has parked the peer on a request that will never be served,
		// which reads as a slow replica rather than a refused request.
		Eventually(blockingRead(stream), "10s").Should(Receive(MatchError(io.EOF)))
	})

	It("refuses rather than chasing a node another live replica owns", func() {
		// A relay that resolved the owner and relayed onward would make a
		// stale row into a loop between two replicas, each certain the other
		// holds the worker. One hop, always: the dialling replica re-resolves.
		Expect(reg.Register(ctx, "other", "10.0.0.2:8080", "v1")).To(Succeed())
		_, err := reg.Claim(ctx, "w1", "other")
		Expect(err).ToNot(HaveOccurred())

		stream := openRelayStream("w1")
		var reply error
		Eventually(relayReply(stream), "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(cluster.ErrNotOwner))
		Eventually(blockingRead(stream), "10s").Should(Receive(MatchError(io.EOF)))
	})

	It("reports a tunnel that will not carry a stream as infrastructure, never as not-owner", func() {
		// The tunnel IS held here; its session died. Answering ErrNotOwner
		// would send the dialling replica looking elsewhere for a worker that
		// is attached right here, and it would find this replica again.
		frontend, worker := workerTunnel()
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		Expect(worker.Close()).To(Succeed())
		Eventually(frontend.IsClosed, "10s").Should(BeTrue())

		stream := openRelayStream("w1")
		var reply error
		Eventually(relayReply(stream), "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(cluster.ErrRelayUnavailable))
		Expect(reply).ToNot(MatchError(cluster.ErrNotOwner))
		Expect(reply).ToNot(MatchError(cluster.ErrNoConnection))
		Expect(reply).ToNot(MatchError(cluster.ErrInstanceNotFound))
		Eventually(blockingRead(stream), "10s").Should(Receive(MatchError(io.EOF)))
	})

	It("refuses a malformed opening frame as the caller's bug", func() {
		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		// A well-formed frame carrying no node id. A relay that read this as a
		// node named "" would go looking for it and answer ErrNotOwner, which
		// tells the caller to retry elsewhere for a request no replica can
		// ever serve.
		_, err = stream.Write([]byte{0x00, 0x00})
		Expect(err).ToNot(HaveOccurred())

		var reply error
		Eventually(relayReply(stream), "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(cluster.ErrRelayRequestInvalid))
		Expect(reply).ToNot(MatchError(cluster.ErrNotOwner))
		Expect(reply).ToNot(MatchError(cluster.ErrRelayUnavailable))
		Eventually(blockingRead(stream), "10s").Should(Receive(MatchError(io.EOF)))
	})
})

var _ = Describe("The relay wire framing", func() {
	// The relay hop and the worker tunnel hop travel back to back on one
	// stream, and a dialler reads a reply from each in order. Giving them
	// disjoint vocabularies means a reader applied to the wrong hop fails
	// loudly rather than returning a plausible sentinel for the other hop,
	// which would report a worker's refusal as the owning replica's and send a
	// retry to the wrong place.
	It("does not read a relay reply as a worker tunnel reply", func() {
		frame := &bytes.Buffer{}
		Expect(cluster.WriteRelayAccepted(frame)).To(Succeed())
		Expect(cluster.ReadStreamReply(frame)).To(HaveOccurred())
	})

	It("does not read a worker tunnel reply as a relay reply", func() {
		frame := &bytes.Buffer{}
		Expect(cluster.WriteStreamAccepted(frame)).To(Succeed())
		Expect(cluster.ReadRelayReply(frame)).To(HaveOccurred())
	})

	It("round-trips a node id", func() {
		frame := &bytes.Buffer{}
		Expect(cluster.WriteRelayRequest(frame, "node-7")).To(Succeed())
		nodeID, err := cluster.ReadRelayRequest(frame)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeID).To(Equal("node-7"))
	})

	It("refuses to write an empty node id, rather than spending a round trip on it", func() {
		Expect(cluster.WriteRelayRequest(&bytes.Buffer{}, "")).To(HaveOccurred())
	})

	// Acceptance is not the whole surface. A refusal read by the wrong hop's
	// reader must not come back as one of that hop's own sentinels: "the
	// owning replica does not hold this worker" arriving as "the worker does
	// not serve that tag" would send a retry to the wrong end of the path, and
	// it would look like a perfectly ordinary answer on the way.
	DescribeTable("does not read a relay refusal as one of the worker tunnel's",
		func(reason error) {
			frame := &bytes.Buffer{}
			Expect(cluster.WriteRelayRefusal(frame, reason)).To(Succeed())
			err := cluster.ReadStreamReply(frame)
			Expect(err).To(HaveOccurred())
			Expect(err).ToNot(MatchError(cluster.ErrStreamTagUnknown))
			Expect(err).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(err).ToNot(MatchError(cluster.ErrStreamRequestInvalid))
		},
		Entry("not the owner", cluster.ErrNotOwner),
		Entry("the tunnel will not carry a stream", cluster.ErrRelayUnavailable),
		Entry("a malformed relay request", cluster.ErrRelayRequestInvalid),
	)

	DescribeTable("does not read a worker tunnel refusal as one of the relay's",
		func(reason error) {
			frame := &bytes.Buffer{}
			Expect(cluster.WriteStreamRefusal(frame, reason)).To(Succeed())
			err := cluster.ReadRelayReply(frame)
			Expect(err).To(HaveOccurred())
			Expect(err).ToNot(MatchError(cluster.ErrNotOwner))
			Expect(err).ToNot(MatchError(cluster.ErrRelayUnavailable))
			Expect(err).ToNot(MatchError(cluster.ErrRelayRequestInvalid))
		},
		Entry("an unknown stream tag", cluster.ErrStreamTagUnknown),
		Entry("a local service that will not answer", cluster.ErrStreamTargetUnavailable),
		Entry("a malformed stream request", cluster.ErrStreamRequestInvalid),
	)
})
