package cluster_test

import (
	"io"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// yamuxPair returns a client and a server session over an in-memory pipe. It
// stands in for a dialled peer link: everything the store does with a session
// is transport-agnostic, and the WebSocket half is covered where it is used.
func yamuxPair() (client *yamux.Session, server *yamux.Session) {
	GinkgoHelper()
	a, b := net.Pipe()
	var err error
	server, err = yamux.Server(a, nil, nil)
	Expect(err).ToNot(HaveOccurred())
	client, err = yamux.Client(b, nil, nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

// refusalDeadline bounds how long a refused stream may take to end. A refusal
// is one frame from a peer that already decided, so anything near this is the
// hang it exists to detect.
const refusalDeadline = 2 * time.Second

var _ = Describe("Accepted peer sessions", func() {
	It("accepts and refuses a stream rather than leaving the peer parked", func() {
		// yamux only acknowledges a stream once the far side accepts it, so a
		// store that held the session without accepting would not fail a peer's
		// Open, it would hang it, and every relayed request behind it.
		store := cluster.NewSessionStore(nil)
		DeferCleanup(store.CloseAll)
		client, server := yamuxPair()
		store.Accept("peer-1", server)

		stream, err := client.OpenStream(GinkgoT().Context())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })

		// The deadline is short and is NOT the thing being asserted: yamux
		// reports a deadline as ErrTimeout, and requiring an ending instead
		// (EOF from the peer's Close, or a reset) is what separates "refused"
		// from "parked". An earlier version asserted only that some error
		// arrived, which a parked stream satisfies just as well.
		Expect(stream.SetReadDeadline(time.Now().Add(refusalDeadline))).To(Succeed())
		_, err = stream.Read(make([]byte, 1))
		Expect(err).To(SatisfyAny(MatchError(io.EOF), MatchError(yamux.ErrStreamReset)),
			"a refused stream must END within %s; %v means the peer accepted it and then left it parked", refusalDeadline, err)
	})

	It("hands a stream to the relay when one is installed", func() {
		streams := make(chan net.Conn, 1)
		store := cluster.NewSessionStore(func(_ string, stream net.Conn) { streams <- stream })
		DeferCleanup(store.CloseAll)
		client, server := yamuxPair()
		store.Accept("peer-1", server)

		stream, err := client.OpenStream(GinkgoT().Context())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })

		var relayed net.Conn
		Eventually(streams, "10s").Should(Receive(&relayed))
		go func() {
			defer GinkgoRecover()
			_, _ = stream.Write([]byte("hello"))
		}()
		buf := make([]byte, 5)
		Expect(relayed.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
		_, err = relayed.Read(buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("hello"))
	})

	It("replaces a peer's link when it dials again, and closes the one it lost", func() {
		// A peer only re-dials because its previous link is gone from where it
		// stands. Keeping both would leave a session nothing can be routed to,
		// since the store holds one per peer.
		store := cluster.NewSessionStore(nil)
		DeferCleanup(store.CloseAll)
		_, first := yamuxPair()
		_, second := yamuxPair()

		store.Accept("peer-1", first)
		store.Accept("peer-1", second)

		held, ok := store.Get("peer-1")
		Expect(ok).To(BeTrue())
		Expect(held).To(BeIdenticalTo(second))
		Eventually(first.IsClosed, "10s").Should(BeTrue())
		Expect(second.IsClosed()).To(BeFalse(), "the link the peer is actually using was dropped")
	})

	It("forgets a session that ended, without evicting the one that replaced it", func() {
		store := cluster.NewSessionStore(nil)
		DeferCleanup(store.CloseAll)
		client, server := yamuxPair()
		store.Accept("peer-1", server)

		Expect(client.Close()).To(Succeed())
		Eventually(func() bool {
			_, ok := store.Get("peer-1")
			return ok
		}, "10s").Should(BeFalse())
	})

	It("closes every held link on shutdown, and refuses to store one afterwards", func() {
		store := cluster.NewSessionStore(nil)
		_, server := yamuxPair()
		store.Accept("peer-1", server)

		store.CloseAll()
		Eventually(server.IsClosed, "10s").Should(BeTrue())

		_, late := yamuxPair()
		store.Accept("peer-late", late)
		_, ok := store.Get("peer-late")
		Expect(ok).To(BeFalse())
		Eventually(late.IsClosed, "10s").Should(BeTrue(),
			"a dial racing shutdown must not be left believing it holds a live link")
	})
})
