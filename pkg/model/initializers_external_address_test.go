package model

import (
	"context"
	"net"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

// addressBackend stands in for an external backend that is already running at
// an address, as configured in external_backends.json.
type addressBackend struct{ pb.UnimplementedBackendServer }

func (addressBackend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}

func (addressBackend) LoadModel(context.Context, *pb.ModelOptions) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

var _ = Describe("address-based external backends", func() {
	It("registers the loaded model with the watchdog", func() {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		server := grpc.NewServer()
		pb.RegisterBackendServer(server, addressBackend{})
		go func() { _ = server.Serve(lis) }()
		DeferCleanup(server.Stop)

		loader := NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}})
		wd := NewWatchDog(WithProcessManager(loader), WithLRULimit(1))
		loader.SetWatchDog(wd)
		options := NewOptions(WithLoadGRPCLoadModelOpts(&pb.ModelOptions{}))

		address := lis.Addr().String()
		_, err = loader.spawnGRPCModel("external", address, options, "model-a", "model-a", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(wd.GetState().AddressModelMap).To(Equal(map[string]string{address: "model-a"}))
	})
})
