package startup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	. "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
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
		ml = model.NewModelLoader(systemState)
	})

	AfterEach(func() {
		cancel()
	})

	Context("Preloading from strings", func() {
		It("loads from embedded full-urls", func() {
			url := "https://raw.githubusercontent.com/mudler/LocalAI-examples/main/configurations/phi-2.yaml"
			fileName := fmt.Sprintf("%s.yaml", "phi-2")

			galleryService := galleryop.NewGalleryService(&config.ApplicationConfig{
				SystemState: systemState,
			}, ml)
			galleryService.Start(ctx, config.NewModelConfigLoader(tmpdir), systemState)

			err := InstallModels(ctx, galleryService, []config.Gallery{}, []config.Gallery{}, systemState, ml, true, true, false, func(s1, s2, s3 string, f float64) {
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

			galleryService := galleryop.NewGalleryService(&config.ApplicationConfig{
				SystemState: systemState,
			}, ml)
			galleryService.Start(ctx, config.NewModelConfigLoader(tmpdir), systemState)

			err := InstallModels(ctx, galleryService, []config.Gallery{}, []config.Gallery{}, systemState, ml, true, true, false, func(s1, s2, s3 string, f float64) {
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

// clusterBackendManager answers availability from a fixed set, as the
// NATS-backed manager would from the workers' listings.
type clusterBackendManager struct{ available map[string]bool }

func (clusterBackendManager) InstallBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return nil
}
func (clusterBackendManager) DeleteBackend(string) error { return nil }
func (clusterBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.SystemBackends{}, nil
}
func (clusterBackendManager) UpgradeBackend(context.Context, *galleryop.ManagementOp[gallery.GalleryBackend, any], galleryop.ProgressCallback) error {
	return nil
}
func (clusterBackendManager) CheckUpgrades(context.Context) (map[string]gallery.UpgradeInfo, error) {
	return nil, nil
}
func (clusterBackendManager) IsDistributed() bool { return true }
func (m clusterBackendManager) BackendAvailable(name string) (bool, error) {
	return m.available[name], nil
}

var _ = Describe("Preload with backend installation disabled", func() {
	var (
		tmpdir      string
		systemState *system.SystemState
		ml          *model.ModelLoader
		galleries   []config.Gallery
	)

	BeforeEach(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "preload-install-disabled-*")
		Expect(err).ToNot(HaveOccurred())
		systemState, err = system.GetSystemState(
			system.WithModelPath(filepath.Join(tmpdir, "models")),
			system.WithBackendPath(filepath.Join(tmpdir, "backends")),
			system.WithDisableBackendInstall(true),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(systemState.Model.ModelsPath, 0o750)).To(Succeed())
		Expect(os.MkdirAll(systemState.Backend.BackendsPath, 0o750)).To(Succeed())
		ml = model.NewModelLoader(systemState)

		// The weights already sit in the models path, so the install touches
		// nothing but the config file and the admission check.
		Expect(os.WriteFile(filepath.Join(systemState.Model.ModelsPath, "weights.gguf"), []byte("weights"), 0o600)).To(Succeed())
		entries := []gallery.GalleryModel{{
			Metadata:   gallery.Metadata{Name: "needs-vllm", Backend: "vllm"},
			ConfigFile: map[string]any{"backend": "vllm", "parameters": map[string]any{"model": "weights.gguf"}},
		}}
		out, err := yaml.Marshal(entries)
		Expect(err).ToNot(HaveOccurred())
		galleryPath := filepath.Join(systemState.Model.ModelsPath, "preload.yaml")
		Expect(os.WriteFile(galleryPath, out, 0o600)).To(Succeed())
		galleries = []config.Gallery{{Name: "preload", URL: "file://" + galleryPath}}
	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)
	})

	preload := func(workersHave map[string]bool) error {
		galleryService := galleryop.NewGalleryService(&config.ApplicationConfig{SystemState: systemState, DisableBackendInstall: true}, ml)
		galleryService.SetBackendManager(clusterBackendManager{available: workersHave})
		return InstallModels(context.Background(), galleryService, galleries, nil, systemState, ml, false, false, false, nil, "needs-vllm")
	}

	It("installs a model whose backend a worker has, with no local backends", func() {
		Expect(preload(map[string]bool{"vllm": true})).To(Succeed())
		Expect(filepath.Join(systemState.Model.ModelsPath, "needs-vllm.yaml")).To(BeAnExistingFile())
	})

	It("refuses a model whose backend no worker has", func() {
		err := preload(map[string]bool{})
		Expect(err).To(MatchError(gallery.ErrBackendInstallDisabled))
		Expect(filepath.Join(systemState.Model.ModelsPath, "needs-vllm.yaml")).NotTo(BeAnExistingFile())
	})
})
