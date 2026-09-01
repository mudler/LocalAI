// SPDX-License-Identifier: MIT

package cluster

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This spec is in-package because the header deadline it exercises is a
// production constant measured in seconds, and a spec that waited it out would
// be the slowest in the suite. The seam is unexported for the same reason the
// worker tunnel's is: it is a test knob, not an operator knob.
var _ = Describe("A peer stream that never says what it wants", func() {
	It("is refused rather than left holding a relay goroutine", func() {
		// Without a deadline on the opening frame, a peer that opens a stream
		// and then goes quiet parks a goroutine and a stream slot until the
		// whole session dies. A peer need not be malicious to do it: a dialler
		// killed between OpenStream and its first write leaves exactly this.
		relay := newRelay(NewTunnelRegistry(nil, "me"), 50*time.Millisecond, 0)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)

		a, b := net.Pipe()
		accepted, err := yamux.Server(a, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		peer, err := yamux.Client(b, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = peer.Close()
			_ = accepted.Close()
		})
		store.Accept("peer-1", accepted)

		stream, err := peer.OpenStream(GinkgoT().Context())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })

		// Read with no deadline of our own: what is being asserted is that the
		// RELAY answered, and a deadline here would be satisfied by a stream
		// left parked just as well.
		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		var reply error
		Eventually(replies, "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(ErrRelayRequestInvalid))

		ends := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := stream.Read(make([]byte, 1))
			ends <- err
		}()
		Eventually(ends, "10s").Should(Receive(HaveOccurred()))
	})
})

// backloggedPair returns a peer/relay session pair whose SYN backlog is one
// stream deep, so a single un-accepted open fills it and the next one parks.
// yamux's default is 256 (mux.go, DefaultConfig), and filling that from a spec
// would mean 256 real opens to prove one property.
func backloggedPair(backlog int) (dialled, accepted *yamux.Session) {
	GinkgoHelper()
	cfg := yamux.DefaultConfig()
	cfg.AcceptBacklog = backlog
	a, b := net.Pipe()
	var err error
	accepted, err = yamux.Server(a, cfg, nil)
	Expect(err).ToNot(HaveOccurred())
	dialled, err = yamux.Client(b, cfg, nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		_ = dialled.Close()
		_ = accepted.Close()
	})
	return dialled, accepted
}

// unwritableStream is a peer stream that delivers one relay request and then
// fails every write. It stands in for a peer that vanished between opening the
// stream and hearing the answer, which is the only way the acceptance reply
// fails, and which no pair of live yamux sessions can be made to do on cue.
type unwritableStream struct {
	net.Conn
	request  []byte
	read     int
	closed   chan struct{}
	closeOne sync.Once
}

func newUnwritableStream(nodeID string) *unwritableStream {
	GinkgoHelper()
	frame := &bytes.Buffer{}
	Expect(WriteRelayRequest(frame, nodeID, 0)).To(Succeed())
	return &unwritableStream{request: frame.Bytes(), closed: make(chan struct{})}
}

func (s *unwritableStream) Read(p []byte) (int, error) {
	if s.read >= len(s.request) {
		// Never EOF: an EOF here would end the relay for a reason other than
		// the failed write, and the spec would pass without exercising it.
		<-s.closed
		return 0, io.EOF
	}
	n := copy(p, s.request[s.read:])
	s.read += n
	return n, nil
}

func (s *unwritableStream) Write([]byte) (int, error) { return 0, errors.New("peer went away") }

func (s *unwritableStream) Close() error {
	s.closeOne.Do(func() { close(s.closed) })
	return nil
}

func (s *unwritableStream) SetReadDeadline(time.Time) error { return nil }

var _ = Describe("The relay's own budgets", func() {
	var (
		reg *Registry
		tun *TunnelRegistry
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db := testutil.SetupTestDB()
		Expect(Migrate(ctx, db)).To(Succeed())
		reg = NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		tun = NewTunnelRegistry(reg, "me")
	})

	It("stops bounding the stream once the relay hands it over", func() {
		// Both budgets are set to 50ms here and both are deliberately shorter
		// than the window this spec then watches. A header deadline left armed
		// past acceptance, or an open budget applied to the stream it produced,
		// would abort a relayed inference after 50ms of quiet, which in
		// production is the difference between a response that streams for an
		// hour and one that dies mid-token.
		relay := newRelay(tun, 50*time.Millisecond, 50*time.Millisecond)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)
		peer, accepted := backloggedPair(256)
		store.Accept("peer-1", accepted)

		worker, frontend := backloggedPair(256)
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		workerSide := make(chan net.Conn, 1)
		go func() {
			defer GinkgoRecover()
			stream, err := worker.AcceptStream()
			if err != nil {
				return
			}
			workerSide <- stream
		}()

		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		Expect(WriteRelayRequest(stream, "w1", 0)).To(Succeed())

		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		Eventually(replies, "10s").Should(Receive(BeNil()))

		var served net.Conn
		Eventually(workerSide, "10s").Should(Receive(&served))

		// One reader for both questions, so that watching for a teardown does
		// not eat the bytes the second half of the spec is waiting for.
		data := make(chan []byte, 4)
		ended := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			buf := make([]byte, 64)
			for {
				n, err := served.Read(buf)
				if n > 0 {
					data <- append([]byte(nil), buf[:n]...)
				}
				if err != nil {
					ended <- err
					return
				}
			}
		}()

		// An assertion about an event that must NOT happen, which is the one
		// kind a channel cannot replace: a torn-down splice ends the worker's
		// side, and there is no event for "still alive". The window is ten
		// times the budgets it is watching.
		Consistently(ended, "500ms", "50ms").ShouldNot(Receive(),
			"the relay tore the stream down on a budget that should have stopped applying at acceptance")

		// And it is not merely un-torn-down: it still carries bytes, long after
		// both budgets would have expired.
		_, err = stream.Write([]byte("late"))
		Expect(err).ToNot(HaveOccurred())
		Eventually(data, "10s").Should(Receive(Equal([]byte("late"))))
	})

	It("refuses rather than parking when the worker's tunnel will not take another stream", func() {
		// The open budget exists because yamux BLOCKS an open once the accept
		// backlog is full rather than failing it, so without a bound an
		// overloaded worker turns a refusable condition into a parked peer.
		relay := newRelay(tun, 0, 50*time.Millisecond)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)
		peer, accepted := backloggedPair(256)
		store.Accept("peer-1", accepted)

		_, frontend := backloggedPair(1)
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		// One un-accepted open fills the one-deep backlog; the relay's own open
		// is the one that has to wait.
		filler, err := frontend.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = filler.Close() })

		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		Expect(WriteRelayRequest(stream, "w1", 0)).To(Succeed())

		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		var reply error
		Eventually(replies, "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(ErrRelayUnavailable))
		// The tunnel IS held here, so this must not read as a routing fact.
		Expect(reply).ToNot(MatchError(ErrNotOwner))

		ends := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := stream.Read(make([]byte, 1))
			ends <- err
		}()
		Eventually(ends, "10s").Should(Receive(HaveOccurred()))
	})

	It("bounds its open by the caller's stated budget when that is the shorter", func() {
		// The ceiling here is 10s and the caller says it has 50ms. Without the
		// stated budget this replica would hold a relay goroutine and a yamux
		// stream slot for the full ceiling on behalf of a client that gave up
		// almost immediately.
		relay := newRelay(tun, 0, 10*time.Second)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)
		peer, accepted := backloggedPair(256)
		store.Accept("peer-1", accepted)

		_, frontend := backloggedPair(1)
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		// One un-accepted open fills the one-deep backlog, so the relay's own
		// open is the one that has to wait out a budget.
		filler, err := frontend.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = filler.Close() })

		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		Expect(WriteRelayRequest(stream, "w1", 50*time.Millisecond)).To(Succeed())

		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		var reply error
		// Two seconds is twenty times the stated budget and a fifth of the
		// ceiling, so only a relay that honoured the budget answers inside it.
		Eventually(replies, "2s").Should(Receive(&reply))
		Expect(reply).To(MatchError(ErrRelayUnavailable))
		// The tunnel IS held here. A budget running out must not turn into a
		// routing fact, and it must never become absence.
		Expect(reply).ToNot(MatchError(ErrNotOwner))
		Expect(reply).ToNot(MatchError(ErrNoConnection))
	})

	It("does not let a stated budget stretch its own ceiling", func() {
		// A patient client must not be able to park this replica. The ceiling
		// is 50ms and the caller says it will wait ten seconds; the refusal
		// still has to arrive on the ceiling.
		relay := newRelay(tun, 0, 50*time.Millisecond)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)
		peer, accepted := backloggedPair(256)
		store.Accept("peer-1", accepted)

		_, frontend := backloggedPair(1)
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())
		filler, err := frontend.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = filler.Close() })

		stream, err := peer.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		Expect(WriteRelayRequest(stream, "w1", 10*time.Second)).To(Succeed())

		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		var reply error
		Eventually(replies, "2s").Should(Receive(&reply))
		Expect(reply).To(MatchError(ErrRelayUnavailable))
	})

	It("closes the worker's stream when it cannot tell the peer the stream was accepted", func() {
		// The reply is the last thing that can fail after a worker stream has
		// been opened. A relay that gave up without closing it would leak one
		// stream on the worker per failed acceptance, and the worker cannot
		// tell those from live ones.
		relay := newRelay(tun, 0, 0)
		worker, frontend := backloggedPair(256)
		_, err := tun.Attach(ctx, "w1", frontend)
		Expect(err).ToNot(HaveOccurred())

		workerSide := make(chan net.Conn, 1)
		go func() {
			defer GinkgoRecover()
			stream, err := worker.AcceptStream()
			if err != nil {
				return
			}
			workerSide <- stream
		}()

		peerStream := newUnwritableStream("w1")
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			relay.Stream("peer-1", peerStream)
		}()
		Eventually(done, "10s").Should(BeClosed())

		var served net.Conn
		Eventually(workerSide, "10s").Should(Receive(&served))
		ended := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := served.Read(make([]byte, 1))
			ended <- err
		}()
		Eventually(ended, "10s").Should(Receive(HaveOccurred()),
			"the worker-side stream outlived the relay that opened it")
	})
})
