package grpc

import (
	"context"
	"net"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func startTestServer(token string) (addr string, stop func()) {
	old := os.Getenv(AuthTokenEnvVar)
	os.Setenv(AuthTokenEnvVar, token)
	DeferCleanup(func() { os.Setenv(AuthTokenEnvVar, old) })

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	opts := serverOpts()
	s := gogrpc.NewServer(opts...)
	pb.RegisterBackendServer(s, &server{llm: &dummyModel{}})
	go s.Serve(lis)
	DeferCleanup(func() { s.GracefulStop() })

	return lis.Addr().String(), func() {}
}

var _ = Describe("AuthInterceptor", func() {
	// Not parallel: tests mutate the LOCALAI_GRPC_AUTH_TOKEN env var which is process-global.

	DescribeTable("token authentication",
		func(serverToken, clientToken string, wantCode codes.Code, wantOK bool) {
			addr, _ := startTestServer(serverToken)

			client := &Client{address: addr, token: clientToken}
			ok, err := client.HealthCheck(context.Background())

			if wantOK {
				Expect(err).ToNot(HaveOccurred())
				Expect(ok).To(BeTrue())
			} else {
				Expect(err).To(HaveOccurred())
				st, ok := status.FromError(err)
				Expect(ok).To(BeTrue(), "expected gRPC status error")
				Expect(st.Code()).To(Equal(wantCode))
			}
		},
		Entry("no auth, any client passes", "", "", codes.OK, true),
		Entry("token set, no client rejected", "secret-token-123", "", codes.Unauthenticated, false),
		Entry("token set, correct passes", "secret-token-123", "secret-token-123", codes.OK, true),
		Entry("token set, wrong rejected", "secret-token-123", "wrong-token", codes.Unauthenticated, false),
	)

	It("authenticates Predict calls", func() {
		addr, _ := startTestServer("predict-token")

		client := &Client{address: addr, token: "predict-token", parallel: true}
		reply, err := client.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("ok"))

		noAuthClient := &Client{address: addr, parallel: true}
		_, err = noAuthClient.Predict(context.Background(), &pb.PredictOptions{})
		Expect(err).To(HaveOccurred())
	})

	It("rejects raw token without Bearer prefix", func() {
		addr, _ := startTestServer("secret-token")

		conn, err := gogrpc.NewClient("passthrough:///"+addr, gogrpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { conn.Close() })

		client := pb.NewBackendClient(conn)

		ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "secret-token")
		_, err = client.Health(ctx, &pb.HealthMessage{})

		Expect(err).To(HaveOccurred(), "expected raw token without Bearer prefix to be rejected (bug H7)")
		st, ok := status.FromError(err)
		Expect(ok).To(BeTrue(), "expected gRPC status error")
		Expect(st.Code()).To(Equal(codes.Unauthenticated))
	})

	It("rejects empty Bearer value", func() {
		addr, _ := startTestServer("secret-token")

		conn, err := gogrpc.NewClient("passthrough:///"+addr, gogrpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { conn.Close() })

		client := pb.NewBackendClient(conn)

		ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer ")
		_, err = client.Health(ctx, &pb.HealthMessage{})
		Expect(err).To(HaveOccurred(), "expected empty bearer value to be rejected")
	})
})
