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

// GET /backends/available on a distributed controller must advertise what the
// cluster can run, not what the controller itself can run.
var _ = Describe("ListAvailableBackendsEndpoint cluster capabilities", func() {
	var (
		app         *echo.Echo
		systemState *system.SystemState
		tmpDir      string
		galleries   []config.Gallery
	)

	BeforeEach(func() {
		app = echo.New()

		var err error
		tmpDir, err = os.MkdirTemp("", "backends-available-cluster-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		galleryPath := filepath.Join(tmpDir, "gallery.yaml")
		data, err := yaml.Marshal([]gallery.GalleryBackend{
			{
				Metadata: gallery.Metadata{Name: "longcat-video"},
				CapabilitiesMap: map[string]string{
					"nvidia":             "longcat-video-nvidia",
					"nvidia-cuda-13":     "longcat-video-cuda-13",
					"nvidia-l4t-cuda-13": "longcat-video-l4t-cuda-13",
				},
			},
			{
				Metadata:        gallery.Metadata{Name: "whisper"},
				CapabilitiesMap: map[string]string{"default": "whisper-cpu"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, data, 0o644)).To(Succeed())

		galleries = []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryPath}}

		// A GPU-less controller pod: capability "default".
		systemState = system.NewCapabilityState("default",
			system.WithBackendPath(tmpDir), system.WithBackendSystemPath(tmpDir))
	})

	listNames := func(provider ClusterCapabilityProvider) []string {
		svc := CreateBackendEndpointService(galleries, systemState, nil, nil)
		app.GET("/backends/available", svc.ListAvailableBackendsEndpoint(systemState, provider))

		req := httptest.NewRequest(http.MethodGet, "/backends/available", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var backends []gallery.GalleryBackend
		Expect(json.Unmarshal(rec.Body.Bytes(), &backends)).To(Succeed())

		names := []string{}
		for _, b := range backends {
			names = append(names, b.Name)
		}
		return names
	}

	It("hides GPU-only backends with no provider (single-node)", func() {
		names := listNames(nil)
		Expect(names).To(ContainElement("whisper"))
		Expect(names).NotTo(ContainElement("longcat-video"))
	})

	It("lists GPU-only backends a worker node can run", func() {
		provider := func(ctx context.Context) ([]string, error) {
			return []string{"nvidia-l4t-cuda-13"}, nil
		}
		Expect(listNames(provider)).To(ContainElements("whisper", "longcat-video"))
	})

	It("falls back to the local listing when the registry errors", func() {
		// A registry hiccup must degrade to the old behavior, never blank the
		// backend catalog with a 500.
		provider := func(ctx context.Context) ([]string, error) {
			return nil, errors.New("registry unavailable")
		}
		names := listNames(provider)
		Expect(names).To(ContainElement("whisper"))
		Expect(names).NotTo(ContainElement("longcat-video"))
	})
})
