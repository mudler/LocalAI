package tunnel_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The relay request frame", func() {
	It("round-trips the node, the lane and a stated budget", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayRequest(&buf, "w1", "bulk", 1500*time.Millisecond)).To(Succeed())
		node, lane, budget, err := tunnel.ReadRelayRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(node).To(Equal("w1"))
		Expect(lane).To(Equal("bulk"))
		Expect(budget).To(Equal(1500 * time.Millisecond))
	})

	It("writes zero for a budget that is not stated, and reads it back as not stated", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayRequest(&buf, "w1", "inference", 0)).To(Succeed())
		_, _, budget, err := tunnel.ReadRelayRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(budget).To(BeZero())
	})

	It("rounds a sub-millisecond budget up, so that it keeps meaning almost none", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayRequest(&buf, "w1", "inference", time.Microsecond)).To(Succeed())
		_, _, budget, err := tunnel.ReadRelayRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(budget).To(Equal(time.Millisecond))
	})

	It("refuses a node id that would split across the separator, an empty one, and an unknown lane", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayRequest(&buf, "w 1", "inference", 0)).ToNot(Succeed())
		Expect(tunnel.WriteRelayRequest(&buf, "", "inference", 0)).ToNot(Succeed())
		Expect(tunnel.WriteRelayRequest(&buf, "w1", "express", 0)).ToNot(Succeed())
		Expect(buf.Len()).To(BeZero(), "nothing may reach the wire")
	})

	DescribeTable("rejects a frame it cannot read, as an ordinary error",
		func(payload string) {
			var buf bytes.Buffer
			frame := append([]byte{byte(len(payload) >> 8), byte(len(payload))}, payload...)
			buf.Write(frame)
			_, _, _, err := tunnel.ReadRelayRequest(&buf)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, tunnel.ErrRelayRequestInvalid)).To(BeFalse(),
				"that sentinel is what a relay sends; a reader that returned it would confuse the two ends")
		},
		Entry("a node alone", "w1"),
		Entry("a missing budget", "w1 bulk"),
		Entry("a budget that is not a number", "w1 bulk soon"),
		Entry("a negative budget", "w1 bulk -5"),
		Entry("an unknown lane", "w1 express 0"),
		Entry("no node", " bulk 0"),
		Entry("too many fields", "w1 bulk 0 extra"),
	)

	It("clamps a budget above a day, so that the multiplication cannot overflow", func() {
		var buf bytes.Buffer
		payload := "w1 inference 9223372036854775807"
		buf.Write(append([]byte{byte(len(payload) >> 8), byte(len(payload))}, payload...))
		_, _, budget, err := tunnel.ReadRelayRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(budget).To(Equal(24 * time.Hour))
	})
})

var _ = Describe("The relay reply", func() {
	roundTrip := func(reason error) error {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayRefusal(&buf, reason)).To(Succeed())
		return tunnel.ReadRelayReply(&buf)
	}

	It("reads the acceptance as nil", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteRelayAccepted(&buf)).To(Succeed())
		Expect(tunnel.ReadRelayReply(&buf)).To(Succeed())
	})

	It("keeps the three refusals apart", func() {
		Expect(roundTrip(tunnel.ErrNotOwner)).To(MatchError(tunnel.ErrNotOwner))
		Expect(roundTrip(tunnel.ErrRelayUnavailable)).To(MatchError(tunnel.ErrRelayUnavailable))
		Expect(roundTrip(tunnel.ErrNoBulkSession)).To(MatchError(tunnel.ErrNoBulkSession))
		Expect(roundTrip(errors.New("anything else"))).To(MatchError(tunnel.ErrRelayRequestInvalid))
	})

	It("never reads a refusal of the owner as one of the worker, nor the reverse", func() {
		Expect(tunnel.IsWorkerAnswer(roundTrip(tunnel.ErrNotOwner))).To(BeFalse())
		Expect(tunnel.IsWorkerAnswer(roundTrip(tunnel.ErrRelayUnavailable))).To(BeFalse())

		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRefusal(&buf, tunnel.ErrStreamTargetUnavailable)).To(Succeed())
		err := tunnel.ReadRelayReply(&buf)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, tunnel.ErrStreamTargetUnavailable)).To(BeFalse(), "a reader of the wrong hop fails with an unrecognised reply")
	})

	It("reports a code of a newer replica as itself and not as the nearest known code", func() {
		payload := "err relay-teleport somewhere else"
		var buf bytes.Buffer
		buf.Write(append([]byte{byte(len(payload) >> 8), byte(len(payload))}, payload...))
		err := tunnel.ReadRelayReply(&buf)
		Expect(err).To(MatchError(ContainSubstring("unrecognised code")))
		Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse())
		Expect(errors.Is(err, tunnel.ErrRelayUnavailable)).To(BeFalse())
	})

	It("reports a link that broke as a failed read and not as a refusal", func() {
		err := tunnel.ReadRelayReply(strings.NewReader(""))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse())
		Expect(errors.Is(err, tunnel.ErrRelayUnavailable)).To(BeFalse())
	})
})

var _ = Describe("The relay", func() {
	var (
		ctx      context.Context
		clusterR *cluster.Registry
		tunnels  *tunnel.Registry
		worker   *scriptedWorker
	)

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-b", "test", 0, "")).To(Succeed())
		tunnels = tunnel.NewRegistry(clusterR, "replica-b")
		worker = &scriptedWorker{}
		front, back := sessionPair(tunnel.LaneInference)
		worker.serve(back)
		_, err := tunnels.Attach(ctx, "w1", tunnel.LaneInference, front)
		Expect(err).ToNot(HaveOccurred())
	})

	// ask sends one request frame to a relay and returns the reply.
	ask := func(frame func(net.Conn)) (net.Conn, error) {
		near, far := net.Pipe()
		DeferCleanup(func() { _ = near.Close() })
		go tunnel.NewRelay(tunnels).Stream("replica-a", far)
		frame(near)
		_ = near.SetReadDeadline(time.Now().Add(5 * time.Second))
		return near, tunnel.ReadRelayReply(near)
	}

	It("relays a stream to the worker it names and splices the two", func() {
		near, err := ask(func(c net.Conn) {
			Expect(tunnel.WriteRelayRequest(c, "w1", "inference", 0)).To(Succeed())
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(tunnel.WriteStreamRequest(near, tunnel.StreamTagGRPC, "x:1")).To(Succeed())
		Expect(tunnel.ReadStreamReply(near)).To(Succeed())
	})

	It("refuses a worker it does not hold with ErrNotOwner, and does not relay onward", func() {
		_, err := ask(func(c net.Conn) {
			Expect(tunnel.WriteRelayRequest(c, "w-elsewhere", "inference", 0)).To(Succeed())
		})
		Expect(err).To(MatchError(tunnel.ErrNotOwner))
	})

	It("refuses a request it cannot read as a bad request", func() {
		_, err := ask(func(c net.Conn) { _, _ = c.Write([]byte{0, 3, 'a', 'b', 'c'}) })
		Expect(err).To(MatchError(tunnel.ErrRelayRequestInvalid))
	})

	It("refuses with ErrRelayUnavailable when the tunnel is held and will not carry a stream", func() {
		Expect(tunnels.Disconnect("w1")).To(BeTrue())
		front, _ := sessionPair(tunnel.LaneInference)
		_, err := tunnels.Attach(ctx, "w1", tunnel.LaneInference, front)
		Expect(err).ToNot(HaveOccurred())
		Expect(front.Close()).To(Succeed())

		_, err = ask(func(c net.Conn) {
			Expect(tunnel.WriteRelayRequest(c, "w1", "inference", 0)).To(Succeed())
		})
		Expect(err).To(MatchError(tunnel.ErrRelayUnavailable))
		Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeFalse(), "the tunnel is held here; the caller would come back to this replica")
	})

	It("ends a stream that never says which worker it is for", func() {
		near, far := net.Pipe()
		DeferCleanup(func() { _ = near.Close() })
		done := make(chan struct{})
		relay := tunnel.NewRelayWithTimeouts(tunnels, 150*time.Millisecond, time.Second)
		go func() { relay.Stream("replica-a", far); close(done) }()
		_ = near.SetReadDeadline(time.Now().Add(5 * time.Second))
		err := tunnel.ReadRelayReply(near)
		Expect(err).To(MatchError(tunnel.ErrRelayRequestInvalid))
		Eventually(done).Should(BeClosed())
	})

	It("takes the smaller of its ceiling and the stated budget for the open", func() {
		// A worker that holds a session which never accepts a stream: the open
		// blocks on the backlog. The stated budget ends it long before the ceiling.
		Expect(tunnels.Disconnect("w1")).To(BeTrue())
		front, _ := sessionPair(tunnel.LaneInference)
		_, err := tunnels.Attach(ctx, "w1", tunnel.LaneInference, front)
		Expect(err).ToNot(HaveOccurred())
		// Fill the accept backlog of the far side, which accepts nothing.
		var (
			heldMu sync.Mutex
			held   []net.Conn
		)
		DeferCleanup(func() {
			heldMu.Lock()
			defer heldMu.Unlock()
			for _, c := range held {
				_ = c.Close()
			}
		})
		for range 300 {
			go func() {
				c, err := tunnels.Open(ctx, "w1", tunnel.LaneInference)
				if err == nil {
					heldMu.Lock()
					held = append(held, c)
					heldMu.Unlock()
				}
			}()
		}
		time.Sleep(300 * time.Millisecond)

		begun := time.Now()
		_, rerr := ask(func(c net.Conn) {
			Expect(tunnel.WriteRelayRequest(c, "w1", "inference", 300*time.Millisecond)).To(Succeed())
		})
		Expect(rerr).To(MatchError(tunnel.ErrRelayUnavailable))
		Expect(time.Since(begun)).To(BeNumerically("<", 5*time.Second))
	})
})

var _ = Describe("Route failures", func() {
	It("never lets an absence claim out, and keeps ErrNoRoute on a route that does not exist", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg := cluster.NewRegistry(db)
		Expect(reg.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		dialer := tunnel.NewWorkerDialer(tunnel.NewRegistry(reg, "replica-a"), &downPeers{err: cluster.ErrInstanceNotFound})
		_, err := dialer.Dial(ctx, "nobody", tunnel.StreamTagGRPC, "x:1")
		Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeTrue())
		Expect(errors.Is(err, cluster.ErrNoConnection)).To(BeFalse())
		Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse())
		Expect(err.Error()).To(ContainSubstring("nobody"))
	})
})
