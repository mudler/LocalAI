package model

import (
	"context"
	"errors"
	"net"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type workerAnswer struct{ error }

func (workerAnswer) IsBackendAnswer() bool { return true }

// A connection error during an inference shuts the model down on the worker and
// removes its rows. A dial that failed in the transport is a connection error to
// gRPC and says nothing about the backend.
var _ = Describe("ConnectionEvictingClient", func() {
	evictedBy := func(dialErr error) bool {
		GinkgoHelper()
		var evicted atomic.Bool
		inner := grpc.NewClientWithDialer("10.0.0.5:50051", false, nil, false, "", func(context.Context, string) (net.Conn, error) {
			return nil, dialErr
		})
		client := newConnectionEvictingClient(inner, "m", func() { evicted.Store(true) })
		_, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		return evicted.Load()
	}

	It("keeps the model when the transport could not get a stream to the backend", func() {
		Expect(evictedBy(errors.New("no tunnel is held for that node"))).To(BeFalse())
	})

	It("evicts the model when the worker answered that the backend is gone", func() {
		Expect(evictedBy(workerAnswer{errors.New("the worker could not reach the backend")})).To(BeTrue())
	})

	It("lets a caller see through it to the dial error of the client", func() {
		boom := errors.New("no route")
		inner := grpc.NewClientWithDialer("x:1", false, nil, false, "", func(context.Context, string) (net.Conn, error) { return nil, boom })
		_, _ = inner.Predict(context.Background(), &pb.PredictOptions{})
		client := newConnectionEvictingClient(inner, "m", func() {})
		Expect(errors.Is(grpc.LastDialErrorOf(client), boom)).To(BeTrue())
	})
})
