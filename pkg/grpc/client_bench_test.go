package grpc

import (
	"context"
	"net"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	gogrpc "google.golang.org/grpc"
)

// startBenchServer serves the dummy model on a loopback port. It does not use
// startTestServer, which registers Ginkgo cleanups that a benchmark cannot run.
func startBenchServer(b *testing.B) string {
	b.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	s := gogrpc.NewServer(serverOpts()...)
	pb.RegisterBackendServer(s, &server{llm: &dummyModel{}})
	go func() { _ = s.Serve(lis) }()
	b.Cleanup(s.Stop)
	return lis.Addr().String()
}

// BenchmarkClientHealthCheck measures one short unary call through a client
// that is reused across calls, as a loaded model's client is.
func BenchmarkClientHealthCheck(b *testing.B) {
	client := NewClient(startBenchServer(b), true, nil, false)
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if ok, err := client.HealthCheck(ctx); err != nil || !ok {
			b.Fatalf("health check: ok=%v err=%v", ok, err)
		}
	}
}

// BenchmarkClientHealthCheckParallel measures the same call with parallel
// requests, as a model with parallel slots receives them.
func BenchmarkClientHealthCheckParallel(b *testing.B) {
	client := NewClient(startBenchServer(b), true, nil, false)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, err := client.HealthCheck(ctx); err != nil || !ok {
				b.Errorf("health check: ok=%v err=%v", ok, err)
				return
			}
		}
	})
}
