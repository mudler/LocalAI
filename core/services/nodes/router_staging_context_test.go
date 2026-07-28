package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// cancelOnStageStager simulates the triggering HTTP request being abandoned
// (client disconnect, ingress idle-timeout) the moment a multi-GB file starts
// staging. It cancels the request context and records whether the context the
// stager itself received was cancelled as a result.
type cancelOnStageStager struct {
	fakeFileStager
	cancelRequest context.CancelFunc
	staged        bool
	ctxErrOnStage error
}

func (s *cancelOnStageStager) EnsureRemote(ctx context.Context, _, _, key string) (string, error) {
	s.staged = true
	// Mid-transfer: the client gives up on the (minutes-long) request.
	if s.cancelRequest != nil {
		s.cancelRequest()
	}
	// A multi-GB upload must survive this. If staging were bound to the
	// request context, ctx is now cancelled and the real HTTP stager would
	// abort with "context canceled" — exactly the production outage.
	s.ctxErrOnStage = ctx.Err()
	return "/remote/" + key, nil
}

var _ = Describe("Route cold-load staging context", func() {
	It("detaches staging from the request context so a client disconnect cannot abort a multi-GB transfer", func() {
		// A real model file so stageModelFiles actually calls the stager
		// (non-existent paths are skipped).
		tmp := GinkgoT().TempDir()
		modelFile := filepath.Join(tmp, "big.gguf")
		Expect(os.WriteFile(modelFile, []byte("weights"), 0o644)).To(Succeed())

		reg := &fakeModelRouter{
			findAndLockErr: errors.New("not loaded"),
			findIdleNode:   &BackendNode{ID: "n1", Name: "worker-1", Address: "10.0.0.1:50051"},
		}
		backend := &stubBackend{loadResult: &pb.Result{Success: true}}
		factory := &stubClientFactory{client: backend}
		unloader := &fakeUnloader{installReply: &messaging.BackendInstallReply{
			Success: true,
			Address: "10.0.0.1:9001",
		}}
		stager := &cancelOnStageStager{}

		router := NewSmartRouter(reg, SmartRouterOptions{
			Unloader:      unloader,
			ClientFactory: factory,
			FileStager:    stager,
			// DB nil: no advisory lock, exercises the same detached load ctx.
		})

		ctx, cancel := context.WithCancel(context.Background())
		stager.cancelRequest = cancel
		defer cancel()

		result, err := router.Route(ctx, "big-model", filepath.Join("models", "big.gguf"), "llama-cpp",
			&pb.ModelOptions{Model: "big.gguf", ModelFile: modelFile}, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(stager.staged).To(BeTrue(), "staging must have been attempted")
		Expect(stager.ctxErrOnStage).ToNot(HaveOccurred(),
			"staging context must survive cancellation of the triggering request")
	})
})
