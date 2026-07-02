package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// These tests cover shared-models mode (LOCALAI_DISTRIBUTED_SHARED_MODELS): when
// every node mounts the same models directory at the same path, the router must
// NOT stage model files to workers. The canonical absolute path is already valid
// on the worker, so staging would only re-download what is already present
// (#10556).
var _ = Describe("stageModelFiles shared-models mode", func() {
	var (
		stager  *fakeFileStager
		node    *BackendNode
		tmp     string
		gguf    string
		modelID = "ornith-1.0-35b"
	)

	BeforeEach(func() {
		stager = &fakeFileStager{}
		node = &BackendNode{ID: "node-1", Name: "node-1", Address: "10.0.0.1:50051"}
		tmp = GinkgoT().TempDir()

		modelDir := filepath.Join(tmp, "models", "llama-cpp", "models")
		Expect(os.MkdirAll(modelDir, 0o755)).To(Succeed())
		gguf = filepath.Join(modelDir, "ornith.gguf")
		Expect(os.WriteFile(gguf, []byte("weights"), 0o644)).To(Succeed())
	})

	It("does not stage and keeps the canonical absolute ModelFile when shared-models is enabled", func() {
		router := &SmartRouter{
			fileStager:     stager,
			stagingTracker: NewStagingTracker(),
			sharedModels:   true,
		}

		opts := &pb.ModelOptions{
			Model:     "llama-cpp/models/ornith.gguf",
			ModelFile: gguf,
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, modelID)
		Expect(err).ToNot(HaveOccurred())

		// The file stager must never be touched: no upload, no re-download.
		Expect(stager.ensureCalls).To(BeEmpty())
		// The worker loads directly from the shared volume, so the path is unchanged.
		Expect(staged.ModelFile).To(Equal(gguf))
	})

	It("stages files (existing behavior) when shared-models is disabled", func() {
		router := &SmartRouter{
			fileStager:     stager,
			stagingTracker: NewStagingTracker(),
			sharedModels:   false,
		}

		opts := &pb.ModelOptions{
			Model:     "llama-cpp/models/ornith.gguf",
			ModelFile: gguf,
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, modelID)
		Expect(err).ToNot(HaveOccurred())

		// Default mode uploads the model file to the worker.
		Expect(stager.ensureCalls).ToNot(BeEmpty())
		stagedLocals := make([]string, 0, len(stager.ensureCalls))
		for _, c := range stager.ensureCalls {
			stagedLocals = append(stagedLocals, c.localPath)
		}
		Expect(stagedLocals).To(ContainElement(gguf))
		// ModelFile is rewritten to the remote (tracking-key namespaced) path.
		Expect(staged.ModelFile).ToNot(Equal(gguf))
	})
})
