package tunnel_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Request and reply between replicas", func() {
	const maxBytes = 1 << 20

	var (
		replies *tunnel.Replies
		open    func(context.Context, string) (net.Conn, error)
	)

	// serveWith starts the replica "owner" on one side of a session and returns
	// the opener of the other side, as a peer link carries a request.
	serveWith := func(relay *tunnel.Relay) func(context.Context, string) (net.Conn, error) {
		GinkgoHelper()
		caller, owner := sessionPair(tunnel.LaneBulk)
		go func() {
			for {
				st, err := owner.AcceptStream()
				if err != nil {
					return
				}
				go relay.Stream("caller", st)
			}
		}()
		return func(ctx context.Context, _ string) (net.Conn, error) { return caller.OpenStream(ctx) }
	}

	BeforeEach(func() {
		replies = tunnel.NewReplies("owner", maxBytes)
		open = serveWith(tunnel.NewRelay(nil, tunnel.WithReplies(replies)))
	})

	echo := func(prefix string) tunnel.ReplyHandler {
		return func(_ context.Context, data []byte) ([]byte, error) { return append([]byte(prefix), data...), nil }
	}

	ask := func(ctx context.Context, target, subject string, data []byte) ([]byte, error) {
		return tunnel.RequestThrough(ctx, open, target, subject, data, maxBytes, 0)
	}

	It("sends the request to the handler of its subject and returns the reply", func() {
		replies.Handle("chats.replay.a", echo("a:"))
		replies.Handle("chats.replay.b", echo("b:"))
		out, err := ask(context.Background(), "owner", "chats.replay.b", []byte("x"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("b:x"))
	})

	It("carries an empty body and a body of the whole bound", func() {
		replies.Handle("s", echo(""))
		out, err := ask(context.Background(), "owner", "s", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
		big := []byte(strings.Repeat("z", maxBytes))
		out, err = ask(context.Background(), "owner", "s", big)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(maxBytes))
	})

	It("goes to the newest handler of a subject, and to the older one when the newest is removed", func() {
		replies.Handle("s", echo("old:"))
		remove := replies.Handle("s", echo("new:"))
		out, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("new:x"))
		remove()
		remove()
		out, err = ask(context.Background(), "owner", "s", []byte("x"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("old:x"))
	})

	It("answers that nothing serves a subject without a handler, which proves that nothing ran", func() {
		_, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrNoResponder)).To(BeTrue(), "got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeTrue())
	})

	It("refuses a request for another replica as not the owner, and runs nothing", func() {
		ran := make(chan struct{}, 1)
		replies.Handle("s", func(context.Context, []byte) ([]byte, error) { ran <- struct{}{}; return nil, nil })
		_, err := ask(context.Background(), "someone-else", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrNotOwner)).To(BeTrue(), "got %v", err)
		Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse())
		Expect(tunnel.NotStarted(err)).To(BeTrue())
		Consistently(ran, 100*time.Millisecond).ShouldNot(Receive())
	})

	It("reads a refusal of a replica that serves no request and reply as unsupported, and not as absence", func() {
		open = serveWith(tunnel.NewRelay(nil))
		_, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrRequestUnsupported)).To(BeTrue(), "got %v", err)
		Expect(errors.Is(err, tunnel.ErrNoResponder)).To(BeFalse())
		Expect(tunnel.NotStarted(err)).To(BeTrue())
	})

	It("reports a handler that failed as an outcome that is not known", func() {
		replies.Handle("s", func(context.Context, []byte) ([]byte, error) { return nil, errors.New("store down") })
		_, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrHandlerFailed)).To(BeTrue(), "got %v", err)
		Expect(err.Error()).To(ContainSubstring("store down"))
		Expect(tunnel.NotStarted(err)).To(BeFalse())
	})

	It("reports a handler that panicked as a failed handler, and the replica keeps serving", func() {
		replies.Handle("s", func(context.Context, []byte) ([]byte, error) { panic("boom") })
		_, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrHandlerFailed)).To(BeTrue(), "got %v", err)
		replies.Handle("t", echo(""))
		_, err = ask(context.Background(), "owner", "t", []byte("x"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("cancels the handler when the caller gives up, by closing the stream", func() {
		started, cancelled := make(chan struct{}), make(chan error, 1)
		replies.Handle("s", func(ctx context.Context, _ []byte) ([]byte, error) {
			close(started)
			<-ctx.Done()
			cancelled <- ctx.Err()
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		errc := make(chan error, 1)
		go func() {
			_, err := ask(ctx, "owner", "s", []byte("x"))
			errc <- err
		}()
		Eventually(started).Should(BeClosed())
		cancel()
		var err error
		Eventually(errc).Should(Receive(&err))
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got %v", err)
		Expect(errors.Is(err, tunnel.ErrReplyLost)).To(BeTrue())
		Expect(tunnel.NotStarted(err)).To(BeFalse(), "the request was written: the handler may have run")
		Eventually(cancelled, 5*time.Second).Should(Receive(MatchError(context.Canceled)))
	})

	It("gives the handler the budget of the caller, and ends it when the budget runs out", func() {
		deadlines, ended := make(chan time.Duration, 1), make(chan error, 1)
		replies.Handle("s", func(ctx context.Context, _ []byte) ([]byte, error) {
			d, ok := ctx.Deadline()
			Expect(ok).To(BeTrue())
			deadlines <- time.Until(d)
			<-ctx.Done()
			ended <- ctx.Err()
			return nil, ctx.Err()
		})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, err := ask(ctx, "owner", "s", []byte("x"))
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeFalse())
		var left time.Duration
		Eventually(deadlines).Should(Receive(&left))
		Expect(left).To(BeNumerically("<=", 300*time.Millisecond))
		Eventually(ended, 2*time.Second).Should(Receive())
	})

	It("does not send a request whose budget is spent, nor one above the bound", func() {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := ask(ctx, "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrRequestNotSent)).To(BeTrue())
		_, err = ask(context.Background(), "owner", "s", make([]byte, maxBytes+1))
		Expect(errors.Is(err, tunnel.ErrPayloadTooLarge)).To(BeTrue())
		Expect(tunnel.NotStarted(err)).To(BeTrue())
	})

	It("is refused by a replica whose bound is lower, before any handler runs", func() {
		small := tunnel.NewReplies("owner", 4)
		small.Handle("s", echo(""))
		open = serveWith(tunnel.NewRelay(nil, tunnel.WithReplies(small)))
		_, err := ask(context.Background(), "owner", "s", []byte("too long"))
		Expect(errors.Is(err, tunnel.ErrPayloadTooLarge)).To(BeTrue(), "got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeTrue())
	})

	It("tells a request that never left from one whose reply was lost", func() {
		down := errors.New("dial refused")
		_, err := tunnel.RequestThrough(context.Background(), func(context.Context, string) (net.Conn, error) { return nil, down }, "owner", "s", []byte("x"), maxBytes, 0)
		Expect(errors.Is(err, tunnel.ErrRequestNotSent)).To(BeTrue())
		Expect(errors.Is(err, down)).To(BeTrue())
		Expect(tunnel.NotStarted(err)).To(BeTrue())

		// A replica that reads the whole request and hangs up without a reply.
		_, err = tunnel.RequestThrough(context.Background(), func(context.Context, string) (net.Conn, error) {
			near, far := net.Pipe()
			go func() {
				buf := make([]byte, 64)
				_, _ = io.ReadAtLeast(far, buf, 1)
				_ = far.Close()
			}()
			return near, nil
		}, "owner", "s", []byte("x"), maxBytes, 0)
		Expect(errors.Is(err, tunnel.ErrReplyLost)).To(BeTrue(), "got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeFalse())
	})

	It("does not reserve the declared size of a body before its bytes arrive", func() {
		const declared = 64 << 20
		// A peer that declares the largest body and sends none of it.
		header := binary.BigEndian.AppendUint32(nil, declared)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := tunnel.ReadBody(bytes.NewReader(header), declared)
		runtime.ReadMemStats(&after)
		Expect(errors.Is(err, io.ErrUnexpectedEOF)).To(BeTrue(), "got %v", err)
		Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 1<<20))

		body := []byte(strings.Repeat("b", 300<<10))
		out, err := tunnel.ReadBody(bytes.NewReader(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...)), declared)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal(body))
	})

	It("refuses the requests of a peer above its share and above the bound of the replica, as requests that ran nothing", func() {
		replies.SetLimits(2, 3)
		hold, entered := make(chan struct{}), make(chan struct{}, 8)
		DeferCleanup(func() { close(hold) })
		replies.Handle("s", func(context.Context, []byte) ([]byte, error) {
			entered <- struct{}{}
			<-hold
			return []byte("ok"), nil
		})
		// A second peer link to the same replica, as another replica holds.
		relay := tunnel.NewRelay(nil, tunnel.WithReplies(replies))
		other := func() func(context.Context, string) (net.Conn, error) {
			caller, owner := sessionPair(tunnel.LaneBulk)
			go func() {
				for {
					st, err := owner.AcceptStream()
					if err != nil {
						return
					}
					go relay.Stream("other", st)
				}
			}()
			return func(ctx context.Context, _ string) (net.Conn, error) { return caller.OpenStream(ctx) }
		}()
		open = serveWith(relay)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		for range 2 {
			go func() { _, _ = ask(ctx, "owner", "s", []byte("x")) }()
		}
		Eventually(entered).Should(Receive())
		Eventually(entered).Should(Receive())

		_, err := ask(context.Background(), "owner", "s", []byte("x"))
		Expect(errors.Is(err, tunnel.ErrReplicaBusy)).To(BeTrue(), "the share of one peer: got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeTrue())

		go func() { _, _ = tunnel.RequestThrough(ctx, other, "owner", "s", []byte("x"), maxBytes, 0) }()
		Eventually(entered).Should(Receive(), "another peer still has room")
		_, err = tunnel.RequestThrough(context.Background(), other, "owner", "s", []byte("x"), maxBytes, 0)
		Expect(errors.Is(err, tunnel.ErrReplicaBusy)).To(BeTrue(), "the bound of the replica: got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeTrue())
		Consistently(entered, 100*time.Millisecond).ShouldNot(Receive())

		cancel()
		Eventually(func() error {
			_, err := tunnel.RequestThrough(context.Background(), other, "owner", "t", []byte("x"), maxBytes, 0)
			return err
		}, 5*time.Second).Should(MatchError(tunnel.ErrNoResponder), "the slots are given back")
	})

	It("reports a reply above the bound as a handler that ran, wherever the handler runs", func() {
		big := func(context.Context, []byte) ([]byte, error) { return make([]byte, maxBytes+1), nil }
		replies.Handle("s", big)
		check := func(err error) {
			GinkgoHelper()
			Expect(errors.Is(err, tunnel.ErrHandlerFailed)).To(BeTrue(), "got %v", err)
			Expect(errors.Is(err, tunnel.ErrReplyTooLarge)).To(BeTrue(), "got %v", err)
			Expect(errors.Is(err, tunnel.ErrPayloadTooLarge)).To(BeFalse(), "the request was not too large: %v", err)
			Expect(tunnel.NotStarted(err)).To(BeFalse())
		}

		_, err := replies.Dispatch(context.Background(), "s", nil)
		check(err)
		_, err = ask(context.Background(), "owner", "s", nil)
		check(err)

		// A replica whose bound is higher than the bound of the caller.
		roomy := tunnel.NewReplies("owner", 2*maxBytes)
		roomy.Handle("s", big)
		open = serveWith(tunnel.NewRelay(nil, tunnel.WithReplies(roomy)))
		_, err = ask(context.Background(), "owner", "s", nil)
		check(err)
	})

	It("gives up opening the stream after its share of the budget, as a request that was not sent", func() {
		hang := func(ctx context.Context, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		began := time.Now()
		_, err := tunnel.RequestThrough(ctx, hang, "owner", "s", []byte("x"), maxBytes, 100*time.Millisecond)
		Expect(time.Since(began)).To(BeNumerically("<", 5*time.Second))
		Expect(errors.Is(err, tunnel.ErrRequestNotSent)).To(BeTrue(), "got %v", err)
		Expect(tunnel.NotStarted(err)).To(BeTrue())
		Expect(ctx.Err()).ToNot(HaveOccurred())

		// The share bounds the open only: a handler may use the rest of the budget.
		replies.Handle("slow", func(context.Context, []byte) ([]byte, error) {
			time.Sleep(300 * time.Millisecond)
			return []byte("done"), nil
		})
		out, err := tunnel.RequestThrough(ctx, open, "owner", "slow", nil, maxBytes, 100*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("done"))
	})

	It("dispatches a request for this replica in the process", func() {
		replies.Handle("s", echo("here:"))
		out, err := replies.Dispatch(context.Background(), "s", []byte("x"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal("here:x"))
		_, err = replies.Dispatch(context.Background(), "t", nil)
		Expect(errors.Is(err, tunnel.ErrNoResponder)).To(BeTrue())
	})
})
