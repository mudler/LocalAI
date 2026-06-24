package gallery_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// These specs cover backend discovery in distributed mode, where the machine
// answering GET /backends/available (the controller) is not the machine that
// will run the backend (a worker). Filtering the listing against the
// controller's own hardware hid every GPU-only meta backend from admins.
var _ = Describe("AvailableBackendsForCapabilities", func() {
	var (
		tempDir     string
		galleryPath string
		galleries   []config.Gallery
		// controller stands in for a GPU-less frontend pod: no vendor, so its
		// reported capability is "default".
		controller *system.SystemState
	)

	writeGalleryYAML := func(backends []GalleryBackend) {
		data, err := yaml.Marshal(backends)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(galleryPath, data, 0644)).To(Succeed())
	}

	names := func(backends GalleryElements[*GalleryBackend]) []string {
		out := []string{}
		for _, b := range backends {
			out = append(out, b.GetName())
		}
		return out
	}

	// longcatVideo mirrors the real gallery entry that triggered this bug: a
	// meta backend enumerating only NVIDIA variants, with neither a "default"
	// nor a "cpu" key for Capability() to fall back to.
	longcatVideo := GalleryBackend{
		Metadata: Metadata{Name: "longcat-video"},
		CapabilitiesMap: map[string]string{
			"nvidia":             "longcat-video-nvidia",
			"nvidia-cuda-12":     "longcat-video-cuda-12",
			"nvidia-cuda-13":     "longcat-video-cuda-13",
			"nvidia-l4t-cuda-13": "longcat-video-l4t-cuda-13",
		},
	}

	// cpuMeta is compatible with every host and must keep showing up in all
	// scenarios, proving the union widens the listing without replacing it.
	cpuMeta := GalleryBackend{
		Metadata:        Metadata{Name: "whisper"},
		CapabilitiesMap: map[string]string{"default": "whisper-cpu", "nvidia": "whisper-cuda"},
	}

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "node-capability-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tempDir)).To(Succeed())
		})

		galleryPath = filepath.Join(tempDir, "gallery.yaml")
		writeGalleryYAML([]GalleryBackend{longcatVideo, cpuMeta})

		galleries = []config.Gallery{{Name: "test-gallery", URL: "file://" + galleryPath}}
		controller = system.NewCapabilityState("default", system.WithBackendPath(tempDir))
	})

	It("hides a GPU-only meta when no node capabilities are supplied", func() {
		backends, err := AvailableBackendsForCapabilities(galleries, controller, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(backends)).To(ContainElement("whisper"))
		Expect(names(backends)).NotTo(ContainElement("longcat-video"))
	})

	It("lists a GPU-only meta runnable on a registered worker node", func() {
		backends, err := AvailableBackendsForCapabilities(galleries, controller, []string{"nvidia-l4t-cuda-13"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(backends)).To(ContainElement("longcat-video"))
		Expect(names(backends)).To(ContainElement("whisper"))
	})

	It("unions across heterogeneous nodes rather than intersecting them", func() {
		writeGalleryYAML([]GalleryBackend{
			longcatVideo,
			{
				Metadata:        Metadata{Name: "amd-only"},
				CapabilitiesMap: map[string]string{"amd": "amd-only-rocm"},
			},
		})

		backends, err := AvailableBackendsForCapabilities(galleries, controller, []string{"nvidia", "amd"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(backends)).To(ContainElements("longcat-video", "amd-only"))
	})

	It("still excludes a meta no node can satisfy", func() {
		backends, err := AvailableBackendsForCapabilities(galleries, controller, []string{"amd"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(backends)).NotTo(ContainElement("longcat-video"))
	})

	It("filters concrete backends by node capability too", func() {
		writeGalleryYAML([]GalleryBackend{
			{Metadata: Metadata{Name: "some-backend-cuda"}, URI: "quay.io/test/cuda"},
			{Metadata: Metadata{Name: "some-backend-rocm"}, URI: "quay.io/test/rocm"},
		})

		backends, err := AvailableBackendsForCapabilities(galleries, controller, []string{"nvidia"})
		Expect(err).NotTo(HaveOccurred())
		Expect(names(backends)).To(ContainElement("some-backend-cuda"))
		Expect(names(backends)).NotTo(ContainElement("some-backend-rocm"))
	})

	It("returns exactly the single-node listing when the node list is empty", func() {
		withNodes, err := AvailableBackendsForCapabilities(galleries, controller, []string{})
		Expect(err).NotTo(HaveOccurred())
		baseline, err := AvailableBackends(galleries, controller)
		Expect(err).NotTo(HaveOccurred())
		Expect(names(withNodes)).To(Equal(names(baseline)))
	})
})
