package nodes

import (
	"errors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pixal3D distributed views", func() {
	It("stages each view, preserves order and releases all inputs", func(ctx SpecContext) {
		backend := &capturing3DBackend{}
		stager := &lifecycleStager{}
		client := NewFileStagingClient(backend, stager, "worker")
		paths := []string{"/tmp/front.png", "/tmp/right.png", "/tmp/back.png", "/tmp/left.png"}
		request := &pb.Generate3DRequest{Images: paths, MeshScale: 0.8}
		_, err := client.Generate3D(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(request.Images).To(Equal(paths))
		Expect(stager.ensureCalls).To(HaveLen(4))
		Expect(stager.releasedKeys).To(HaveLen(4))
		for i, path := range backend.request.Images {
			Expect(path).To(ContainSubstring("/remote/ephemeral/inputs/"))
			Expect(path).To(HaveSuffix(paths[i][5:]))
		}
		Expect(backend.request.MeshScale).To(Equal(0.8))
	})
	It("releases partial uploads without invoking the backend", func(ctx SpecContext) {
		backend := &capturing3DBackend{}
		stager := &lifecycleStager{ensureErr: errors.New("offline"), ensureErrAt: 3}
		client := NewFileStagingClient(backend, stager, "worker")
		_, err := client.Generate3D(ctx, &pb.Generate3DRequest{Images: []string{"/tmp/a", "/tmp/b", "/tmp/c", "/tmp/d"}})
		Expect(err).To(MatchError(ContainSubstring("offline")))
		Expect(stager.releasedKeys).To(HaveLen(3))
		Expect(backend.request).To(BeNil())
	})
})
