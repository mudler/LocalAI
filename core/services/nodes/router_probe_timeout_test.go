package nodes

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// slowHealthBackend answers HealthCheck only when the probe context ends, like
// a saturated backend whose health RPC queues behind running inference.
type slowHealthBackend struct {
	grpc.Backend
	err error // returned immediately when set, like a refused connection
}

func (b *slowHealthBackend) HealthCheck(ctx context.Context) (bool, error) {
	if b.err != nil {
		return false, b.err
	}
	<-ctx.Done()
	return false, ctx.Err()
}

// slowTransportBackend times out like slowHealthBackend, and its dial failed in
// the transport, like a worker that no tunnel reaches from this frontend.
type slowTransportBackend struct {
	slowHealthBackend
	dialErr error
}

func (b *slowTransportBackend) LastDialError() error { return b.dialErr }

var _ = Describe("SmartRouter.probeHealth under load", func() {
	probe := func(ctx context.Context, b grpc.Backend) probeVerdict {
		f := &recordingFactory{next: func() grpc.Backend { return b }}
		router := NewSmartRouter(&fakeModelRouter{}, SmartRouterOptions{ClientFactory: f})
		return router.probeHealth(ctx, &BackendNode{ID: "n1", Name: "node-1"}, "10.0.0.1:9001")
	}

	It("treats a probe that only timed out as a busy replica, not a dead one", func() {
		Expect(probe(context.Background(), &slowHealthBackend{})).To(Equal(probeAlive))
	})

	It("reads a timeout as unknown, not as a busy replica, when the transport failed", func() {
		Expect(probe(context.Background(), &slowTransportBackend{dialErr: errors.New("no tunnel is held for that node")})).To(Equal(probeUnknown))
	})

	It("still reports a refused connection as dead", func() {
		Expect(probe(context.Background(), &slowHealthBackend{err: errors.New("connection refused")})).To(Equal(probeDead))
	})

	It("does not report alive when the caller gave up", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		Expect(probe(ctx, &slowHealthBackend{})).To(Equal(probeDead))
	})
})
