package gallery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"
)

const (
	testImage = "quay.io/mudler/tests:localai-backend-test"
)

var _ = Describe("Gallery Backends", func() {
	var (
		tempDir     string
		galleries   []config.Gallery
		ml          *model.ModelLoader
		systemState *system.SystemState
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "gallery-test-*")
		Expect(err).NotTo(HaveOccurred())

		// Setup test galleries
		galleries = []config.Gallery{
			{
				Name: "test-gallery",
				URL:  "https://gist.githubusercontent.com/mudler/71d5376bc2aa168873fa519fa9f4bd56/raw/0557f9c640c159fa8e4eab29e8d98df6a3d6e80f/backend-gallery.yaml",
			},
		}
		systemState, err = system.GetSystemState(system.WithBackendPath(tempDir))
		Expect(err).NotTo(HaveOccurred())
		ml = model.NewModelLoader(systemState, true)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("InstallBackendFromGallery", func() {
		It("should return error when backend is not found", func() {
			err := InstallBackendFromGallery(galleries, systemState, ml, "non-existent", nil, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no backend found with name \"non-existent\""))
		})

		It("should install backend from gallery", func() {
			err := InstallBackendFromGallery(galleries, systemState, ml, "test-backend", nil, true)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "run.sh")).To(BeARegularFile())
		})
	})

	Describe("Meta Backends", func() {
		It("should identify meta backends correctly", func() {
			metaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "meta-backend",
				},
				CapabilitiesMap: map[string]string{
					"nvidia": "nvidia-backend",
					"amd":    "amd-backend",
					"intel":  "intel-backend",
				},
			}

			Expect(metaBackend.IsMeta()).To(BeTrue())

			regularBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "regular-backend",
				},
				URI: testImage,
			}

			Expect(regularBackend.IsMeta()).To(BeFalse())

			emptyMetaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "empty-meta-backend",
				},
				CapabilitiesMap: map[string]string{},
			}

			Expect(emptyMetaBackend.IsMeta()).To(BeFalse())

			nilMetaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "nil-meta-backend",
				},
				CapabilitiesMap: nil,
			}

			Expect(nilMetaBackend.IsMeta()).To(BeFalse())
		})

		It("should find best backend from meta based on system capabilities", func() {

			metaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "meta-backend",
				},
				CapabilitiesMap: map[string]string{
					"nvidia":  "nvidia-backend",
					"amd":     "amd-backend",
					"intel":   "intel-backend",
					"metal":   "metal-backend",
					"default": "default-backend",
				},
			}

			nvidiaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "nvidia-backend",
				},
				URI: testImage,
			}

			amdBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "amd-backend",
				},
				URI: testImage,
			}

			metalBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "metal-backend",
				},
				URI: testImage,
			}

			defaultBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "default-backend",
				},
				URI: testImage,
			}

			backends := GalleryElements[*GalleryBackend]{nvidiaBackend, amdBackend, metalBackend, defaultBackend}

			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				metal := &system.SystemState{}
				bestBackend := metaBackend.FindBestBackendFromMeta(metal, backends)
				Expect(bestBackend).To(Equal(metalBackend))

			} else {
				// Test with NVIDIA system state
				nvidiaSystemState := &system.SystemState{GPUVendor: "nvidia", VRAM: 1000000000000}
				bestBackend := metaBackend.FindBestBackendFromMeta(nvidiaSystemState, backends)
				Expect(bestBackend).To(Equal(nvidiaBackend))

				// Test with AMD system state
				amdSystemState := &system.SystemState{GPUVendor: "amd", VRAM: 1000000000000}
				bestBackend = metaBackend.FindBestBackendFromMeta(amdSystemState, backends)
				Expect(bestBackend).To(Equal(amdBackend))

				// Test with default system state (not enough VRAM)
				defaultSystemState := &system.SystemState{GPUVendor: "amd"}
				bestBackend = metaBackend.FindBestBackendFromMeta(defaultSystemState, backends)
				Expect(bestBackend).To(Equal(defaultBackend))

				// Test with default system state
				defaultSystemState = &system.SystemState{GPUVendor: "default"}
				bestBackend = metaBackend.FindBestBackendFromMeta(defaultSystemState, backends)
				Expect(bestBackend).To(Equal(defaultBackend))

				backends = GalleryElements[*GalleryBackend]{nvidiaBackend, amdBackend, metalBackend}
				// Test with unsupported GPU vendor
				unsupportedSystemState := &system.SystemState{GPUVendor: "unsupported"}
				bestBackend = metaBackend.FindBestBackendFromMeta(unsupportedSystemState, backends)
				Expect(bestBackend).To(BeNil())
			}
		})

		It("should handle meta backend deletion correctly", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}

			metaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "meta-backend",
				},
				CapabilitiesMap: map[string]string{
					"nvidia": "nvidia-backend",
					"amd":    "amd-backend",
					"intel":  "intel-backend",
				},
			}

			nvidiaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "nvidia-backend",
				},
				URI: testImage,
			}

			amdBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "amd-backend",
				},
				URI: testImage,
			}

			gallery := config.Gallery{
				Name: "test-gallery",
				URL:  "file://" + filepath.Join(tempDir, "backend-gallery.yaml"),
			}

			galleryBackend := GalleryBackends{amdBackend, nvidiaBackend, metaBackend}

			dat, err := yaml.Marshal(galleryBackend)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempDir, "backend-gallery.yaml"), dat, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Test with NVIDIA system state
			nvidiaSystemState := &system.SystemState{
				GPUVendor: "nvidia",
				VRAM:      1000000000000,
				Backend:   system.Backend{BackendsPath: tempDir},
			}
			err = InstallBackendFromGallery([]config.Gallery{gallery}, nvidiaSystemState, ml, "meta-backend", nil, true)
			Expect(err).NotTo(HaveOccurred())

			metaBackendPath := filepath.Join(tempDir, "meta-backend")
			Expect(metaBackendPath).To(BeADirectory())

			concreteBackendPath := filepath.Join(tempDir, "nvidia-backend")
			Expect(concreteBackendPath).To(BeADirectory())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())

			allBackends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(allBackends).To(HaveKey("meta-backend"))
			Expect(allBackends).To(HaveKey("nvidia-backend"))

			// Delete meta backend by name
			err = DeleteBackendFromSystem(systemState, "meta-backend")
			Expect(err).NotTo(HaveOccurred())

			// Verify meta backend directory is deleted
			Expect(metaBackendPath).NotTo(BeADirectory())

			// Verify concrete backend directory is deleted
			Expect(concreteBackendPath).NotTo(BeADirectory())
		})

		It("should handle meta backend deletion correctly with aliases", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}
			metaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "meta-backend",
				},
				Alias: "backend-alias",
				CapabilitiesMap: map[string]string{
					"nvidia": "nvidia-backend",
					"amd":    "amd-backend",
					"intel":  "intel-backend",
				},
			}

			nvidiaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "nvidia-backend",
				},
				Alias: "backend-alias",
				URI:   testImage,
			}

			amdBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "amd-backend",
				},
				Alias: "backend-alias",
				URI:   testImage,
			}

			gallery := config.Gallery{
				Name: "test-gallery",
				URL:  "file://" + filepath.Join(tempDir, "backend-gallery.yaml"),
			}

			galleryBackend := GalleryBackends{amdBackend, nvidiaBackend, metaBackend}

			dat, err := yaml.Marshal(galleryBackend)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempDir, "backend-gallery.yaml"), dat, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Test with NVIDIA system state
			nvidiaSystemState := &system.SystemState{
				GPUVendor: "nvidia",
				VRAM:      1000000000000,
				Backend:   system.Backend{BackendsPath: tempDir},
			}
			err = InstallBackendFromGallery([]config.Gallery{gallery}, nvidiaSystemState, ml, "meta-backend", nil, true)
			Expect(err).NotTo(HaveOccurred())

			metaBackendPath := filepath.Join(tempDir, "meta-backend")
			Expect(metaBackendPath).To(BeADirectory())

			concreteBackendPath := filepath.Join(tempDir, "nvidia-backend")
			Expect(concreteBackendPath).To(BeADirectory())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())

			allBackends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(allBackends).To(HaveKey("meta-backend"))
			Expect(allBackends).To(HaveKey("nvidia-backend"))
			mback, exists := allBackends.Get("meta-backend")
			Expect(exists).To(BeTrue())
			Expect(mback.IsMeta).To(BeTrue())
			Expect(mback.Metadata.MetaBackendFor).To(Equal("nvidia-backend"))

			// Delete meta backend by name
			err = DeleteBackendFromSystem(systemState, "meta-backend")
			Expect(err).NotTo(HaveOccurred())

			// Verify meta backend directory is deleted
			Expect(metaBackendPath).NotTo(BeADirectory())

			// Verify concrete backend directory is deleted
			Expect(concreteBackendPath).NotTo(BeADirectory())
		})

		It("should handle meta backend deletion correctly with aliases pointing to the same backend", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}
			metaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "meta-backend",
				},
				Alias: "meta-backend",
				CapabilitiesMap: map[string]string{
					"nvidia": "nvidia-backend",
					"amd":    "amd-backend",
					"intel":  "intel-backend",
				},
			}

			nvidiaBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "nvidia-backend",
				},
				Alias: "meta-backend",
				URI:   testImage,
			}

			amdBackend := &GalleryBackend{
				Metadata: Metadata{
					Name: "amd-backend",
				},
				Alias: "meta-backend",
				URI:   testImage,
			}

			gallery := config.Gallery{
				Name: "test-gallery",
				URL:  "file://" + filepath.Join(tempDir, "backend-gallery.yaml"),
			}

			galleryBackend := GalleryBackends{amdBackend, nvidiaBackend, metaBackend}

			dat, err := yaml.Marshal(galleryBackend)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempDir, "backend-gallery.yaml"), dat, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Test with NVIDIA system state
			nvidiaSystemState := &system.SystemState{
				GPUVendor: "nvidia",
				VRAM:      1000000000000,
				Backend:   system.Backend{BackendsPath: tempDir},
			}
			err = InstallBackendFromGallery([]config.Gallery{gallery}, nvidiaSystemState, ml, "meta-backend", nil, true)
			Expect(err).NotTo(HaveOccurred())

			metaBackendPath := filepath.Join(tempDir, "meta-backend")
			Expect(metaBackendPath).To(BeADirectory())

			concreteBackendPath := filepath.Join(tempDir, "nvidia-backend")
			Expect(concreteBackendPath).To(BeADirectory())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())

			allBackends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(allBackends).To(HaveKey("meta-backend"))
			Expect(allBackends).To(HaveKey("nvidia-backend"))
			mback, exists := allBackends.Get("meta-backend")
			Expect(exists).To(BeTrue())
			Expect(mback.RunFile).To(Equal(filepath.Join(tempDir, "nvidia-backend", "run.sh")))

			// Delete meta backend by name
			err = DeleteBackendFromSystem(systemState, "meta-backend")
			Expect(err).NotTo(HaveOccurred())

			// Verify meta backend directory is deleted
			Expect(metaBackendPath).NotTo(BeADirectory())

			// Verify concrete backend directory is deleted
			Expect(concreteBackendPath).NotTo(BeADirectory())
		})

		It("should list meta backends correctly in system backends", func() {
			// Create a meta backend directory with metadata
			metaBackendPath := filepath.Join(tempDir, "meta-backend")
			err := os.MkdirAll(metaBackendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			// Create metadata file pointing to concrete backend
			metadata := &BackendMetadata{
				MetaBackendFor: "concrete-backend",
				Name:           "meta-backend",
				InstalledAt:    "2023-01-01T00:00:00Z",
			}
			metadataData, err := json.Marshal(metadata)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(metaBackendPath, "metadata.json"), metadataData, 0644)
			Expect(err).NotTo(HaveOccurred())

			// Create the concrete backend directory with run.sh
			concreteBackendPath := filepath.Join(tempDir, "concrete-backend")
			err = os.MkdirAll(concreteBackendPath, 0750)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(concreteBackendPath, "metadata.json"), []byte("{}"), 0755)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(concreteBackendPath, "run.sh"), []byte(""), 0755)
			Expect(err).NotTo(HaveOccurred())

			// List system backends
			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())

			backends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())

			metaBackend, exists := backends.Get("meta-backend")
			concreteBackendRunFile := filepath.Join(tempDir, "concrete-backend", "run.sh")

			// Should include both the meta backend name and concrete backend name
			Expect(exists).To(BeTrue())
			Expect(backends.Exists("concrete-backend")).To(BeTrue())

			// meta-backend should be empty
			Expect(metaBackend.IsMeta).To(BeTrue())
			Expect(metaBackend.RunFile).To(Equal(concreteBackendRunFile))
			// concrete-backend should point to its own run.sh
			concreteBackend, exists := backends.Get("concrete-backend")
			Expect(exists).To(BeTrue())
			Expect(concreteBackend.RunFile).To(Equal(concreteBackendRunFile))
		})
	})

	Describe("InstallBackend", func() {
		It("should create base path if it doesn't exist", func() {
			newPath := filepath.Join(tempDir, "new-path")
			backend := GalleryBackend{
				Metadata: Metadata{
					Name: "test-backend",
				},
				URI: "test-uri",
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(newPath),
			)
			Expect(err).NotTo(HaveOccurred())
			err = InstallBackend(systemState, ml, &backend, nil)
			Expect(err).To(HaveOccurred()) // Will fail due to invalid URI, but path should be created
			Expect(newPath).To(BeADirectory())
		})

		It("should overwrite existing backend", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}
			newPath := filepath.Join(tempDir, "test-backend")

			// Create a dummy backend directory
			err := os.MkdirAll(newPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(newPath, "metadata.json"), []byte("foo"), 0644)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(newPath, "run.sh"), []byte(""), 0644)
			Expect(err).NotTo(HaveOccurred())

			backend := GalleryBackend{
				Metadata: Metadata{
					Name: "test-backend",
				},
				URI:   "quay.io/mudler/tests:localai-backend-test",
				Alias: "test-alias",
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			err = InstallBackend(systemState, ml, &backend, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "metadata.json")).To(BeARegularFile())
			dat, err := os.ReadFile(filepath.Join(tempDir, "test-backend", "metadata.json"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(dat)).ToNot(Equal("foo"))
		})

		It("should overwrite existing backend", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}
			newPath := filepath.Join(tempDir, "test-backend")

			// Create a dummy backend directory
			err := os.MkdirAll(newPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			backend := GalleryBackend{
				Metadata: Metadata{
					Name: "test-backend",
				},
				URI:   "quay.io/mudler/tests:localai-backend-test",
				Alias: "test-alias",
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(filepath.Join(tempDir, "test-backend", "metadata.json")).ToNot(BeARegularFile())

			err = InstallBackend(systemState, ml, &backend, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "metadata.json")).To(BeARegularFile())
		})

		It("should create alias file when specified", func() {
			if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
				Skip("Skipping test on darwin/arm64")
			}
			backend := GalleryBackend{
				Metadata: Metadata{
					Name: "test-backend",
				},
				URI:   "quay.io/mudler/tests:localai-backend-test",
				Alias: "test-alias",
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			err = InstallBackend(systemState, ml, &backend, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "metadata.json")).To(BeARegularFile())

			// Read and verify metadata
			metadataData, err := os.ReadFile(filepath.Join(tempDir, "test-backend", "metadata.json"))
			Expect(err).ToNot(HaveOccurred())
			var metadata BackendMetadata
			err = json.Unmarshal(metadataData, &metadata)
			Expect(err).ToNot(HaveOccurred())
			Expect(metadata.Alias).To(Equal("test-alias"))
			Expect(metadata.Name).To(Equal("test-backend"))

			Expect(filepath.Join(tempDir, "test-backend", "run.sh")).To(BeARegularFile())

			// Check that the alias was recognized
			backends, err := ListSystemBackends(systemState)
			Expect(err).ToNot(HaveOccurred())
			aliasBackend, exists := backends.Get("test-alias")
			Expect(exists).To(BeTrue())
			Expect(aliasBackend.RunFile).To(Equal(filepath.Join(tempDir, "test-backend", "run.sh")))
			testB, exists := backends.Get("test-backend")
			Expect(exists).To(BeTrue())
			Expect(testB.RunFile).To(Equal(filepath.Join(tempDir, "test-backend", "run.sh")))
		})
	})

	Describe("DeleteBackendFromSystem", func() {
		It("should delete backend directory", func() {
			backendName := "test-backend"
			backendPath := filepath.Join(tempDir, backendName)

			// Create a dummy backend directory
			err := os.MkdirAll(backendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(backendPath, "metadata.json"), []byte("{}"), 0644)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(backendPath, "run.sh"), []byte(""), 0644)
			Expect(err).NotTo(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			err = DeleteBackendFromSystem(systemState, backendName)
			Expect(err).NotTo(HaveOccurred())
			Expect(backendPath).NotTo(BeADirectory())
		})

		It("should not error when backend doesn't exist", func() {
			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			err = DeleteBackendFromSystem(systemState, "non-existent")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ListSystemBackends", func() {
		It("should list backends without aliases", func() {
			// Create some dummy backend directories
			backendNames := []string{"backend1", "backend2", "backend3"}
			for _, name := range backendNames {
				err := os.MkdirAll(filepath.Join(tempDir, name), 0750)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, name, "metadata.json"), []byte("{}"), 0755)
				Expect(err).NotTo(HaveOccurred())
				err = os.WriteFile(filepath.Join(tempDir, name, "run.sh"), []byte(""), 0755)
				Expect(err).NotTo(HaveOccurred())
			}

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			backends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			Expect(backends).To(HaveLen(len(backendNames)))

			for _, name := range backendNames {
				backend, exists := backends.Get(name)
				Expect(exists).To(BeTrue())
				Expect(backend.RunFile).To(Equal(filepath.Join(tempDir, name, "run.sh")))
			}
		})

		It("should handle backends with aliases", func() {
			backendName := "backend1"
			alias := "alias1"
			backendPath := filepath.Join(tempDir, backendName)

			// Create backend directory
			err := os.MkdirAll(backendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			// Create metadata file with alias
			metadata := &BackendMetadata{
				Alias:       alias,
				Name:        backendName,
				InstalledAt: "2023-01-01T00:00:00Z",
			}
			metadataData, err := json.Marshal(metadata)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(backendPath, "metadata.json"), metadataData, 0644)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(backendPath, "run.sh"), []byte(""), 0755)
			Expect(err).NotTo(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithBackendPath(tempDir),
			)
			Expect(err).NotTo(HaveOccurred())
			backends, err := ListSystemBackends(systemState)
			Expect(err).NotTo(HaveOccurred())
			backend, exists := backends.Get(alias)
			Expect(exists).To(BeTrue())
			Expect(backend.RunFile).To(Equal(filepath.Join(tempDir, backendName, "run.sh")))
		})

		It("should return error when base path doesn't exist", func() {
			systemState, err := system.GetSystemState(
				system.WithBackendPath("foobardir"),
			)
			Expect(err).NotTo(HaveOccurred())
			_, err = ListSystemBackends(systemState)
			Expect(err).To(HaveOccurred())
		})
	})
})
