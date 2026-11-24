package p2pnet_test

import (
	"crypto/rand"
	"io"
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

const protoTest protocol.ID = "/localai-test/echo/1"

var _ = Describe("Admission", func() {
	var server, client *p2pnet.Host
	var gate *p2pnet.Allowlist

	BeforeEach(func() {
		gate = p2pnet.NewAllowlist()
		server = newHost(p2pnet.Config{Gate: gate})
		client = newHost(p2pnet.Config{})
		client.Libp2p().Peerstore().AddAddrs(server.ID(), server.Libp2p().Addrs(), time.Hour)
	})

	It("drops an unpaired peer after the handshake, before any stream", func() {
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		accepted := make(chan net.Conn, 1)
		serve(l, func(c net.Conn) { accepted <- c })

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(exchange(c, err)).To(HaveOccurred())
		Expect(server.Libp2p().Network().ConnsToPeer(client.ID())).To(BeEmpty())
		Consistently(accepted, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("cuts a live transfer when the peer is revoked", func() {
		gate.Allow(client.ID())
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		serve(l, func(c net.Conn) { _, _ = io.Copy(c, rand.Reader) })

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		_, err = io.ReadFull(c, make([]byte, 1<<20))
		Expect(err).ToNot(HaveOccurred())

		server.Revoke(client.ID())
		Expect(gate.Allowed(client.ID())).To(BeFalse())
		done := make(chan error, 1)
		go func() { _, err := io.Copy(io.Discard, c); done <- err }()
		Eventually(done, 5*time.Second).Should(Receive())

		c, err = client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(exchange(c, err)).To(HaveOccurred())
	})

	It("admits an unpaired peer only on the pairing protocol while pairing is open", func() {
		gate.SetPairing(true)
		lh, err := server.Listen(p2pnet.ProtoHTTP, nil)
		Expect(err).ToNot(HaveOccurred())
		serve(lh, echo)
		lo, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		other := make(chan net.Conn, 1)
		serve(lo, func(c net.Conn) { other <- c })

		c, err := client.Dial(dialCtx(), server.ID(), p2pnet.ProtoHTTP)
		Expect(err).ToNot(HaveOccurred())
		_, err = c.Write([]byte("hi"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 2)
		_, err = io.ReadFull(c, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("hi"))

		c2, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(exchange(c2, err)).To(HaveOccurred())
		Consistently(other, 200*time.Millisecond).ShouldNot(Receive())

		gate.SetPairing(false)
		c3, err := client.Dial(dialCtx(), server.ID(), p2pnet.ProtoHTTP)
		Expect(exchange(c3, err)).To(HaveOccurred())
	})

	It("applies the listener's own admit function after the gate", func() {
		gate.Allow(client.ID())
		l, err := server.Listen(protoTest, func(p peer.ID) bool { return false })
		Expect(err).ToNot(HaveOccurred())
		serve(l, echo)
		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(exchange(c, err)).To(HaveOccurred())
	})
})

// exchange writes and reads one byte on a dialed connection. Stream
// negotiation is lazy, so a refused stream may fail only at the first read.
func exchange(c net.Conn, dialErr error) error {
	if dialErr != nil {
		return dialErr
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("x")); err != nil {
		return err
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Read(make([]byte, 1))
	return err
}
