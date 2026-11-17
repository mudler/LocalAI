package p2pnet_test

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// tcpPair returns both ends of a loopback TCP connection.
func tcpPair() (client, server *net.TCPConn) {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = l.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	s := <-accepted
	Expect(s).ToNot(BeNil())
	// Pipe closes the server end itself, so a second close may fail.
	DeferCleanup(func() { _ = c.Close() })
	DeferCleanup(func() { _ = s.Close() })
	return c.(*net.TCPConn), s.(*net.TCPConn)
}

var _ = Describe("Pipe", func() {
	It("passes a half-close on and still delivers the reply", func() {
		c1, s1 := tcpPair()
		c2, s2 := tcpPair()
		piped := make(chan error, 1)
		go func() { piped <- p2pnet.Pipe(s1, c2) }()

		go func() {
			defer GinkgoRecover()
			n, _ := io.Copy(io.Discard, s2)
			_, _ = fmt.Fprintf(s2, "got %d bytes", n)
			_ = s2.Close()
		}()

		_, err := c1.Write([]byte("hello"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c1.CloseWrite()).To(Succeed())
		reply, err := io.ReadAll(c1)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply)).To(Equal("got 5 bytes"))
		Eventually(piped, 5*time.Second).Should(Receive(BeNil()))
	})

	It("resets the stream when the other side fails", func() {
		server := newHost(p2pnet.Config{})
		client := newHost(p2pnet.Config{})
		pair(server, client)
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		const size = 64 << 10
		started := make(chan struct{})
		readErr := make(chan error, 1)
		serve(l, func(c net.Conn) {
			// Stream negotiation is lazy: wait for the data, so the reset
			// below reaches a stream the server already has.
			if _, err := io.ReadFull(c, make([]byte, size)); err != nil {
				readErr <- err
				return
			}
			close(started)
			_, err := io.Copy(io.Discard, c)
			readErr <- err
		})

		c1, s1 := tcpPair()
		stream, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		piped := make(chan error, 1)
		go func() { piped <- p2pnet.Pipe(s1, stream) }()

		_, err = io.CopyN(c1, rand.Reader, size)
		Expect(err).ToNot(HaveOccurred())
		Eventually(started, 5*time.Second).Should(BeClosed())
		// An abortive close: the TCP side sends a reset, not a FIN.
		Expect(c1.SetLinger(0)).To(Succeed())
		Expect(c1.Close()).To(Succeed())

		var err2 error
		Eventually(readErr, 5*time.Second).Should(Receive(&err2))
		Expect(err2).To(HaveOccurred(), "the far side saw a clean EOF instead of a reset")
		Eventually(piped, 5*time.Second).Should(Receive(HaveOccurred()))
	})
})
