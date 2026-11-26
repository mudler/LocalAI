package nodes

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// The load-budget hint names the knob that raises the budget. A deadline from
// the remote LoadModel usually arrives as a gRPC status, not as a wrapped
// context error, and the hint used to miss it.
var _ = Describe("LoadModel deadline hint", func() {
	route := func(loadErr error) error {
		reg := &fakeModelRouter{findAndLockErr: errors.New("not found")}
		reg.findIdleNode = &BackendNode{ID: "node-a", Name: "node-a", Address: "node-a:50051"}
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		backend := &stubBackend{healthResult: true, loadErr: loadErr}
		router := NewSmartRouter(reg, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}})
		_, err := router.Route(context.Background(), "model-x", "model-x.gguf", "llama-cpp", "", &pb.ModelOptions{Model: "model-x.gguf"}, false)
		return err
	}

	It("names the knob for a gRPC DeadlineExceeded status", func() {
		err := route(status.Error(codes.DeadlineExceeded, "context deadline exceeded"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagModelLoadTimeout))
	})

	It("names the knob for a wrapped context deadline", func() {
		err := route(context.DeadlineExceeded)
		Expect(err.Error()).To(ContainSubstring(config.FlagModelLoadTimeout))
	})

	It("does not add the hint to other failures", func() {
		err := route(status.Error(codes.Internal, "out of memory"))
		Expect(err.Error()).NotTo(ContainSubstring(config.FlagModelLoadTimeout))
	})
})
