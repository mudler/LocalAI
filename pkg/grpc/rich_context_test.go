package grpc

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ctxBackend reports the context each rich call receives, so a spec can check
// that cancelling the caller reaches the backend.
type ctxBackend struct {
	base.SingleThread
	seen chan context.Context
}

func (b *ctxBackend) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	return b.PredictRichContext(context.Background(), opts)
}

func (b *ctxBackend) PredictStreamRich(opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	return b.PredictStreamRichContext(context.Background(), opts, out)
}

func (b *ctxBackend) PredictRichContext(ctx context.Context, _ *pb.PredictOptions) (*pb.Reply, error) {
	b.seen <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *ctxBackend) PredictStreamRichContext(ctx context.Context, _ *pb.PredictOptions, _ chan<- *pb.Reply) error {
	b.seen <- ctx
	<-ctx.Done()
	return ctx.Err()
}

var _ AIModelRichContext = (*ctxBackend)(nil)

var _ = Describe("AIModelRichContext dispatch", func() {
	// A backend that only saw context.Background would block forever here,
	// so bound each call and check the backend returned because of the cancel.
	expectCancelReaches := func(call func(ctx context.Context) error, seen chan context.Context) {
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- call(ctx) }()

		var got context.Context
		Eventually(seen).Should(Receive(&got))
		cancel()
		Eventually(got.Done(), 2*time.Second).Should(BeClosed())
		Eventually(errCh, 2*time.Second).Should(Receive(HaveOccurred()))
	}

	It("passes the caller's context to PredictStreamRichContext", func() {
		b := &ctxBackend{seen: make(chan context.Context, 1)}
		Provide("test://rich-ctx-stream", b)
		c := NewClient("test://rich-ctx-stream", true, nil, false)
		expectCancelReaches(func(ctx context.Context) error {
			return c.PredictStream(ctx, &pb.PredictOptions{}, func(*pb.Reply) {})
		}, b.seen)
	})

	It("passes the caller's context to PredictRichContext", func() {
		b := &ctxBackend{seen: make(chan context.Context, 1)}
		Provide("test://rich-ctx-predict", b)
		c := NewClient("test://rich-ctx-predict", true, nil, false)
		expectCancelReaches(func(ctx context.Context) error {
			_, err := c.Predict(ctx, &pb.PredictOptions{})
			return err
		}, b.seen)
	})
})
