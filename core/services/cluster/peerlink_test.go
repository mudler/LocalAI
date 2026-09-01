package cluster_test

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// servePeerRoute mounts the peer handler on the route both sides agree on.
//
// It deliberately does not call routes.RegisterClusterRoutes: that registrar
// lives in core/http/routes, which imports half the server, and these specs are
// about the handler and the dialler rather than about the route table. The path
// comes from the same constant the registrar uses, so the two cannot drift.
func servePeerRoute(e *echo.Echo, token string, onPeer func(string, *yamux.Session)) {
	e.GET(cluster.PeerPath, clusterep.PeerHandler(token, onPeer))
}

// deadlinePassed is a context whose deadline has elapsed and whose
// cancellation has not been delivered, which is the state a caller is in for
// the moment between the two timers that fire at its deadline. Only Deadline is
// overridden: the embedded context supplies a nil Done and a nil Err, which is
// what a context in that window reports.
type deadlinePassed struct{ context.Context }

func (deadlinePassed) Deadline() (time.Time, bool) {
	return time.Now().Add(-time.Millisecond), true
}

var _ = Describe("Peer pool", func() {
	var (
		db       *gorm.DB
		reg      *cluster.Registry
		pool     *cluster.PeerPool
		srv      *httptest.Server
		accepted chan *yamux.Session
		ctx      context.Context
	)

	// startPeer stands up a real peer server and registers it under peerID.
	startPeer := func(peerID string) *httptest.Server {
		e := echo.New()
		servePeerRoute(e, "peer-token", func(_ string, s *yamux.Session) {
			accepted <- s
		})
		ts := httptest.NewServer(e)
		addr := strings.TrimPrefix(ts.URL, "http://")
		Expect(reg.Register(ctx, peerID, addr, "test")).To(Succeed())
		return ts
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)
		accepted = make(chan *yamux.Session, 4)
		pool = cluster.NewPeerPool("self", "peer-token", reg)
		DeferCleanup(pool.Close)
		srv = startPeer("peer-1")
		DeferCleanup(srv.Close)
	})

	It("opens a working stream to a live peer", func() {
		st, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })

		var serverSess *yamux.Session
		Eventually(accepted, "10s").Should(Receive(&serverSess))

		go func() {
			defer GinkgoRecover()
			_, _ = st.Write([]byte("ping"))
		}()

		got := make(chan []byte, 1)
		go func() {
			defer GinkgoRecover()
			in, e := serverSess.AcceptStream()
			if e != nil {
				return
			}
			buf := make([]byte, 4)
			if _, e := io.ReadFull(in, buf); e == nil {
				got <- buf
			}
		}()
		Eventually(got, "10s").Should(Receive(Equal([]byte("ping"))))
	})

	It("identifies itself to the peer by its own instance id", func() {
		// The peer records which replica is on the far end of the link, so a
		// pool that sent the peer's id (or nothing) would leave every inbound
		// link anonymous and indistinguishable from every other.
		ids := make(chan string, 1)
		e := echo.New()
		servePeerRoute(e, "peer-token", func(id string, _ *yamux.Session) { ids <- id })
		ts := httptest.NewServer(e)
		DeferCleanup(ts.Close)
		Expect(reg.Register(ctx, "peer-named", strings.TrimPrefix(ts.URL, "http://"), "test")).To(Succeed())

		st, err := pool.Open(ctx, "peer-named")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = st.Close() })
		Eventually(ids, "10s").Should(Receive(Equal("self")))
	})

	It("reuses one session across opens rather than dialling per stream", func() {
		a, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = a.Close() })
		Eventually(accepted, "10s").Should(Receive())

		b, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = b.Close() })

		// A second dial would deliver a second server session. One session
		// serving both streams is the property under test: peer links are
		// pooled, not per-stream.
		Consistently(accepted, "2s", "200ms").ShouldNot(Receive())
	})

	It("returns ErrPeerUnreachable when the peer is registered but not listening", func() {
		dead := startPeer("peer-dead")
		dead.Close()

		_, err := pool.Open(ctx, "peer-dead")
		Expect(err).To(MatchError(cluster.ErrPeerUnreachable),
			"a peer that will not answer must be a transport error, never node absence")
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound),
			"an unreachable peer must never be readable as an absent node; a replica acting on absence evicts healthy workers")
	})

	It("returns ErrPeerUnreachable when the peer answers but rejects the credentials", func() {
		// A token mismatch is a live peer refusing the link, not a missing
		// row. Reporting absence here would evict every worker behind a peer
		// that was merely rolled out with a stale secret.
		e := echo.New()
		servePeerRoute(e, "a-different-token", func(_ string, s *yamux.Session) { accepted <- s })
		ts := httptest.NewServer(e)
		DeferCleanup(ts.Close)
		Expect(reg.Register(ctx, "peer-strict", strings.TrimPrefix(ts.URL, "http://"), "test")).To(Succeed())

		_, err := pool.Open(ctx, "peer-strict")
		Expect(err).To(MatchError(cluster.ErrPeerUnreachable))
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
	})

	It("returns ErrInstanceNotFound when the peer is not in the registry", func() {
		_, err := pool.Open(ctx, "never-registered")
		Expect(err).To(MatchError(cluster.ErrInstanceNotFound))
		Expect(err).ToNot(MatchError(cluster.ErrPeerUnreachable),
			"a node that was never registered is absent, not merely unreachable")
	})

	It("re-dials after the cached session dies", func() {
		first, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(first.Close()).To(Succeed())

		var serverSess *yamux.Session
		Eventually(accepted, "10s").Should(Receive(&serverSess))
		Expect(serverSess.Close()).To(Succeed())
		srv.Close()

		// A replacement peer comes back on a new address under the same id,
		// which is what a restarted replica looks like.
		replacement := startPeer("peer-1")
		DeferCleanup(replacement.Close)

		Eventually(func() error {
			st, e := pool.Open(ctx, "peer-1")
			if e == nil {
				_ = st.Close()
			}
			return e
		}, "15s", "500ms").Should(Succeed())

		// The replacement's own session proves the pool re-dialled the address
		// it re-read from the registry rather than resurrecting the dead one.
		Eventually(accepted, "10s").Should(Receive())
	})

	It("does not drop the pooled session when a single stream is reset by the peer", func() {
		// A peer-initiated stream reset is scoped to one request. Dropping the
		// session on it would tear down every other worker's traffic on the
		// same link, so the pool must keep the session and hand out a fresh
		// stream on it.
		st, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())

		var serverSess *yamux.Session
		Eventually(accepted, "10s").Should(Receive(&serverSess))

		go func() {
			defer GinkgoRecover()
			_, _ = st.Write([]byte("x"))
		}()
		var inbound *yamux.Stream
		Eventually(func() error {
			s, e := serverSess.AcceptStream()
			inbound = s
			return e
		}, "10s").Should(Succeed())
		// Reset, not a graceful close: this is the *StreamError{Remote:true}
		// the far end sends when it abandons a request.
		Expect(inbound.Reset()).To(Succeed())
		Eventually(func() error {
			_, e := st.Write([]byte("y"))
			return e
		}, "10s", "100ms").Should(HaveOccurred())

		next, err := pool.Open(ctx, "peer-1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = next.Close() })
		Consistently(accepted, "2s", "200ms").ShouldNot(Receive(),
			"a reset stream must not cost the whole peer link")
	})

	It("blames the caller's deadline, not the peer, when a dial runs out of time", func() {
		// A listener that completes the TCP connection and then says nothing,
		// which is what a peer under load or behind a wedged proxy looks like.
		// The peer is not unreachable; the caller is impatient. Reporting
		// ErrPeerUnreachable here would make an impatient client enough to get
		// a healthy replica routed around.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = ln.Close() })
		// The accept loop owns the connections it holds and closes them when
		// the listener goes away, so nothing is shared with the spec goroutine.
		go func() {
			defer GinkgoRecover()
			var held []net.Conn
			defer func() {
				for _, c := range held {
					_ = c.Close()
				}
			}()
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				// Hold the connection open without ever answering the upgrade.
				held = append(held, c)
			}
		}()
		Expect(reg.Register(ctx, "peer-silent", ln.Addr().String(), "test")).To(Succeed())

		deadlined, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		DeferCleanup(cancel)
		_, err = pool.Open(deadlined, "peer-silent")
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(err).ToNot(MatchError(cluster.ErrPeerUnreachable),
			"the caller ran out of time; the peer never got a verdict")
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
	})

	It("blames the caller's deadline even when its cancellation has not landed yet", func() {
		// The same rule as the spec above, at the instant that makes it hard.
		//
		// A dial carries the caller's deadline down to the socket, so the
		// socket's timer and the context's cancellation timer fire at the same
		// moment and nothing orders them. The socket's error can be back in
		// Open before the scheduler has run the context's cancel func, and in
		// that window ctx.Err() is nil while the caller's budget is
		// unambiguously spent. Reading only ctx.Err() there reports a peer that
		// is listening and healthy as unreachable.
		//
		// That window is real: this spec's sibling above reproduces it under
		// `-race` about three runs in seven, which is exactly often enough to
		// be dismissed as noise. Here it is made deterministic instead, by
		// handing Open a context in precisely that state: deadline passed,
		// cancellation not delivered. Nothing is faked about the dial, which
		// runs for real against an address nothing is listening on.
		refused, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := refused.Addr().String()
		Expect(refused.Close()).To(Succeed())
		Expect(reg.Register(ctx, "peer-refusing", addr, "test")).To(Succeed())

		_, err = pool.Open(deadlinePassed{ctx}, "peer-refusing")
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(err).ToNot(MatchError(cluster.ErrPeerUnreachable),
			"the caller's budget was spent before the dial was made; blaming the peer for it is how an impatient client gets a healthy replica routed around")
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound))
	})

	It("dials once when many callers open the same peer at the same time", func() {
		// Without a per-peer lock held across the dial, every concurrent
		// caller races to dial and all but one of the resulting sessions is
		// dropped on the floor still holding a live WebSocket.
		const callers = 16
		streams := make(chan net.Conn, callers)
		var wg sync.WaitGroup
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				st, err := pool.Open(context.Background(), "peer-1")
				Expect(err).ToNot(HaveOccurred())
				streams <- st
			}()
		}
		wg.Wait()
		close(streams)

		count := 0
		for st := range streams {
			count++
			DeferCleanup(func(c net.Conn) { _ = c.Close() }, st)
		}
		Expect(count).To(Equal(callers))

		Eventually(accepted, "10s").Should(Receive())
		Consistently(accepted, "2s", "200ms").ShouldNot(Receive(),
			"concurrent opens must share one dial, not race to dial per caller")
	})

	It("refuses to open after Close and is safe to close twice", func() {
		pool.Close()
		pool.Close()

		_, err := pool.Open(ctx, "peer-1")
		Expect(err).To(HaveOccurred())
		Expect(err).ToNot(MatchError(cluster.ErrInstanceNotFound),
			"a locally closed pool says nothing about whether the node exists")
	})
})
