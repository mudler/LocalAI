package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// These tests cover staging of "directory models" — models whose ModelFile is a
// directory containing multiple files (e.g. qwen3-tts-cpp ships weights +
// tokenizer ggufs under one directory). The HTTP file stager uploads a single
// regular file per path, so a directory ModelFile must be expanded into its
// constituent files; otherwise the upload reads a directory fd and fails with
// "is a directory" (EISDIR) on remote NATS worker nodes.
var _ = Describe("stageModelFiles directory models", func() {
	var (
		stager  *fakeFileStager
		router  *SmartRouter
		node    *BackendNode
		tmp     string
		modelID = "qwen3-tts-cpp"
	)

	BeforeEach(func() {
		stager = &fakeFileStager{}
		router = &SmartRouter{
			fileStager:     stager,
			stagingTracker: NewStagingTracker(),
		}
		node = &BackendNode{ID: "node-1", Name: "node-1", Address: "10.0.0.1:50051"}
		tmp = GinkgoT().TempDir()
	})

	It("stages every file inside a directory ModelFile instead of the directory path", func() {
		modelDir := filepath.Join(tmp, "models", modelID)
		Expect(os.MkdirAll(modelDir, 0o755)).To(Succeed())
		weights := filepath.Join(modelDir, "qwen3-tts-0.6b-f16.gguf")
		tokenizer := filepath.Join(modelDir, "qwen3-tts-tokenizer-f16.gguf")
		Expect(os.WriteFile(weights, []byte("weights"), 0o644)).To(Succeed())
		Expect(os.WriteFile(tokenizer, []byte("tokenizer"), 0o644)).To(Succeed())

		opts := &pb.ModelOptions{
			Model:     modelID,
			ModelFile: modelDir,
		}

		_, err := router.stageModelFiles(context.Background(), node, opts, "track-key")
		Expect(err).ToNot(HaveOccurred())

		staged := make([]string, 0, len(stager.ensureCalls))
		for _, c := range stager.ensureCalls {
			staged = append(staged, c.localPath)
		}
		// Each contained file is staged individually; the directory path itself
		// is never handed to the stager (which would read a directory fd).
		Expect(staged).To(ConsistOf(weights, tokenizer))
		Expect(staged).ToNot(ContainElement(modelDir))
	})
})
