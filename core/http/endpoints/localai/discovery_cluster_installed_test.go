package localai_test

import (
	"context"
	"encoding/json"
	"errors"
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

// Installed-state on a distributed controller is derived from the controller's
// own filesystem, where a backend installed on a GPU worker does not exist.
// The capability fix (#10947) made those backends *listable*; every surface
// that filters on installed-state still dropped them, so an admin with a
// fine-tuning-capable GPU worker got an empty dropdown.
var _ = Describe("Backend discovery with cluster install state", func() {
	var (
		appCfg      *config.ApplicationConfig
		systemState *system.SystemState
		galleries   []config.Gallery
		tmpDir      string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "discovery-cluster-installed-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		// cpu-trainer sits on the controller's disk; gpu-trainer exists only on
		// a worker, so nothing on this filesystem can prove it is installed.
		writeFakeSystemBackend(tmpDir, "cpu-trainer")

		galleryPath := filepath.Join(tmpDir, "gallery.yaml")
		data, err := yaml.Marshal([]gallery.GalleryBackend{
			{
				Metadata: gallery.Metadata{
					Name: "gpu-trainer",
					Tags: []string{"fine-tuning", "quantization"},
				},
				CapabilitiesMap: map[string]string{"nvidia-cuda-13": "gpu-trainer-cuda-13"},
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

		galleries = []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryPath}}
		// A GPU-less controller pod: capability "default".
		systemState = system.NewCapabilityState("default",
			system.WithBackendPath(tmpDir), system.WithBackendSystemPath(tmpDir))
		appCfg = &config.ApplicationConfig{
			BackendGalleries: galleries,
			SystemState:      systemState,
		}
	})

	nvidiaWorker := func(ctx context.Context) ([]string, error) {
		return []string{"nvidia-cuda-13"}, nil
	}
	installedOnWorker := func(ctx context.Context) ([]string, error) {
		return []string{"gpu-trainer"}, nil
	}
	registryDown := func(ctx context.Context) ([]string, error) {
		return nil, errors.New("registry unavailable")
	}

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

	Describe("fine-tune backends", func() {
		It("lists a backend installed only on a worker node", func() {
			names := listNames("/backends", ListFineTuneBackendsEndpoint(appCfg, nvidiaWorker, installedOnWorker))
			Expect(names).To(ContainElements("cpu-trainer", "gpu-trainer"))
		})

		It("keeps single-node listings unchanged with no provider", func() {
			names := listNames("/backends", ListFineTuneBackendsEndpoint(appCfg, nil, nil))
			Expect(names).To(ContainElement("cpu-trainer"))
			Expect(names).NotTo(ContainElement("gpu-trainer"))
		})

		It("degrades to the local listing when the registry errors", func() {
			names := listNames("/backends", ListFineTuneBackendsEndpoint(appCfg, nvidiaWorker, registryDown))
			Expect(names).To(ContainElement("cpu-trainer"))
			Expect(names).NotTo(ContainElement("gpu-trainer"))
		})
	})

	Describe("quantization backends", func() {
		It("lists a backend installed only on a worker node", func() {
			names := listNames("/backends", ListQuantizationBackendsEndpoint(appCfg, nvidiaWorker, installedOnWorker))
			Expect(names).To(ContainElements("cpu-trainer", "gpu-trainer"))
		})

		It("keeps single-node listings unchanged with no provider", func() {
			names := listNames("/backends", ListQuantizationBackendsEndpoint(appCfg, nil, nil))
			Expect(names).To(ContainElement("cpu-trainer"))
			Expect(names).NotTo(ContainElement("gpu-trainer"))
		})
	})

	Describe("GET /backends/available", func() {
		installedFlags := func(capabilities ClusterCapabilityProvider, installed ClusterInstalledProvider) map[string]bool {
			e := echo.New()
			svc := CreateBackendEndpointService(galleries, systemState, nil, nil)
			e.GET("/backends/available", svc.ListAvailableBackendsEndpoint(systemState, capabilities, installed))

			req := httptest.NewRequest(http.MethodGet, "/backends/available", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))

			var backends []gallery.GalleryBackend
			Expect(json.Unmarshal(rec.Body.Bytes(), &backends)).To(Succeed())

			flags := map[string]bool{}
			for _, b := range backends {
				flags[b.Name] = b.Installed
			}
			return flags
		}

		It("reports a worker-installed backend as installed", func() {
			flags := installedFlags(nvidiaWorker, installedOnWorker)
			Expect(flags).To(HaveKeyWithValue("gpu-trainer", true))
			Expect(flags).To(HaveKeyWithValue("cpu-trainer", true))
		})

		It("keeps single-node install state unchanged with no provider", func() {
			flags := installedFlags(nvidiaWorker, nil)
			Expect(flags).To(HaveKeyWithValue("gpu-trainer", false))
			Expect(flags).To(HaveKeyWithValue("cpu-trainer", true))
		})
	})
})
