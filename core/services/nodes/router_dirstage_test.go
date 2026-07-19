package nodes

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/storage"
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

	It("stages a content-addressed Hugging Face snapshot with its relative tree intact", func() {
		cacheKey := strings.Repeat("a", 64)
		logicalModel := "owner/repo"
		relativeSnapshot := filepath.Join(".artifacts", "huggingface", cacheKey, "snapshot")
		snapshot := filepath.Join(tmp, "models", relativeSnapshot)
		files := map[string]string{
			"config.json":                            "{}",
			"model.safetensors.index.json":           "{}",
			"model-00001-of-00002.safetensors":       "part-1",
			"model-00002-of-00002.safetensors":       "part-2",
			filepath.Join("tokenizer", "vocab.json"): "{}",
		}
		for relative, contents := range files {
			path := filepath.Join(snapshot, relative)
			Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
			Expect(os.WriteFile(path, []byte(contents), 0o644)).To(Succeed())
		}

		opts := &pb.ModelOptions{Model: logicalModel, ModelFile: snapshot}
		stagedOpts, err := router.stageModelFiles(context.Background(), node, opts, "managed-model")
		Expect(err).NotTo(HaveOccurred())

		stagedPaths := make([]string, 0, len(stager.ensureCalls))
		stagedKeys := make([]string, 0, len(stager.ensureCalls))
		for _, call := range stager.ensureCalls {
			stagedPaths = append(stagedPaths, call.localPath)
			stagedKeys = append(stagedKeys, call.key)
		}
		expectedPaths := make([]string, 0, len(files))
		expectedKeys := make([]string, 0, len(files))
		for relative := range files {
			expectedPaths = append(expectedPaths, filepath.Join(snapshot, relative))
			// Keys keep the full models-root-relative path, including the
			// .artifacts/huggingface/<key>/snapshot prefix. This changed when
			// companion artifacts arrived: keys used to be relative to the
			// snapshot itself, which flattened the prefix away and left two
			// snapshots of one model indistinguishable on the worker.
			expectedKeys = append(expectedKeys, storage.ModelKey(filepath.Join("managed-model", relativeSnapshot, relative)))
		}
		Expect(stagedPaths).To(ConsistOf(expectedPaths))
		Expect(stagedKeys).To(ConsistOf(expectedKeys))
		remoteRoot := filepath.Join("/remote", storage.ModelKey("managed-model"))
		Expect(stagedOpts.Model).To(Equal(logicalModel))
		Expect(stagedOpts.ModelFile).To(Equal(filepath.Join(remoteRoot, relativeSnapshot)))
		// ModelPath is the staged models ROOT, not the snapshot. It used to be
		// the snapshot, which made every relative option resolve inside the
		// primary and put a sibling companion snapshot out of reach.
		Expect(stagedOpts.ModelPath).To(Equal(remoteRoot))
		Expect(opts.Model).To(Equal(logicalModel))
		Expect(opts.ModelFile).To(Equal(snapshot))
	})
})
