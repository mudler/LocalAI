package galleryop_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression test for issue #9817: the Ollama /api/tags handler calls
// ListModels with a nil filter, which used to panic as soon as a loose file
// existed under ModelsPath. The panic surfaced to Ollama clients (e.g. Home
// Assistant) as "Server disconnected without sending a response".
var _ = Describe("ListModels", func() {
	var (
		tempDir     string
		bcl         *config.ModelConfigLoader
		ml          *model.ModelLoader
		systemState *system.SystemState
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "list-models-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err = system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).NotTo(HaveOccurred())
		ml = model.NewModelLoader(systemState)
		bcl = config.NewModelConfigLoader(tempDir)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("does not panic with a nil filter when loose files exist", func() {
		// ListFilesInModelPath skips well-known weight-file extensions
		// (.gguf, .bin, ...) so use an extension-less file to ensure the
		// filter path is exercised.
		Expect(os.WriteFile(filepath.Join(tempDir, "loose-model"), []byte("x"), 0o644)).To(Succeed())

		var names []string
		var err error
		Expect(func() {
			names, err = galleryop.ListModels(bcl, ml, nil, galleryop.SKIP_IF_CONFIGURED)
		}).ToNot(Panic())
		Expect(err).ToNot(HaveOccurred())
		Expect(names).To(ContainElement("loose-model"))
	})

	It("does not panic with a nil filter when ModelsPath is empty", func() {
		Expect(func() {
			_, err := galleryop.ListModels(bcl, ml, nil, galleryop.SKIP_IF_CONFIGURED)
			Expect(err).ToNot(HaveOccurred())
		}).ToNot(Panic())
	})
})
