package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A split GGUF is configured by its first shard only; llama.cpp opens the
// remaining "-0000N-of-0000M" files from the same directory on its own. The
// worker has no view of the frontend's models directory, so every shard must be
// staged, or the load fails with "failed to load GGUF split".
var _ = Describe("stageModelFiles split GGUF shards", func() {
	var (
		stager   *fakeFileStager
		router   *SmartRouter
		node     *BackendNode
		modelDir string
		shards   []string
	)

	BeforeEach(func() {
		stager = &fakeFileStager{}
		router = &SmartRouter{
			fileStager:     stager,
			stagingTracker: NewStagingTracker(),
		}
		node = &BackendNode{ID: "node-1", Name: "node-1", Address: "10.0.0.1:50051"}
		modelDir = filepath.Join(GinkgoT().TempDir(), "llama-cpp", "models", "big")
		Expect(os.MkdirAll(modelDir, 0o755)).To(Succeed())

		shards = nil
		for _, name := range []string{
			"Big-Q4_K_M-00001-of-00003.gguf",
			"Big-Q4_K_M-00002-of-00003.gguf",
			"Big-Q4_K_M-00003-of-00003.gguf",
		} {
			p := filepath.Join(modelDir, name)
			Expect(os.WriteFile(p, []byte("shard "+name), 0o644)).To(Succeed())
			shards = append(shards, p)
		}
		// An unrelated file in the same directory must not be swept up.
		Expect(os.WriteFile(filepath.Join(modelDir, "Other-00001-of-00002.gguf"), []byte("x"), 0o644)).To(Succeed())
	})

	stagedPaths := func() []string {
		out := make([]string, 0, len(stager.ensureCalls))
		for _, c := range stager.ensureCalls {
			out = append(out, c.localPath)
		}
		return out
	}

	It("stages every shard beside the first one", func() {
		opts := &pb.ModelOptions{
			Model:     "llama-cpp/models/big/Big-Q4_K_M-00001-of-00003.gguf",
			ModelFile: shards[0],
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, "big")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(shards[0], shards[1], shards[2]))

		// Shards must land in the same remote directory as the first one,
		// since llama.cpp derives their paths from it.
		for _, c := range stager.ensureCalls {
			Expect(filepath.Dir(c.key)).To(Equal(filepath.Dir(stager.ensureCalls[0].key)))
		}
		Expect(staged.ModelFile).To(Equal("/remote/" + stager.ensureCalls[0].key))
	})

	It("fails the load when a shard of the model is missing locally", func() {
		Expect(os.Remove(shards[2])).To(Succeed())
		opts := &pb.ModelOptions{
			Model:     "llama-cpp/models/big/Big-Q4_K_M-00001-of-00003.gguf",
			ModelFile: shards[0],
		}

		_, err := router.stageModelFiles(context.Background(), node, opts, "big")
		Expect(err).To(MatchError(ContainSubstring("00003-of-00003")))
	})

	It("counts and sizes all shards for progress, disk and load budget", func() {
		Expect(countStageableFiles(shards[0])).To(Equal(3))

		var want int64
		for _, s := range shards {
			fi, err := os.Stat(s)
			Expect(err).ToNot(HaveOccurred())
			want += fi.Size()
		}
		Expect(modelPayloadBytes(&pb.ModelOptions{ModelFile: shards[0]})).To(Equal(want))
	})

	It("leaves a non-split GGUF alone", func() {
		single := filepath.Join(modelDir, "single.gguf")
		Expect(os.WriteFile(single, []byte("one"), 0o644)).To(Succeed())

		_, err := router.stageModelFiles(context.Background(), node,
			&pb.ModelOptions{Model: "llama-cpp/models/big/single.gguf", ModelFile: single}, "single")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(single))
		Expect(countStageableFiles(single)).To(Equal(1))
	})
})
