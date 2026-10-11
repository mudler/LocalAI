// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"net"
	"sync"
	"time"

	d "github.com/mudler/LocalAI/pkg/diagnostics"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	g "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type diagnosticServer struct {
	pb.UnimplementedBackendServer
	mu      sync.Mutex
	seen    []metadata.MD
	methods []string
}

func (s *diagnosticServer) capture(ctx context.Context, method string) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, md.Copy())
	s.methods = append(s.methods, method)
}
func (s *diagnosticServer) Predict(ctx context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	s.capture(ctx, "Predict")
	time.Sleep(15 * time.Millisecond)
	return &pb.Reply{Message: []byte("one")}, nil
}
func (s *diagnosticServer) Health(ctx context.Context, in *pb.HealthMessage) (*pb.Reply, error) {
	s.capture(ctx, "Health")
	return &pb.Reply{Message: []byte("OK")}, nil
}
func (s *diagnosticServer) ModelMetadata(ctx context.Context, in *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	s.capture(ctx, "ModelMetadata")
	return &pb.ModelMetadataResponse{}, nil
}
func (s *diagnosticServer) PredictStream(in *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	s.capture(stream.Context(), "PredictStream")
	if err := stream.SendHeader(metadata.Pairs("fixture", "headers")); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	for _, v := range []string{"one", "two"} {
		if err := stream.Send(&pb.Reply{Message: []byte(v)}); err != nil {
			return err
		}
	}
	return nil
}

var _ = Describe("Passive dispatch diagnostics", func() {
	It("preserves auth, call order, stream EOF and emits observations for each actual RPC", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server := g.NewServer()
		backend := &diagnosticServer{}
		pb.RegisterBackendServer(server, backend)
		go server.Serve(listener)
		DeferCleanup(server.Stop)
		var mu sync.Mutex
		var events []d.Event
		recorder := d.NewRecorder(func(e d.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
		ctx := recorder.Request(context.Background())
		original := metadata.Pairs("authorization", "custom", "localai-diagnostic-id", "untrusted")
		ctx = metadata.NewOutgoingContext(ctx, original)
		client := &Client{address: listener.Addr().String(), token: "secret"}
		_, err = client.ModelMetadata(ctx, &pb.ModelOptions{})
		Expect(err).NotTo(HaveOccurred())
		ok, err := client.HealthCheck(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		_, err = client.Predict(ctx, &pb.PredictOptions{})
		Expect(err).NotTo(HaveOccurred())
		var replies []string
		Expect(client.PredictStream(ctx, &pb.PredictOptions{}, func(r *pb.Reply) { replies = append(replies, string(r.Message)) })).To(Succeed())
		Expect(replies).To(Equal([]string{"one", "two"}))
		backend.mu.Lock()
		seen := append([]metadata.MD(nil), backend.seen...)
		methods := append([]string(nil), backend.methods...)
		backend.mu.Unlock()
		Expect(seen).To(HaveLen(4))
		Expect(methods).To(Equal([]string{"ModelMetadata", "Health", "Predict", "PredictStream"}))
		for _, md := range seen {
			Expect(md.Get("localai-diagnostic-id")).To(Equal([]string{d.RequestID(ctx)}))
			Expect(md.Get("authorization")).To(ContainElements("custom", "Bearer secret"))
		}
		Expect(original.Get("localai-diagnostic-id")).To(Equal([]string{"untrusted"}))
		Eventually(func() int {
			mu.Lock()
			defer mu.Unlock()
			n := 0
			for _, e := range events {
				if e.Phase == d.PhaseRPC && e.State == d.StateEnd {
					n++
				}
			}
			return n
		}).Should(Equal(4))
		mu.Lock()
		defer mu.Unlock()
		var streamSpan uint32
		for _, e := range events {
			if e.Phase == d.PhaseRPCPredictStream {
				streamSpan = e.Dispatch.Parent
			}
		}
		Expect(streamSpan).NotTo(BeZero())
		var headers, message time.Duration
		for _, e := range events {
			if e.Dispatch != nil && e.Dispatch.Span == streamSpan {
				if e.Phase == d.PhaseRPCHeaders {
					headers = e.Dispatch.Offset
				}
				if e.Phase == d.PhaseRPCFirstMessage {
					message = e.Dispatch.Offset
				}
			}
		}
		Expect(headers).To(BeNumerically(">", 0))
		Expect(message - headers).To(BeNumerically(">=", 30*time.Millisecond))
		connections := map[uint32]bool{}
		for _, e := range events {
			if e.Phase == d.PhaseTransportReady {
				connections[e.Dispatch.Connection] = true
			}
		}
		Expect(connections).To(HaveLen(4))
		for _, phase := range []d.Phase{d.PhaseRPCMetadata, d.PhaseRPCHealth, d.PhaseRPCPredict, d.PhaseRPCPredictStream} {
			count := 0
			for _, e := range events {
				if e.Phase == phase && e.State == d.StateEnd {
					count += e.Count
					Expect(e.Outcome).To(Equal(d.OutcomeOK))
				}
			}
			Expect(count).To(Equal(1))
		}
		for _, phase := range []d.Phase{d.PhaseRPCSent, d.PhaseRPCHeaders, d.PhaseRPCFirstMessage} {
			count := 0
			for _, e := range events {
				if e.Phase == phase {
					count++
				}
			}
			Expect(count).To(Equal(4))
		}
	})
})
