package grpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var errGenCancelled = errors.New("generation cancelled")

// cancellableBackend implements AIModel + AIModelRich + Cancellable. Its
// rich predict paths optionally block until Cancel fires (blockUntilCancel),
// which lets the specs prove the server's context.AfterFunc plumbing: a
// cancelled request context must reach Cancel and unblock the generation.
type cancellableBackend struct {
	base.SingleThread

	blockUntilCancel bool

	started     chan struct{} // closed when a predict call is in flight
	startOnce   sync.Once
	cancelled   chan struct{} // closed by Cancel
	cancelOnce  sync.Once
	cancelCalls atomic.Int32
}

func newCancellableBackend(blockUntilCancel bool) *cancellableBackend {
	return &cancellableBackend{
		blockUntilCancel: blockUntilCancel,
		started:          make(chan struct{}),
		cancelled:        make(chan struct{}),
	}
}

func (c *cancellableBackend) Cancel() {
	c.cancelCalls.Add(1)
	c.cancelOnce.Do(func() { close(c.cancelled) })
}

func (c *cancellableBackend) run() error {
	c.startOnce.Do(func() { close(c.started) })
	if !c.blockUntilCancel {
		return nil
	}
	select {
	case <-c.cancelled:
		return errGenCancelled
	case <-time.After(30 * time.Second):
		// Backstop so a regression (Cancel never wired) fails the spec
		// instead of hanging the suite.
		return errors.New("cancellableBackend: Cancel never fired")
	}
}

func (c *cancellableBackend) PredictRich(*pb.PredictOptions) (*pb.Reply, error) {
	if err := c.run(); err != nil {
		return nil, err
	}
	return &pb.Reply{Message: []byte("done")}, nil
}

func (c *cancellableBackend) PredictStreamRich(_ *pb.PredictOptions, out chan<- *pb.Reply) error {
	out <- &pb.Reply{Message: []byte("first")}
	return c.run()
}

func (c *cancellableBackend) Predict(*pb.PredictOptions) (string, error) {
	return "", errors.New("cancellableBackend: legacy Predict should not have been called")
}

func (c *cancellableBackend) PredictStream(*pb.PredictOptions, chan string) error {
	return errors.New("cancellableBackend: legacy PredictStream should not have been called")
}

var _ AIModelRich = (*cancellableBackend)(nil)
var _ Cancellable = (*cancellableBackend)(nil)

var _ = Describe("Cancellable capability", func() {
	It("PredictStream: cancelling the request context fires Cancel and ends the stream with the backend's error", func() {
		backend := newCancellableBackend(true)
		addr := "test://cancel-stream"
		Provide(addr, backend)
		c := NewClient(addr, true, nil, false)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			errCh <- c.PredictStream(ctx, &pb.PredictOptions{}, func(*pb.Reply) {})
		}()

		// Only cancel once the generation is provably in flight; cancelling
		// earlier would race the AfterFunc registration in the server.
		Eventually(backend.started, "5s").Should(BeClosed())
		cancel()

		var err error
		Eventually(errCh, "5s").Should(Receive(&err))
		Expect(err).To(MatchError(errGenCancelled))
		Expect(backend.cancelCalls.Load()).To(BeNumerically(">=", 1))
	})

	It("Predict: cancelling the request context fires Cancel and unblocks the call", func() {
		backend := newCancellableBackend(true)
		addr := "test://cancel-predict"
		Provide(addr, backend)
		c := NewClient(addr, true, nil, false)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := c.Predict(ctx, &pb.PredictOptions{})
			errCh <- err
		}()

		Eventually(backend.started, "5s").Should(BeClosed())
		cancel()

		var err error
		Eventually(errCh, "5s").Should(Receive(&err))
		Expect(err).To(MatchError(errGenCancelled))
		Expect(backend.cancelCalls.Load()).To(BeNumerically(">=", 1))
	})

	It("does not call Cancel when the request completes normally", func() {
		backend := newCancellableBackend(false)
		addr := "test://cancel-clean"
		Provide(addr, backend)
		c := NewClient(addr, true, nil, false)

		ctx, cancel := context.WithCancel(context.Background())

		var replies []*pb.Reply
		err := c.PredictStream(ctx, &pb.PredictOptions{}, func(r *pb.Reply) {
			replies = append(replies, r)
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(replies).To(HaveLen(1))

		// Cancelling AFTER completion must not reach the backend: the
		// deferred AfterFunc stop de-registered the hook, so a shared or
		// reused context cannot abort someone else's later generation.
		cancel()
		Consistently(backend.cancelCalls.Load, "200ms").Should(BeZero())
	})
})
