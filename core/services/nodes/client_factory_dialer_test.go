package nodes

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

var _ = Describe("The client factory with a dialer", func() {
	It("hands the dialer of the node to the client, and the node id selects it", func() {
		var asked []string
		boom := errors.New("no route to that node")
		factory := NewDialerClientFactory("", func(nodeID string) func(context.Context, string) (net.Conn, error) {
			asked = append(asked, nodeID)
			return func(context.Context, string) (net.Conn, error) { return nil, boom }
		})

		client := factory.NewClient("node-7", "127.0.0.1:50051", false)
		ok, err := client.HealthCheck(context.Background())
		Expect(ok).To(BeFalse())
		Expect(err).To(HaveOccurred())
		Expect(asked).To(Equal([]string{"node-7"}))
		Expect(errors.Is(grpc.TransportFailureOf(client), boom)).To(BeTrue())
	})

	It("reaches a backend that listens on a socket the dialer chose", func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = lis.Close() })
		stop := serveBackend(lis)
		DeferCleanup(stop)

		var dials atomic.Int32
		factory := NewDialerClientFactory("", func(string) func(context.Context, string) (net.Conn, error) {
			return func(ctx context.Context, addr string) (net.Conn, error) {
				dials.Add(1)
				Expect(addr).To(Equal("a-name-for-a-process:1"))
				var d net.Dialer
				return d.DialContext(ctx, "tcp", lis.Addr().String())
			}
		})
		client := factory.NewClient("n", "a-name-for-a-process:1", false)
		ok, err := client.HealthCheck(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(dials.Load()).To(BeNumerically(">=", 1))
	})
})

type healthServer struct{ pb.UnimplementedBackendServer }

func (healthServer) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

// serveBackend starts a backend that answers its health check on lis.
func serveBackend(lis net.Listener) func() {
	srv := ggrpc.NewServer()
	pb.RegisterBackendServer(srv, healthServer{})
	go func() { _ = srv.Serve(lis) }()
	return srv.Stop
}

var _ = Describe("The HTTP file stager without a route", func() {
	It("reports a missing dialer as ErrNoRoute and not as a panic", func() {
		stager := NewHTTPFileStager(func(string) (string, error) { return "w.worker.invalid", nil }, "", nil)
		_, err := stager.ListRemoteDir(context.Background(), "n", "k")
		Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "%v", err)
	})

	It("reports a node that has no dialer the same way", func() {
		stager := NewHTTPFileStager(func(string) (string, error) { return "w.worker.invalid", nil }, "", func(string) func(context.Context, string, string) (net.Conn, error) { return nil })
		_, err := stager.ListRemoteDir(context.Background(), "n", "k")
		Expect(errors.Is(err, ErrNoRoute)).To(BeTrue(), "%v", err)
	})

	It("reports a dialer that finds no route as that error, with the chain intact", func() {
		stager := NewHTTPFileStager(func(string) (string, error) { return "w.worker.invalid", nil }, "", func(string) func(context.Context, string, string) (net.Conn, error) {
			return func(context.Context, string, string) (net.Conn, error) { return nil, ErrNoRoute }
		})
		_, err := stager.ListRemoteDir(context.Background(), "n", "k")
		Expect(errors.Is(err, ErrNoRoute)).To(BeTrue())
	})
})

var _ = Describe("The HTTP file stager and a node that left", func() {
	It("closes the idle streams of the node and dials again after ForgetNode", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"files":[]}`))
		}))
		DeferCleanup(srv.Close)
		var dials atomic.Int32
		stager := NewHTTPFileStager(func(string) (string, error) { return strings.TrimPrefix(srv.URL, "http://"), nil }, "",
			func(string) func(context.Context, string, string) (net.Conn, error) {
				return func(ctx context.Context, _, _ string) (net.Conn, error) {
					dials.Add(1)
					var d net.Dialer
					return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
				}
			})
		for range 3 {
			_, err := stager.ListRemoteDir(context.Background(), "n", "k")
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(dials.Load()).To(Equal(int32(1)))

		stager.ForgetNode("n")
		_, err := stager.ListRemoteDir(context.Background(), "n", "k")
		Expect(err).ToNot(HaveOccurred())
		Eventually(dials.Load, time.Second).Should(Equal(int32(2)))

		var none *HTTPFileStager
		none.ForgetNode("n")
	})
})
