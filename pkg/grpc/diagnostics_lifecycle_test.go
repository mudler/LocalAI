// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	d "github.com/mudler/LocalAI/pkg/diagnostics"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	g "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

type diagnosticEvents struct {
	mu     sync.Mutex
	events []d.Event
}

func (e *diagnosticEvents) add(v d.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, v)
}
func (e *diagnosticEvents) find(p d.Phase) []d.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []d.Event
	for _, v := range e.events {
		if v.Phase == p {
			out = append(out, v)
		}
	}
	return out
}

type countedListener struct {
	net.Listener
	accepted atomic.Int32
	delay    time.Duration
}

func (l *countedListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e == nil {
		l.accepted.Add(1)
		time.Sleep(l.delay)
	}
	return c, e
}

type statusDiagnosticServer struct {
	pb.UnimplementedBackendServer
	code codes.Code
}

func (s *statusDiagnosticServer) Predict(context.Context, *pb.PredictOptions) (*pb.Reply, error) {
	return nil, status.Error(s.code, "private error sentinel")
}

var _ = Describe("Passive dispatch lifecycle", func() {
	It("keeps lazy dialing and disabled metadata intact, with equal accepted connection counts", func() {
		raw, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		l := &countedListener{Listener: raw, delay: 20 * time.Millisecond}
		server := g.NewServer()
		backend := &diagnosticServer{}
		pb.RegisterBackendServer(server, backend)
		go server.Serve(l)
		DeferCleanup(server.Stop)
		client := &Client{address: l.Addr().String(), parallel: true}
		events := &diagnosticEvents{}
		base := d.NewRecorder(events.add).Request(context.Background())
		_, h := observeRPC(base)
		conn, err := client.dial(h.options()...)
		Expect(err).NotTo(HaveOccurred())
		Consistently(l.accepted.Load, 30*time.Millisecond).Should(BeZero())
		Expect(conn.Close()).To(Succeed())
		h.finish()
		for _, enabled := range []bool{false, true} {
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("localai-diagnostic-id", "unchanged", "authorization", "auth"))
			if enabled {
				ctx = d.NewRecorder(events.add).Request(ctx)
			}
			for i := 0; i < 2; i++ {
				_, err = client.Predict(ctx, &pb.PredictOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
			expected := int32(2)
			if enabled {
				expected = 4
			}
			Expect(l.accepted.Load()).To(Equal(expected))
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		Expect(backend.seen[0].Get("localai-diagnostic-id")).To(Equal([]string{"unchanged"}))
		Expect(backend.seen[0].Get("authorization")).To(Equal([]string{"auth"}))
		Eventually(func() int { return len(events.find(d.PhaseTransportReady)) }).Should(Equal(2))
		// ConnBegin may precede the peer's HTTP/2 handshake. The delayed
		// server acceptance must instead appear in RPC completion duration.
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(4))
		for _, v := range events.find(d.PhaseRPC) {
			if v.State == d.StateEnd {
				Expect(v.Elapsed).To(BeNumerically(">=", 20*time.Millisecond))
			}
		}
	})
	DescribeTable("categorizes actual gRPC status errors without exposing text", func(code codes.Code, outcome d.Outcome) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server := g.NewServer()
		pb.RegisterBackendServer(server, &statusDiagnosticServer{code: code})
		go server.Serve(l)
		DeferCleanup(server.Stop)
		events := &diagnosticEvents{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		client := &Client{address: l.Addr().String()}
		_, err = client.Predict(ctx, &pb.PredictOptions{})
		Expect(status.Code(err)).To(Equal(code))
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(2))
		Expect(events.find(d.PhaseRPC)[1].Outcome).To(Equal(outcome))
	}, Entry("canceled", codes.Canceled, d.OutcomeCanceled), Entry("deadline", codes.DeadlineExceeded, d.OutcomeCanceled), Entry("unavailable", codes.Unavailable, d.OutcomeError))
	It("reports refusal without inventing transport readiness", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := l.Addr().String()
		Expect(l.Close()).To(Succeed())
		events := &diagnosticEvents{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_, err = (&Client{address: address}).Predict(ctx, &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(2))
		Expect(events.find(d.PhaseTransportReady)).To(BeEmpty())
		Expect(events.find(d.PhaseDialAttempt)[0].Outcome).To(Equal(d.OutcomeSkipped))
	})
	It("flushes late producers after the caller and an earlier flush finish", func() {
		events := &diagnosticEvents{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		ctx, h := observeRPC(ctx)
		rpc := h.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: pb.Backend_Predict_FullMethodName})
		h.finish()
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(1))
		h.HandleConn(context.Background(), &stats.ConnBegin{})
		h.HandleRPC(rpc, &stats.InPayload{})
		h.HandleRPC(rpc, &stats.InPayload{})
		h.HandleRPC(rpc, &stats.End{})
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(2))
		Eventually(func() []d.Event { return events.find(d.PhaseRPCFirstMessage) }).Should(HaveLen(1))
		Eventually(func() []d.Event { return events.find(d.PhaseTransportReady) }).Should(HaveLen(1))
	})
	It("isolates parallel requests and preserves their scoped parents", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server := g.NewServer()
		pb.RegisterBackendServer(server, &diagnosticServer{})
		go server.Serve(l)
		DeferCleanup(server.Stop)
		client := &Client{address: l.Addr().String(), parallel: true}
		events := &diagnosticEvents{}
		recorder := d.NewRecorder(events.add)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				ctx := recorder.Request(context.Background())
				scope, end := d.Dispatch(ctx).NewAttempt().Start(d.PhaseRoute)
				defer end(d.OutcomeOK, 1)
				_, err := client.Predict(scope.Context(ctx), &pb.PredictOptions{})
				Expect(err).NotTo(HaveOccurred())
			}()
		}
		wg.Wait()
		Eventually(func() []d.Event { return events.find(d.PhaseRPC) }).Should(HaveLen(16))
		ids := map[string]bool{}
		for _, v := range events.find(d.PhaseRPC) {
			ids[v.ID] = true
			Expect(v.Dispatch.Parent).To(Equal(uint32(1)))
			Expect(v.Dispatch.Attempt).To(Equal(uint32(1)))
		}
		Expect(ids).To(HaveLen(8))
		Expect(events.find(d.PhaseClientLockWait)).To(BeEmpty())
	})
})
