package grpc

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// dummyModel is a minimal AIModel implementation for testing.
type dummyModel struct{ base.Base }

func (d *dummyModel) Predict(opts *pb.PredictOptions) (string, error) {
	return "ok", nil
}

func startTestServer(t *testing.T, token string) (addr string, stop func()) {
	t.Helper()
	if token != "" {
		os.Setenv(AuthTokenEnvVar, token)
	} else {
		os.Unsetenv(AuthTokenEnvVar)
	}
	t.Cleanup(func() { os.Unsetenv(AuthTokenEnvVar) })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	opts := serverOpts()
	s := gogrpc.NewServer(opts...)
	pb.RegisterBackendServer(s, &server{llm: &dummyModel{}})
	go s.Serve(lis)

	return lis.Addr().String(), func() {
		s.GracefulStop()
	}
}

func TestAuthInterceptor_NoToken_AllowsAll(t *testing.T) {
	addr, stop := startTestServer(t, "")
	defer stop()

	client := &Client{address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, err := client.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health check should succeed without token: %v", err)
	}
	if !ok {
		t.Fatal("health check should return true")
	}
}

func TestAuthInterceptor_WithToken_RejectsUnauthenticated(t *testing.T) {
	addr, stop := startTestServer(t, "secret-token-123")
	defer stop()

	client := &Client{address: addr}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.HealthCheck(ctx)
	if err == nil {
		t.Fatal("expected error for unauthenticated call")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got: %v", st.Code())
	}
}

func TestAuthInterceptor_WithToken_AcceptsCorrectToken(t *testing.T) {
	addr, stop := startTestServer(t, "secret-token-123")
	defer stop()

	client := &Client{address: addr, token: "secret-token-123"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ok, err := client.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health check should succeed with correct token: %v", err)
	}
	if !ok {
		t.Fatal("health check should return true")
	}
}

func TestAuthInterceptor_WithToken_RejectsWrongToken(t *testing.T) {
	addr, stop := startTestServer(t, "secret-token-123")
	defer stop()

	client := &Client{address: addr, token: "wrong-token"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.HealthCheck(ctx)
	if err == nil {
		t.Fatal("expected error for wrong token")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got: %v", st.Code())
	}
}

func TestAuthInterceptor_Predict(t *testing.T) {
	addr, stop := startTestServer(t, "predict-token")
	defer stop()

	// Unary call with correct token
	client := &Client{address: addr, token: "predict-token", parallel: true}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reply, err := client.Predict(ctx, &pb.PredictOptions{})
	if err != nil {
		t.Fatalf("predict should succeed with correct token: %v", err)
	}
	if string(reply.Message) != "ok" {
		t.Fatalf("unexpected reply: %v", reply.Message)
	}

	// Unary call without token should fail
	noAuthClient := &Client{address: addr, parallel: true}
	_, err = noAuthClient.Predict(ctx, &pb.PredictOptions{})
	if err == nil {
		t.Fatal("predict should fail without token")
	}
}
