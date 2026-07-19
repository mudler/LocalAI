package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The fine-tune and quantization backend lists are discovery endpoints with
// the same semantics as /backends/available: they populate UI dropdowns. On a
// GPU-less controller they hid GPU-only backends the cluster could run, so an
// admin with a fine-tuning-capable GPU worker saw an empty dropdown.
var _ = Describe("Tagged backend discovery with cluster capabilities", func() {
	var (
		appCfg *config.ApplicationConfig
		tmpDir string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "tagged-discovery-cluster-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		// Both endpoints only surface *installed* backends, so lay the
		// backend down on disk; the capability filter is then the only thing
		// that can remove it from the response.
		writeFakeSystemBackend(tmpDir, "gpu-trainer")
		writeFakeSystemBackend(tmpDir, "cpu-trainer")

		galleryPath := filepath.Join(tmpDir, "gallery.yaml")
		data, err := yaml.Marshal([]gallery.GalleryBackend{
			{
				Metadata: gallery.Metadata{
					Name: "gpu-trainer",
					Tags: []string{"fine-tuning", "quantization"},
				},
				CapabilitiesMap: map[string]string{
					"nvidia":         "gpu-trainer-nvidia",
					"nvidia-cuda-13": "gpu-trainer-cuda-13",
				},
			},
			{
				Metadata: gallery.Metadata{
					Name: "cpu-trainer",
					Tags: []string{"fine-tuning", "quantization"},
				},
				CapabilitiesMap: map[string]string{"default": "cpu-trainer-cpu"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, data, 0o644)).To(Succeed())

		appCfg = &config.ApplicationConfig{
			BackendGalleries: []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryPath}},
			SystemState: system.NewCapabilityState("default",
				system.WithBackendPath(tmpDir), system.WithBackendSystemPath(tmpDir)),
		}
	})

	// listNames drives handler over its route and returns the backend names.
	listNames := func(path string, handler echo.HandlerFunc) []string {
		e := echo.New()
		e.GET(path, handler)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var backends []struct {
			Name string `json:"name"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &backends)).To(Succeed())

		names := []string{}
		for _, b := range backends {
			names = append(names, b.Name)
		}
		return names
	}

	nvidiaWorker := func(ctx context.Context) ([]string, error) {
		return []string{"nvidia-cuda-13"}, nil
	}

	Describe("fine-tune backends", func() {
		It("hides GPU-only backends with no cluster provider", func() {
			names := listNames("/backends", ListFineTuneBackendsEndpoint(appCfg, nil))
			Expect(names).To(ContainElement("cpu-trainer"))
			Expect(names).NotTo(ContainElement("gpu-trainer"))
		})

		It("lists GPU-only backends a worker node can run", func() {
			names := listNames("/backends", ListFineTuneBackendsEndpoint(appCfg, nvidiaWorker))
			Expect(names).To(ContainElements("cpu-trainer", "gpu-trainer"))
		})
	})

	Describe("quantization backends", func() {
		It("hides GPU-only backends with no cluster provider", func() {
			names := listNames("/backends", ListQuantizationBackendsEndpoint(appCfg, nil))
			Expect(names).To(ContainElement("cpu-trainer"))
			Expect(names).NotTo(ContainElement("gpu-trainer"))
		})

		It("lists GPU-only backends a worker node can run", func() {
			names := listNames("/backends", ListQuantizationBackendsEndpoint(appCfg, nvidiaWorker))
			Expect(names).To(ContainElements("cpu-trainer", "gpu-trainer"))
		})
	})
})
