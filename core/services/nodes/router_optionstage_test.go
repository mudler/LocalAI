package nodes

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// These tests cover staging of companion assets declared as option file paths
// (the "vae_path:..." convention). Backends like sherpa-onnx keep a single-file
// ModelFile (the .onnx) but resolve sibling assets — tokens.txt and the
// espeak-ng-data directory — relative to the model dir. Those siblings must be
// shipped to remote workers too, including directory-valued options expanded
// file-by-file.
var _ = Describe("stageGenericOptions companion assets", func() {
	var (
		stager *fakeFileStager
		router *SmartRouter
		node   *BackendNode
		tmp    string
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

	It("stages option-declared sibling files and expands directory options", func() {
		modelRel := "vits-piper-it_IT-paola-medium"
		modelDir := filepath.Join(tmp, "models", modelRel)
		dataDir := filepath.Join(modelDir, "espeak-ng-data")
		Expect(os.MkdirAll(filepath.Join(dataDir, "lang"), 0o755)).To(Succeed())

		onnx := filepath.Join(modelDir, "it_IT-paola-medium.onnx")
		tokens := filepath.Join(modelDir, "tokens.txt")
		phontab := filepath.Join(dataDir, "phontab")
		langIt := filepath.Join(dataDir, "lang", "it")
		for _, f := range []string{onnx, tokens, phontab, langIt} {
			Expect(os.WriteFile(f, []byte("x"), 0o644)).To(Succeed())
		}

		opts := &pb.ModelOptions{
			Model:     filepath.Join(modelRel, "it_IT-paola-medium.onnx"),
			ModelFile: onnx,
			// Bare names: not found under the models root (Model includes a
			// subdir), so they must resolve relative to the model's own dir.
			Options: []string{
				"tts.noise_scale=0.667", // not a path; ignored by staging
				"tokens:tokens.txt",
				"data_dir:espeak-ng-data",
			},
		}

		_, err := router.stageModelFiles(context.Background(), node, opts, "track-key")
		Expect(err).ToNot(HaveOccurred())

		staged := make([]string, 0, len(stager.ensureCalls))
		for _, c := range stager.ensureCalls {
			staged = append(staged, c.localPath)
		}
		// The .onnx (ModelFile), the tokens.txt file option, and every file under
		// the espeak-ng-data directory option are staged; the directory path
		// itself is never handed to the stager.
		Expect(staged).To(ContainElements(onnx, tokens, phontab, langIt))
		Expect(staged).ToNot(ContainElement(dataDir))
	})
})
