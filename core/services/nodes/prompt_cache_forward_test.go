package nodes

import (
	"context"
	"net"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// recordingBackend is the backend process of a worker: it keeps the options
// of every request it gets.
type recordingBackend struct {
	pb.UnimplementedBackendServer
	mu   sync.Mutex
	seen []*pb.PredictOptions
}

func (r *recordingBackend) record(in *pb.PredictOptions) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, proto.Clone(in).(*pb.PredictOptions))
}

func (r *recordingBackend) last() *pb.PredictOptions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[len(r.seen)-1]
}

func (r *recordingBackend) Predict(_ context.Context, in *pb.PredictOptions) (*pb.Reply, error) {
	r.record(in)
	return &pb.Reply{Message: []byte("ok")}, nil
}

func (r *recordingBackend) PredictStream(in *pb.PredictOptions, stream pb.Backend_PredictStreamServer) error {
	r.record(in)
	return stream.Send(&pb.Reply{Message: []byte("ok")})
}

// The namespace of the disk prompt cache and the zero-retention flag are set
// by the frontend (core/backend) and must reach the backend process on the
// worker unchanged: the worker never derives them. These specs send a request
// through the client chain the router hands out (buildClientForAddr, then the
// in-flight tracking) over a real gRPC connection, on the direct dial and on a
// dialer as the tunnel uses.
var _ = Describe("the router forwards the prompt cache fields of a request", func() {
	var (
		backend *recordingBackend
		addr    string
	)

	BeforeEach(func() {
		backend = &recordingBackend{}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		srv := ggrpc.NewServer()
		pb.RegisterBackendServer(srv, backend)
		go func() { _ = srv.Serve(lis) }()
		DeferCleanup(srv.Stop)
		addr = lis.Addr().String()
	})

	factories := map[string]func() BackendClientFactory{
		"the direct dial": func() BackendClientFactory { return NewTokenClientFactory("") },
		"a dialer of the node, as the tunnel": func() BackendClientFactory {
			return NewDialerClientFactory("", func(string) func(context.Context, string) (net.Conn, error) {
				return func(ctx context.Context, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "tcp", addr)
				}
			})
		},
	}

	for name, factory := range factories {
		It("over "+name, func() {
			r := &SmartRouter{clientFactory: factory(), fileStager: &fakeFileStager{}}
			node := &BackendNode{ID: "n1", Name: "n1"}
			client := NewInFlightTrackingClient(r.buildClientForAddr(node, addr, false), &fakeInFlightTracker{}, "n1", "m", 0)

			for _, in := range []*pb.PredictOptions{
				{Prompt: "hi", PromptCacheSalt: "0123456789abcdef0123456789abcdef"},
				{Prompt: "hi", PromptCacheSalt: "fedcba9876543210fedcba9876543210", NoPromptCache: true},
			} {
				sent := proto.Clone(in).(*pb.PredictOptions)
				_, err := client.Predict(context.Background(), in)
				Expect(err).ToNot(HaveOccurred())
				Expect(backend.last().GetPromptCacheSalt()).To(Equal(sent.GetPromptCacheSalt()))
				Expect(backend.last().GetNoPromptCache()).To(Equal(sent.GetNoPromptCache()))

				err = client.PredictStream(context.Background(), in, func(*pb.Reply) {})
				Expect(err).ToNot(HaveOccurred())
				Expect(backend.last().GetPromptCacheSalt()).To(Equal(sent.GetPromptCacheSalt()))
				Expect(backend.last().GetNoPromptCache()).To(Equal(sent.GetNoPromptCache()))

				Expect(proto.Equal(in, sent)).To(BeTrue(), "the caller's options are not changed")
			}
		})
	}
})
