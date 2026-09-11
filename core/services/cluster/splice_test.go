package cluster_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-yamux/v5"

	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Splice", func() {
	// pipePair returns two connected in-memory conns.
	newPair := func() (net.Conn, net.Conn) { return net.Pipe() }

	It("copies bytes in both directions", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		go func() {
			_, _ = aLeft.Write([]byte("ping"))
		}()
		buf := make([]byte, 4)
		Expect(bRight.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, err := io.ReadFull(bRight, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))

		go func() {
			_, _ = bRight.Write([]byte("pong"))
		}()
		Expect(aLeft.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, err = io.ReadFull(aLeft, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("pong"))

		Expect(aLeft.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())
	})

	It("returns when one side closes, and closes the other", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		Expect(aLeft.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive(BeNil()))

		// The far side must have been closed too, so a read there fails
		// rather than blocking forever. The read runs in a goroutine and is
		// polled instead of carrying a read deadline: net.Pipe refuses to set
		// a deadline once *either* end is closed, so a deadline here would
		// fail exactly when Splice did its job.
		reads := make(chan error, 1)
		go func() {
			_, err := bRight.Read(make([]byte, 1))
			reads <- err
		}()
		var err error
		Eventually(reads, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, io.EOF)).To(BeTrue())
	})

	It("returns when both sides close", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		Expect(aLeft.Close()).To(Succeed())
		Expect(bRight.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())
	})

	// The three specs above only ever tear down an idle splice: at the moment
	// of Close no copy is parked inside a Write. A relayed inference response
	// is the opposite case, a reader that walks away mid-body while 50MB is
	// still being pushed at it, so this covers the direction that is blocked
	// in Write rather than in Read when its peer disappears.
	It("returns when the reader disappears while a write is in flight", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		// Nothing ever reads from bRight, so the a->b direction parks inside
		// Write on an unbuffered pipe with the payload half-delivered.
		payload := make([]byte, 1<<20)
		writes := make(chan error, 1)
		go func() {
			_, err := aLeft.Write(payload)
			writes <- err
		}()

		Expect(bRight.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())

		// The abandoned writer must be released as well, and only Splice
		// closing its end can do that: no deadline is set on aLeft, so a
		// splice that forgot to close would leave this write parked forever.
		Eventually(writes, "5s").Should(Receive(HaveOccurred()))
	})
	// net.Pipe can only ever end in EOF or a closed pipe, so the error half of
	// the contract needs a stream that can be told how to fail.
	Context("when a stream fails rather than closing", func() {
		errBoom := errors.New("transport exploded")

		It("reports a genuine transport error", func() {
			failing := &scriptedStream{readErr: errBoom}
			idle := &scriptedStream{}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(failing, idle) }()

			var err error
			Eventually(done, "5s").Should(Receive(&err))
			Expect(errors.Is(err, errBoom)).To(BeTrue())

			// Closed exactly once each: a second Close is what makes a yamux
			// stream complain about a teardown that went fine.
			Expect(failing.closes()).To(Equal(int32(1)))
			Expect(idle.closes()).To(Equal(int32(1)))
		})

		It("reports a genuine failure from its own Close", func() {
			failing := &scriptedStream{closeErr: errBoom}
			idle := &scriptedStream{}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(idle, failing) }()

			Expect(idle.Close()).To(Succeed())
			var err error
			Eventually(done, "5s").Should(Receive(&err))
			Expect(errors.Is(err, errBoom)).To(BeTrue())
		})

		// Every one of these means "a stream we were copying through was
		// closed". The yamux entries are the teardown Splice itself provokes:
		// none of them matches net.ErrClosed, so each has to be classified by
		// name or a normal relayed request ends up reported as a failure.
		DescribeTable("treats a closed stream as normal termination",
			func(ending error) {
				ended := &scriptedStream{readErr: ending}
				idle := &scriptedStream{}

				done := make(chan error, 1)
				go func() { done <- cluster.Splice(ended, idle) }()

				Eventually(done, "5s").Should(Receive(BeNil()))
			},
			Entry("EOF", io.EOF),
			Entry("a closed socket", net.ErrClosed),
			Entry("a closed in-memory pipe", io.ErrClosedPipe),
			Entry("a closed yamux stream", yamux.ErrStreamClosed),
			Entry("a reset yamux stream", yamux.ErrStreamReset),
			Entry("a shut-down yamux session", yamux.ErrSessionShutdown),
			// The LOCAL forms of the two endings the relay settled below. They
			// stay normal because they are the teardown this side asked for,
			// and Remote is the only thing separating them from the endings a
			// peer inflicts.
			Entry("a stream this side reset", &yamux.StreamError{ErrorCode: 0, Remote: false}),
			Entry("a go-away this side sent", &yamux.GoAwayError{ErrorCode: 0, Remote: false}),
		)

		// sessionDeath is the exact shape Session.close hands every live
		// stream when the session dies for a non-go-away reason
		// (session.go:330). It matters that these are wrapped: the cause it
		// carries is routinely io.EOF or a closed socket, so a classifier that
		// looked at the cause would call a vanished peer a clean ending.
		sessionDeath := func(cause error) error {
			return fmt.Errorf("%w: connection closed: %w", yamux.ErrStreamReset, cause)
		}

		// A dead peer under a relayed inference request has to reach the
		// caller. If it arrives as nil, a failed request looks like a finished
		// one and nothing upstream retries or logs it.
		DescribeTable("reports the session dying under a stream",
			func(ending error) {
				dead := &scriptedStream{readErr: ending}
				idle := &scriptedStream{}

				done := make(chan error, 1)
				go func() { done <- cluster.Splice(dead, idle) }()

				var err error
				Eventually(done, "5s").Should(Receive(&err))
				Expect(err).To(MatchError(ending))
			},
			Entry("a keepalive timeout", sessionDeath(yamux.ErrKeepAliveTimeout)),
			Entry("a broken connection", sessionDeath(errors.New("read tcp 10.0.0.1:4000: broken pipe"))),
			Entry("a peer that vanished", sessionDeath(io.EOF)),
			Entry("a protocol-error go-away", &yamux.GoAwayError{Remote: true, ErrorCode: 1}),
			Entry("an internal-error go-away", &yamux.GoAwayError{Remote: true, ErrorCode: 2}),
			// The two endings phase 1 left open and the relay, Splice's first
			// production caller, settled as failures. Both truncate whatever
			// was in flight, and reporting them as normal termination is how a
			// half-finished inference comes to look like a short one that
			// completed. See isMuxFailure for why the decision could not be
			// left to a caller reading Splice's result.
			Entry("a stream the peer reset", &yamux.StreamError{ErrorCode: 1, Remote: true}),
			Entry("a graceful go-away from the peer", yamux.ErrRemoteGoAway),
		)

		// A bare io.EOF can only reach Splice from a failing Write. io.Copy
		// never surfaces a clean read-side EOF, and yamux hands out the raw
		// cause rather than the wrapped one when a Write or Close races
		// Session.close's shutdown window (session.go:507-510), so for a
		// vanished peer this IS the dead session, arriving unwrapped.
		It("reports a write that fails with a bare EOF", func() {
			sink := &scriptedStream{writeErr: io.EOF}
			source := &scriptedStream{feeds: true}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(sink, source) }()

			var err error
			Eventually(done, "5s").Should(Receive(&err))
			Expect(err).To(MatchError(io.EOF))
		})

		// The distinction the classifier turns on, in one spec: yamux uses the
		// same sentinel for "this stream was reset", which Splice provokes
		// itself and must stay quiet about, and as the head of the wrapped
		// error meaning "the session died", which it must report. Only
		// identity separates them.
		It("separates a bare reset from a session that died wrapping one", func() {
			spliceEnding := func(ending error) error {
				done := make(chan error, 1)
				go func() {
					done <- cluster.Splice(&scriptedStream{readErr: ending}, &scriptedStream{})
				}()
				var err error
				EventuallyWithOffset(1, done, "5s").Should(Receive(&err))
				return err
			}

			Expect(spliceEnding(yamux.ErrStreamReset)).To(BeNil())
			Expect(spliceEnding(sessionDeath(yamux.ErrKeepAliveTimeout))).ToNot(BeNil())
		})

		// Session death also arrives through the Close Splice makes itself, on
		// a stream whose session died while the other side was finishing. That
		// is not the quiet teardown ErrSessionShutdown describes.
		It("reports a session that died, even from its own Close", func() {
			stream := &scriptedStream{closeErr: sessionDeath(yamux.ErrKeepAliveTimeout)}
			backend := &scriptedStream{readErr: io.EOF}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(stream, backend) }()

			var err error
			Eventually(done, "5s").Should(Receive(&err))
			Expect(err).To(MatchError(yamux.ErrKeepAliveTimeout))
		})

		// The tunnel's own teardown: the local backend finishes normally while
		// the yamux session has already gone away, so the FIN that Splice's
		// Close writes fails. Nothing went wrong and nothing may be reported.
		It("does not report a shut-down session on its own Close", func() {
			stream := &scriptedStream{closeErr: yamux.ErrSessionShutdown}
			backend := &scriptedStream{readErr: io.EOF}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(stream, backend) }()

			Eventually(done, "5s").Should(Receive(BeNil()))
		})

		// Everything above feeds Splice a synthesized error. This one drives a
		// real yamux session, because the shapes a live library produces are
		// not always the ones its source suggests: the bug this spec was added
		// alongside was a race inside Session.close that no synthesized error
		// could show. It asserts only that a dead session is reported, not how
		// it is spelled, since which of the two forms arrives is a race.
		It("reports a real yamux session dying under a live stream", func() {
			clientConn, serverConn := net.Pipe()
			client, err := yamux.Client(clientConn, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			server, err := yamux.Server(serverConn, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				_ = client.Close()
				_ = server.Close()
			})

			accepted := make(chan *yamux.Stream, 1)
			go func() {
				defer GinkgoRecover()
				far, err := server.AcceptStream()
				if err != nil {
					close(accepted)
					return
				}
				accepted <- far
			}()

			stream, err := client.OpenStream(context.Background())
			Expect(err).ToNot(HaveOccurred())
			// Push a byte so the stream is established on both sides before
			// the session is killed.
			_, err = stream.Write([]byte("x"))
			Expect(err).ToNot(HaveOccurred())
			var far *yamux.Stream
			Eventually(accepted, "10s").Should(Receive(&far))
			// Deadline so a stream that never carries the byte fails this spec
			// instead of parking the suite until its own timeout.
			Expect(far.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
			_, err = far.Read(make([]byte, 1))
			Expect(err).ToNot(HaveOccurred())

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(stream, &scriptedStream{}) }()

			// The peer's process disappears: the connection carrying the
			// session goes away, which kills every stream riding on it.
			Expect(serverConn.Close()).To(Succeed())

			var spliceErr error
			Eventually(done, "10s").Should(Receive(&spliceErr))
			Expect(spliceErr).To(HaveOccurred())
		})

		// The anti-leak guarantee, which the pipe specs cannot see because
		// their parked copy is released too quickly to catch Splice in the
		// act. Waking a copy is asynchronous on a real stream (yamux's Close
		// notifies the reader, which then has to be scheduled), so this stream
		// splits the two: Close records itself, and the spec decides when the
		// parked Read actually returns.
		It("does not return until the second direction has finished", func() {
			parked := &scriptedStream{holdReadPastClose: true}
			ending := &scriptedStream{readErr: io.EOF}

			done := make(chan error, 1)
			go func() { done <- cluster.Splice(ending, parked) }()

			Eventually(parked.closes, "5s").Should(Equal(int32(1)))
			Consistently(done, "200ms").ShouldNot(Receive())

			parked.release()
			Eventually(done, "5s").Should(Receive(BeNil()))
		})
	})
})

// scriptedStream is an io.ReadWriteCloser whose endings the spec dictates, so
// Splice can be fed failures no in-memory pipe can produce. With no readErr it
// parks in Read until Close, standing in for an idle half of a live stream.
type scriptedStream struct {
	readErr  error
	writeErr error
	closeErr error
	// feeds makes Read produce bytes instead of parking, so a spec can keep a
	// direction copying until its destination fails.
	feeds bool
	// holdReadPastClose keeps a parked Read blocked until release is called,
	// standing in for the gap between a Close waking a reader and that reader
	// running. Without it, Close releases the Read as a real stream does.
	holdReadPastClose bool

	releaseOnce sync.Once
	released    chan struct{}
	initOnce    sync.Once
	closeN      atomic.Int32
}

func (s *scriptedStream) gate() chan struct{} {
	s.initOnce.Do(func() { s.released = make(chan struct{}) })
	return s.released
}

func (s *scriptedStream) release() {
	gate := s.gate()
	s.releaseOnce.Do(func() { close(gate) })
}

func (s *scriptedStream) Read(p []byte) (int, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	if s.feeds {
		select {
		case <-s.gate():
			return 0, io.EOF
		default:
			return len(p), nil
		}
	}
	<-s.gate()
	return 0, io.EOF
}

func (s *scriptedStream) Write(p []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	return len(p), nil
}

func (s *scriptedStream) Close() error {
	s.closeN.Add(1)
	if !s.holdReadPastClose {
		s.release()
	}
	return s.closeErr
}

func (s *scriptedStream) closes() int32 { return s.closeN.Load() }
