package tunnel_test

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// streamAndTCP returns a stream of a session (the stream end that Splice gets
// on the worker) with the other end of that stream, and a TCP connection with
// the other end of that connection.
func streamAndTCP() (far net.Conn, spliceStream net.Conn, spliceTCP, backend *net.TCPConn) {
	far, spliceStream, spliceTCP, backend, _, _ = streamAndTCPWithSessions()
	return
}

// streamAndTCPWithSessions is streamAndTCP that also returns the two sessions.
func streamAndTCPWithSessions() (far net.Conn, spliceStream net.Conn, spliceTCP, backend *net.TCPConn, frontend, worker *tunnel.Session) {
	GinkgoHelper()
	frontend, worker = sessionPair(tunnel.LaneInference)
	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := worker.AcceptStream()
		ch <- accepted{c, err}
	}()
	far, err := frontend.OpenStream(context.Background())
	Expect(err).ToNot(HaveOccurred())
	// A stream is announced to the other side with its first bytes.
	Expect(tunnel.WriteStreamAccepted(far)).To(Succeed())
	var got accepted
	Eventually(ch).Should(Receive(&got))
	Expect(got.err).ToNot(HaveOccurred())
	Expect(tunnel.ReadStreamReply(got.c)).To(Succeed())

	spliceTCP, backend = tcpPair()
	return far, got.c, spliceTCP, backend, frontend, worker
}

var _ = Describe("Splice", func() {
	It("carries both directions at the same time", func() {
		far, stream, local, backend := streamAndTCP()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(stream, local) }()

		// The backend speaks first, as a server of gRPC does after the preface.
		_, err := backend.Write([]byte("settings"))
		Expect(err).ToNot(HaveOccurred())
		_, err = far.Write([]byte("preface"))
		Expect(err).ToNot(HaveOccurred())

		got := make([]byte, 8)
		_, err = io.ReadFull(far, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("settings"))
		got = make([]byte, 7)
		_, err = io.ReadFull(backend, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("preface"))

		Expect(far.Close()).To(Succeed())
		_ = backend.Close()
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("keeps the reply of a request that ends in a half-close", func() {
		far, stream, local, backend := streamAndTCP()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(stream, local) }()

		// The backend reads the request up to its end and only then answers.
		go func() {
			defer GinkgoRecover()
			req, err := io.ReadAll(backend)
			Expect(err).ToNot(HaveOccurred())
			_, err = backend.Write(append([]byte("reply to "), req...))
			Expect(err).ToNot(HaveOccurred())
			Expect(backend.CloseWrite()).To(Succeed())
		}()

		_, err := far.Write([]byte("request"))
		Expect(err).ToNot(HaveOccurred())
		cw, ok := far.(interface{ CloseWrite() error })
		Expect(ok).To(BeTrue())
		Expect(cw.CloseWrite()).To(Succeed())

		reply, err := io.ReadAll(far)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply)).To(Equal("reply to request"))

		Expect(far.Close()).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("passes the half-close of the backend to the stream and keeps the request direction open", func() {
		far, stream, local, backend := streamAndTCP()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(stream, local) }()

		Expect(backend.CloseWrite()).To(Succeed())
		// The far side reads the end of the stream, and it can still write.
		_, err := io.ReadAll(far)
		Expect(err).ToNot(HaveOccurred())
		_, err = far.Write([]byte("late"))
		Expect(err).ToNot(HaveOccurred())
		got := make([]byte, 4)
		_, err = io.ReadFull(backend, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("late"))

		Expect(far.Close()).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("closes both streams when the session ends while a direction is still open", func() {
		// The frontend ends its side and the backend says nothing more. Then the
		// session goes away. The read from the stream is at its end and nothing
		// writes to the stream again, so the read from the backend would stay
		// parked for ever without the watch on the session.
		far, stream, local, backend, frontend, _ := streamAndTCPWithSessions()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(stream, local) }()

		Expect(far.(interface{ CloseWrite() error }).CloseWrite()).To(Succeed())
		// The backend reads the end of the request. The half-close has passed.
		_, err := io.ReadAll(backend)
		Expect(err).ToNot(HaveOccurred())
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive(), "the other direction is still open")

		Expect(frontend.Close()).To(Succeed())

		Eventually(done, 5*time.Second).Should(Receive(MatchError(ContainSubstring("session ended"))))
		// The backend sees its connection closed.
		_, err = backend.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred())
	})

	It("closes both streams when one end cannot half-close", func() {
		a1, a2 := net.Pipe()
		b1, b2 := net.Pipe()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(a2, b1) }()

		Expect(a1.Close()).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		// The other end was closed by Splice, so its peer sees the end.
		_, err := b2.Read(make([]byte, 1))
		Expect(err).To(MatchError(io.EOF))
	})

	It("closes both streams at once when a direction fails", func() {
		far, stream, local, backend := streamAndTCP()
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(stream, local) }()

		// Closing the whole session under a live stream is a failure of the
		// transport and not an end that was asked for.
		Expect(stream.Close()).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		_, err := backend.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred())
		_ = far.Close()
	})

	It("reports a stream that the peer reset in the middle of a request", func() {
		a, b := net.Pipe()
		boom := errors.New("link broke")
		done := make(chan error, 1)
		go func() { done <- tunnel.Splice(&failingConn{Conn: a, err: boom}, b) }()
		_, _ = b.Write([]byte("x"))
		Eventually(done, 5*time.Second).Should(Receive(MatchError(boom)))
	})
})

// failingConn fails every read after the first byte.
type failingConn struct {
	net.Conn
	err error
}

func (f *failingConn) Read(p []byte) (int, error) { return 0, f.err }
