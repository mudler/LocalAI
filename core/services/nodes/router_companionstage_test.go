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

// Staging a managed model has to preserve the models-root-relative layout of
// the content-addressed artifact tree, not just the contents of the primary
// snapshot.
//
// A composed pipeline resolves a companion snapshot through an option holding a
// path relative to the models directory (see withCompanionArtifactOptions).
// That only works if the worker's ModelPath is the staged models ROOT: the
// companion lives in a sibling .artifacts/huggingface/<key>/snapshot directory,
// outside the primary snapshot entirely, so anchoring on the primary would put
// it out of reach and collapse its files to bare basenames.
var _ = Describe("stageModelFiles managed artifact trees", func() {
	const (
		primaryKey   = "1111111111111111111111111111111111111111111111111111111111111111"
		companionKey = "2222222222222222222222222222222222222222222222222222222222222222"
	)

	var (
		stager     *fakeFileStager
		router     *SmartRouter
		node       *BackendNode
		modelsDir  string
		primaryRel string
		compRel    string
	)

	write := func(root string, files map[string]string) {
		for relative, contents := range files {
			path := filepath.Join(root, relative)
			Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
			Expect(os.WriteFile(path, []byte(contents), 0o644)).To(Succeed())
		}
	}

	BeforeEach(func() {
		stager = &fakeFileStager{}
		router = &SmartRouter{fileStager: stager, stagingTracker: NewStagingTracker()}
		node = &BackendNode{ID: "node-1", Name: "nvidia-thor", Address: "10.0.0.1:50051"}
		modelsDir = filepath.Join(GinkgoT().TempDir(), "models")
		primaryRel = filepath.Join(".artifacts", "huggingface", primaryKey, "snapshot")
		compRel = filepath.Join(".artifacts", "huggingface", companionKey, "snapshot")

		write(filepath.Join(modelsDir, primaryRel), map[string]string{
			"model_index.json":                 "{}",
			"base_model/diffusion.safetensors": "weights",
		})
		write(filepath.Join(modelsDir, compRel), map[string]string{
			"model_index.json":     "{}",
			"vae/vae.safetensors":  "vae",
			"tokenizer/vocab.json": "{}",
		})
	})

	stagedKeys := func() []string {
		keys := make([]string, 0, len(stager.ensureCalls))
		for _, call := range stager.ensureCalls {
			keys = append(keys, call.key)
		}
		return keys
	}

	It("stages a companion snapshot referenced by a relative option", func() {
		opts := &pb.ModelOptions{
			Model:     "meituan-longcat/LongCat-Video-Avatar-1.5",
			ModelFile: filepath.Join(modelsDir, primaryRel),
			Options:   []string{"attention_backend:sdpa", "base_model:" + compRel},
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, "avatar")
		Expect(err).ToNot(HaveOccurred())

		// Every file of both snapshots ships, each under its models-root-relative
		// path so the two trees stay distinguishable on the worker.
		Expect(stagedKeys()).To(ConsistOf(
			storage.ModelKey(filepath.Join("avatar", primaryRel, "model_index.json")),
			storage.ModelKey(filepath.Join("avatar", primaryRel, "base_model/diffusion.safetensors")),
			storage.ModelKey(filepath.Join("avatar", compRel, "model_index.json")),
			storage.ModelKey(filepath.Join("avatar", compRel, "vae/vae.safetensors")),
			storage.ModelKey(filepath.Join("avatar", compRel, "tokenizer/vocab.json")),
		))

		// The option value is never rewritten: it stays relative and resolves
		// against the worker's ModelPath.
		Expect(staged.Options).To(ContainElement("base_model:" + compRel))
		Expect(staged.Options).To(ContainElement("attention_backend:sdpa"))
	})

	It("points ModelPath at the staged models root, not at the primary snapshot", func() {
		opts := &pb.ModelOptions{
			Model:     "meituan-longcat/LongCat-Video-Avatar-1.5",
			ModelFile: filepath.Join(modelsDir, primaryRel),
			Options:   []string{"base_model:" + compRel},
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, "avatar")
		Expect(err).ToNot(HaveOccurred())

		root := filepath.Join("/remote", storage.ModelKey("avatar"))
		Expect(staged.ModelPath).To(Equal(root))
		// The load target still addresses the primary snapshot itself.
		Expect(staged.ModelFile).To(Equal(filepath.Join(root, primaryRel)))
		// And joining ModelPath with the untouched option value has to land on
		// the companion the worker actually received.
		Expect(filepath.Join(staged.ModelPath, compRel)).To(Equal(filepath.Join(root, compRel)))
	})

	It("keeps a legacy relative model file anchored on the models directory", func() {
		// The pre-artifact layout, where Model is a real relative path under the
		// models dir, must keep deriving the same root it always did.
		legacy := filepath.Join(modelsDir, "sd-cpp", "models")
		write(legacy, map[string]string{"flux.gguf": "weights"})
		opts := &pb.ModelOptions{
			Model:     filepath.Join("sd-cpp", "models", "flux.gguf"),
			ModelFile: filepath.Join(legacy, "flux.gguf"),
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, "flux")
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.HasSuffix(staged.ModelPath, storage.ModelKey("flux"))).To(BeTrue())
		Expect(staged.ModelFile).To(Equal(filepath.Join(staged.ModelPath, "sd-cpp", "models", "flux.gguf")))
	})
})
