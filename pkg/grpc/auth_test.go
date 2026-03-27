package grpc

import (
	"net"
	"testing"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
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
		t.Setenv(AuthTokenEnvVar, token)
	} else {
		t.Setenv(AuthTokenEnvVar, "")
	}

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

func TestAuthInterceptor(t *testing.T) {
	// Not parallel: subtests mutate the LOCALAI_GRPC_AUTH_TOKEN env var which is process-global.
	tests := []struct {
		name        string
		serverToken string
		clientToken string
		wantCode    codes.Code
		wantOK      bool
	}{
		{"no auth, any client passes", "", "", codes.OK, true},
		{"token set, no client rejected", "secret-token-123", "", codes.Unauthenticated, false},
		{"token set, correct passes", "secret-token-123", "secret-token-123", codes.OK, true},
		{"token set, wrong rejected", "secret-token-123", "wrong-token", codes.Unauthenticated, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, stop := startTestServer(t, tt.serverToken)
			t.Cleanup(stop)

			client := &Client{address: addr, token: tt.clientToken}
			ok, err := client.HealthCheck(t.Context())

			if tt.wantOK {
				if err != nil {
					t.Fatalf("health check should succeed: %v", err)
				}
				if !ok {
					t.Fatal("health check should return true")
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("expected gRPC status error, got: %v", err)
				}
				if st.Code() != tt.wantCode {
					t.Fatalf("expected %v, got: %v", tt.wantCode, st.Code())
				}
			}
		})
	}
}

func TestAuthInterceptor_Predict(t *testing.T) {
	// Not parallel: mutates LOCALAI_GRPC_AUTH_TOKEN env var.
	addr, stop := startTestServer(t, "predict-token")
	t.Cleanup(stop)

	// Unary call with correct token
	client := &Client{address: addr, token: "predict-token", parallel: true}
	reply, err := client.Predict(t.Context(), &pb.PredictOptions{})
	if err != nil {
		t.Fatalf("predict should succeed with correct token: %v", err)
	}
	if string(reply.Message) != "ok" {
		t.Fatalf("unexpected reply: %v", reply.Message)
	}

	// Unary call without token should fail
	noAuthClient := &Client{address: addr, parallel: true}
	_, err = noAuthClient.Predict(t.Context(), &pb.PredictOptions{})
	if err == nil {
		t.Fatal("predict should fail without token")
	}
}

func TestAuthInterceptor_RawTokenWithoutBearer(t *testing.T) {
	addr, stop := startTestServer(t, "secret-token")
	t.Cleanup(stop)

	// Connect without token credentials
	conn, err := gogrpc.Dial(addr, gogrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	// Send the raw token without "Bearer " prefix in metadata
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "secret-token")
	_, err = client.Health(ctx, &pb.HealthMessage{})

	// This SHOULD be rejected (token sent without Bearer prefix)
	// BUG H7: currently this passes because TrimPrefix("secret-token", "Bearer ") returns "secret-token" unchanged
	if err == nil {
		t.Fatal("expected raw token without Bearer prefix to be rejected, but it was accepted (bug H7)")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got: %v", st.Code())
	}
}

func TestAuthInterceptor_EmptyBearerValue(t *testing.T) {
	addr, stop := startTestServer(t, "secret-token")
	t.Cleanup(stop)

	conn, err := gogrpc.Dial(addr, gogrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewBackendClient(conn)

	// Send "Bearer " with no actual token
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer ")
	_, err = client.Health(ctx, &pb.HealthMessage{})

	if err == nil {
		t.Fatal("expected empty bearer value to be rejected")
	}
}
