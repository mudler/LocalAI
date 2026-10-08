package grpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The dialer replaces the transport, and the client records why a dial failed.
// gRPC flattens a failure of the dialer into codes.Unavailable, the code of a
// backend that died, and the caller needs the difference to know whether to
// reap a row.
var _ = Describe("A client with its own dialer", func() {
	var addr string

	BeforeEach(func() {
		addr, _ = startTestServer("")
	})

	It("reaches the backend through the dialer and not through the address it is given", func() {
		var dials atomic.Int32
		client := NewClientWithDialer("a-name-that-resolves-nowhere:1", false, nil, false, "", func(ctx context.Context, _ string) (net.Conn, error) {
			dials.Add(1)
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		})

		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(dials.Load()).To(BeNumerically(">=", 1))
		Expect(LastDialErrorOf(client)).To(BeNil())
	})

	It("passes the address it was given to the dialer, as the name of the backend", func() {
		var got atomic.Value
		client := NewClientWithDialer("10.0.0.5:50051", false, nil, false, "", func(ctx context.Context, a string) (net.Conn, error) {
			got.Store(a)
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		})
		_, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Load()).To(Equal("10.0.0.5:50051"))
	})

	It("records the error of a dial that failed, with its value intact", func() {
		boom := errors.New("no route")
		client := NewClientWithDialer("10.0.0.5:50051", false, nil, false, "", func(context.Context, string) (net.Conn, error) {
			return nil, boom
		})
		ok, err := client.HealthCheck(context.Background())
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(LastDialErrorOf(client), boom)).To(BeTrue(), "gRPC flattens the error; the client keeps the value")
		Expect(errors.Is(TransportFailureOf(client), boom)).To(BeTrue())
	})

	It("reads a dial that has not finished as a failure of the transport, and clears it when the dial succeeds", func() {
		// gRPC hands the deadline of a call to the call and not to the dialer, so a
		// call can time out while the dial is still running. The caller then has no
		// error of the dial to read, and a slow transport looks like a dead backend.
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() { once.Do(func() { close(release) }) })
		// The dialer ignores its context, as a dial that is stuck in the middle of a
		// handshake does until the handshake ends.
		client := NewClientWithDialer("x:1", false, nil, false, "", func(_ context.Context, _ string) (net.Conn, error) {
			<-release
			var d net.Dialer
			return d.DialContext(context.Background(), "tcp", addr)
		})

		short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		ok, _ := client.HealthCheck(short)
		Expect(ok).To(BeFalse())
		Expect(TransportFailureOf(client)).ToNot(BeNil(), "the dial is still running; nothing is known about the backend")

		once.Do(func() { close(release) })
		Eventually(func() error { return TransportFailureOf(client) }, 5*time.Second).Should(BeNil())
		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("clears the error when the next dial succeeds", func() {
		var fail atomic.Bool
		fail.Store(true)
		client := NewClientWithDialer("x:1", false, nil, false, "", func(ctx context.Context, _ string) (net.Conn, error) {
			if fail.Load() {
				return nil, errors.New("down")
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		})
		_, _ = client.HealthCheck(context.Background())
		Expect(LastDialErrorOf(client)).ToNot(BeNil())

		fail.Store(false)
		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(LastDialErrorOf(client)).To(BeNil())
	})

	It("does not call the answer of a host a failure of the transport", func() {
		refusal := &answerErr{errors.New("the worker could not reach the backend")}
		client := NewClientWithDialer("x:1", false, nil, false, "", func(context.Context, string) (net.Conn, error) {
			return nil, refusal
		})
		_, _ = client.HealthCheck(context.Background())
		Expect(LastDialErrorOf(client)).To(MatchError(refusal), "the error is still recorded")
		Expect(TransportFailureOf(client)).To(BeNil(), "but it is evidence about the backend, like a refused connection")
	})

	It("sees the dial through a decorator that unwraps", func() {
		boom := errors.New("no route")
		inner := NewClientWithDialer("x:1", false, nil, false, "", func(context.Context, string) (net.Conn, error) { return nil, boom })
		_, _ = inner.HealthCheck(context.Background())
		outer := &decorator{Backend: inner}
		Expect(errors.Is(LastDialErrorOf(outer), boom)).To(BeTrue())
	})

	It("answers nil for a decorator that does not unwrap, and for a client with no dialer", func() {
		Expect(LastDialErrorOf(&undecorated{Backend: NewClient(addr, false, nil, false)})).To(BeNil())
		Expect(LastDialErrorOf(NewClient(addr, false, nil, false))).To(BeNil())
		Expect(LastDialErrorOf(nil)).To(BeNil())
	})

	It("stops walking a cycle of decorators", func() {
		a := &decorator{}
		a.Backend = a
		Expect(LastDialErrorOf(a)).To(BeNil())
	})

	It("keeps the bearer token on every call", func() {
		tokenAddr, _ := startTestServer("secret")
		client := NewClientWithDialer("x:1", false, nil, false, "secret", func(ctx context.Context, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", tokenAddr)
		})
		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})
})

type answerErr struct{ error }

func (*answerErr) IsBackendAnswer() bool { return true }

type decorator struct{ Backend }

func (d *decorator) Unwrap() Backend { return d.Backend }

type undecorated struct{ Backend }
