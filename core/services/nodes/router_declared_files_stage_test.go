package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A model's config names only the file the backend opens first, but its
// install can declare more that the backend reads by itself: llama.cpp opens
// the "-0000N-of-0000M" shards of a split GGUF from the directory of the first
// one. The worker has no view of the frontend's models directory, so every
// declared file must be staged, or the load fails with "failed to load GGUF
// split".
var _ = Describe("stageModelFiles declared model files", func() {
	var (
		stager   *fakeFileStager
		router   *SmartRouter
		node     *BackendNode
		modelDir string
		shards   []string
		mmproj   string
		declared map[string][]string
	)

	BeforeEach(func() {
		stager = &fakeFileStager{}
		declared = map[string][]string{}
		router = &SmartRouter{
			fileStager:     stager,
			stagingTracker: NewStagingTracker(),
			modelFiles:     func(name string) []string { return declared[name] },
		}
		node = &BackendNode{ID: "node-1", Name: "node-1", Address: "10.0.0.1:50051"}
		root := GinkgoT().TempDir()
		modelDir = filepath.Join(root, "llama-cpp", "models", "big")
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
		mmproj = filepath.Join(root, "llama-cpp", "mmproj", "big", "mmproj.gguf")
		Expect(os.MkdirAll(filepath.Dir(mmproj), 0o755)).To(Succeed())
		Expect(os.WriteFile(mmproj, []byte("mmproj"), 0o644)).To(Succeed())
	})

	opts := func() *pb.ModelOptions {
		return &pb.ModelOptions{
			Model:     "llama-cpp/models/big/Big-Q4_K_M-00001-of-00003.gguf",
			ModelFile: shards[0],
			MMProj:    mmproj,
		}
	}

	stagedPaths := func() []string {
		out := make([]string, 0, len(stager.ensureCalls))
		for _, c := range stager.ensureCalls {
			out = append(out, c.localPath)
		}
		return out
	}

	It("stages every declared file once, beside the ones the config names", func() {
		declared["big"] = append(append([]string{}, shards...), mmproj)

		staged, err := router.stageModelFiles(context.Background(), node, opts(), "big")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(shards[0], mmproj, shards[1], shards[2]))

		// llama.cpp derives the other shards' paths from the first one, so
		// they must land in the same remote directory.
		for _, c := range stager.ensureCalls {
			if c.localPath != mmproj {
				Expect(filepath.Dir(c.key)).To(Equal(filepath.Dir(stager.ensureCalls[0].key)))
			}
		}
		Expect(staged.ModelFile).To(Equal("/remote/" + stager.ensureCalls[0].key))
	})

	It("sizes declared files for the load budget and disk check", func() {
		declared["big"] = append(append([]string{}, shards...), mmproj)

		var want int64
		for _, p := range append(append([]string{}, shards...), mmproj) {
			fi, err := os.Stat(p)
			Expect(err).ToNot(HaveOccurred())
			want += fi.Size()
		}
		Expect(router.stagingPayloadBytes("big", opts())).To(Equal(want))
	})

	It("skips a declared file that is missing locally instead of failing", func() {
		declared["big"] = append(append([]string{}, shards...), filepath.Join(modelDir, "gone.bin"))

		_, err := router.stageModelFiles(context.Background(), node, opts(), "big")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(shards[0], mmproj, shards[1], shards[2]))
	})

	It("does not stage a declared file twice when a directory field covers it", func() {
		declared["dir"] = []string{shards[1]}

		_, err := router.stageModelFiles(context.Background(), node,
			&pb.ModelOptions{Model: "llama-cpp/models/big", ModelFile: modelDir}, "dir")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(shards[0], shards[1], shards[2]))
	})

	It("stages only the named files for a model that declares none", func() {
		_, err := router.stageModelFiles(context.Background(), node, opts(), "handwritten")
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedPaths()).To(ConsistOf(shards[0], mmproj))
	})
})
