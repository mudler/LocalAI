package startup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	. "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Preload test", func() {
	var tmpdir string
	var systemState *system.SystemState
	var ml *model.ModelLoader
	var ctx context.Context
	var cancel context.CancelFunc

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		var err error
		tmpdir, err = os.MkdirTemp("", "")
		Expect(err).ToNot(HaveOccurred())
		systemState, err = system.GetSystemState(system.WithModelPath(tmpdir))
		Expect(err).ToNot(HaveOccurred())
		ml = model.NewModelLoader(systemState, true)
	})

	AfterEach(func() {
		cancel()
	})

	Context("Preloading from strings", func() {
		It("loads from embedded full-urls", func() {
			url := "https://raw.githubusercontent.com/mudler/LocalAI-examples/main/configurations/phi-2.yaml"
			fileName := fmt.Sprintf("%s.yaml", "phi-2")

			galleryService := services.NewGalleryService(&config.ApplicationConfig{
				SystemState: systemState,
			}, ml)
			galleryService.Start(ctx, config.NewModelConfigLoader(tmpdir), systemState)

			err := InstallModels(ctx, galleryService, []config.Gallery{}, []config.Gallery{}, systemState, ml, true, true, func(s1, s2, s3 string, f float64) {
				fmt.Println(s1, s2, s3, f)
			}, url)
			Expect(err).ToNot(HaveOccurred())
			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})
		It("downloads from urls", func() {
			url := "huggingface://TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF/tinyllama-1.1b-chat-v0.3.Q2_K.gguf"
			fileName := fmt.Sprintf("%s.gguf", "tinyllama-1.1b-chat-v0.3.Q2_K")

			galleryService := services.NewGalleryService(&config.ApplicationConfig{
				SystemState: systemState,
			}, ml)
			galleryService.Start(ctx, config.NewModelConfigLoader(tmpdir), systemState)

			err := InstallModels(ctx, galleryService, []config.Gallery{}, []config.Gallery{}, systemState, ml, true, true, func(s1, s2, s3 string, f float64) {
				fmt.Println(s1, s2, s3, f)
			}, url)
			Expect(err).ToNot(HaveOccurred())

			resultFile := filepath.Join(tmpdir, fileName)
			dirs, err := os.ReadDir(tmpdir)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(resultFile)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("%+v", dirs))
		})
	})
})
