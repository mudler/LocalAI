package nodes

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// A ModelFile that does not exist on the controller is legitimate: every
// backend outside config.managedArtifactBackends that takes a bare HuggingFace
// repo id gets an optimistically constructed path (ModelFileName falls through
// to the raw model reference) that was never materialized, and downloads its
// own weights on the worker. Staging must therefore keep skipping it rather
// than failing. What it must NOT do is skip silently: the operator sees
// "Staging model files for remote node" and no further trace, so a genuine
// acquisition gap (#10910's allow-list omitting a directory-consuming backend)
// looks like a remote LoadModel timeout instead. The skip is logged at warn so
// it survives a default log level.
var _ = Describe("stageModelFiles missing source visibility", func() {
	var (
		router   *SmartRouter
		node     *BackendNode
		captured *bytes.Buffer
	)

	BeforeEach(func() {
		router = &SmartRouter{
			fileStager:     &fakeFileStager{},
			stagingTracker: NewStagingTracker(),
		}
		node = &BackendNode{ID: "node-1", Name: "nvidia-thor", Address: "10.0.0.1:50051"}

		// Capture at warn level so a debug-level emission is filtered out and
		// the assertion fails on visibility, not on wording.
		captured = &bytes.Buffer{}
		handler := slog.NewTextHandler(captured, &slog.HandlerOptions{Level: slog.LevelWarn})
		xlog.SetLogger(xlog.NewLoggerWithHandler(handler, xlog.LogLevelWarn))
	})

	AfterEach(func() {
		// xlog exposes no getter for the package logger, so restore the same
		// default the suite entrypoint installs rather than the prior value.
		xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text"))
	})

	It("warns, and names the field and path, when the model file does not exist locally", func() {
		missing := filepath.Join(GinkgoT().TempDir(), "meituan-longcat", "LongCat-Video-Avatar-1.5")
		opts := &pb.ModelOptions{
			Model:     "meituan-longcat/LongCat-Video-Avatar-1.5",
			ModelFile: missing,
		}

		staged, err := router.stageModelFiles(context.Background(), node, opts, "longcat-video-avatar-1.5")
		Expect(err).ToNot(HaveOccurred())

		// Behavior is unchanged: the field is still blanked so the worker does
		// not receive a controller-only path.
		Expect(staged.ModelFile).To(BeEmpty())

		logged := captured.String()
		Expect(logged).To(ContainSubstring("ModelFile"))
		Expect(logged).To(ContainSubstring(missing))
	})
})
