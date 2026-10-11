// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	d "github.com/mudler/LocalAI/pkg/diagnostics"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	g "google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"net"
	"sync"
	"time"
)

type waitingDiagnosticServer struct {
	pb.UnimplementedBackendServer
	entered chan struct{}
}

func (s *waitingDiagnosticServer) Predict(ctx context.Context, _ *pb.PredictOptions) (*pb.Reply, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *waitingDiagnosticServer) PredictStream(_ *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	close(s.entered)
	<-stream.Context().Done()
	return stream.Context().Err()
}

var _ = Describe("Passive dispatch cancellation and ownership", func() {
	DescribeTable("ends actual canceled calls without first payload", func(stream bool, deadline bool) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		backend := &waitingDiagnosticServer{entered: make(chan struct{})}
		server := g.NewServer()
		pb.RegisterBackendServer(server, backend)
		go server.Serve(l)
		DeferCleanup(server.Stop)
		events := &diagnosticEvents{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
		} else {
			ctx, cancel = context.WithCancel(ctx)
		}
		defer cancel()
		done := make(chan error, 1)
		client := &Client{address: l.Addr().String()}
		go func() {
			if stream {
				done <- client.PredictStream(ctx, &pb.PredictOptions{}, func(*pb.Reply) {})
			} else {
				_, err := client.Predict(ctx, &pb.PredictOptions{})
				done <- err
			}
		}()
		Eventually(backend.entered).Should(BeClosed())
		if !deadline {
			cancel()
		}
		Eventually(done).Should(Receive(HaveOccurred()))
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(2))
		Expect(events.find(d.PhaseRPC)[1].Outcome).To(Equal(d.OutcomeCanceled))
		Expect(events.find(d.PhaseRPCFirstMessage)).To(BeEmpty())
		Expect(events.find(d.PhaseFrontendMessage)).To(BeEmpty())
	}, Entry("unary cancellation", false, false), Entry("stream cancellation", true, false), Entry("unary deadline", false, true), Entry("stream deadline", true, true))
	It("hands late events to a blocked active flush without blocking producers", func() {
		events := &diagnosticEvents{}
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		ctx := d.NewRecorder(func(e d.Event) { once.Do(func() { close(entered); <-release }); events.add(e) }).Request(context.Background())
		ctx, h := observeRPC(ctx)
		rpc := h.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: pb.Backend_Predict_FullMethodName})
		h.finish()
		Eventually(entered).Should(BeClosed())
		done := make(chan struct{})
		go func() { h.HandleRPC(rpc, &stats.InPayload{}); h.HandleRPC(rpc, &stats.End{}); close(done) }()
		Eventually(done).Should(BeClosed())
		close(release)
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(2))
		Eventually(func() []d.Event { return events.find(d.PhaseRPCFirstMessage) }).Should(HaveLen(1))
	})
	It("records mutex acquisition separately and flushes only after unlock", func() {
		events := &diagnosticEvents{}
		client := &Client{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		client.opMutex.Lock()
		done := make(chan struct{})
		go func() { unlock := client.diagnosticLock(ctx); unlock(); d.FlushDispatch(ctx); close(done) }()
		Consistently(done, 20*time.Millisecond).ShouldNot(BeClosed())
		client.opMutex.Unlock()
		Eventually(done).Should(BeClosed())
		found := events.find(d.PhaseClientLockWait)
		Expect(found).To(HaveLen(2))
		Expect(found[1].Elapsed).To(BeNumerically(">=", 20*time.Millisecond))
	})
})
