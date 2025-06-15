package gallery

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Gallery Backends", func() {
	var (
		tempDir   string
		galleries []config.Gallery
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
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("InstallBackendFromGallery", func() {
		It("should return error when backend is not found", func() {
			err := InstallBackendFromGallery(galleries, "non-existent", tempDir, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no model found with name"))
		})

		It("should install backend from gallery", func() {
			err := InstallBackendFromGallery(galleries, "test-backend", tempDir, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "run.sh")).To(BeARegularFile())
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

			err := InstallBackend(newPath, &backend, nil)
			Expect(err).To(HaveOccurred()) // Will fail due to invalid URI, but path should be created
			Expect(newPath).To(BeADirectory())
		})

		It("should create alias file when specified", func() {
			backend := GalleryBackend{
				Metadata: Metadata{
					Name: "test-backend",
				},
				URI:   "quay.io/mudler/tests:localai-backend-test",
				Alias: "test-alias",
			}

			err := InstallBackend(tempDir, &backend, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Join(tempDir, "test-backend", "alias")).To(BeARegularFile())
			content, err := os.ReadFile(filepath.Join(tempDir, "test-backend", "alias"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("test-alias"))
			Expect(filepath.Join(tempDir, "test-backend", "run.sh")).To(BeARegularFile())

			// Check that the alias was recognized
			backends, err := ListSystemBackends(tempDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(backends).To(HaveKeyWithValue("test-alias", filepath.Join(tempDir, "test-backend", "run.sh")))
			Expect(backends).To(HaveKeyWithValue("test-backend", filepath.Join(tempDir, "test-backend", "run.sh")))
		})
	})

	Describe("DeleteBackendFromSystem", func() {
		It("should delete backend directory", func() {
			backendName := "test-backend"
			backendPath := filepath.Join(tempDir, backendName)

			// Create a dummy backend directory
			err := os.MkdirAll(backendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			err = DeleteBackendFromSystem(tempDir, backendName)
			Expect(err).NotTo(HaveOccurred())
			Expect(backendPath).NotTo(BeADirectory())
		})

		It("should not error when backend doesn't exist", func() {
			err := DeleteBackendFromSystem(tempDir, "non-existent")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ListSystemBackends", func() {
		It("should list backends without aliases", func() {
			// Create some dummy backend directories
			backendNames := []string{"backend1", "backend2", "backend3"}
			for _, name := range backendNames {
				err := os.MkdirAll(filepath.Join(tempDir, name), 0750)
				Expect(err).NotTo(HaveOccurred())
			}

			backends, err := ListSystemBackends(tempDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(backends).To(HaveLen(len(backendNames)))

			for _, name := range backendNames {
				Expect(backends).To(HaveKeyWithValue(name, filepath.Join(tempDir, name, "run.sh")))
			}
		})

		It("should handle backends with aliases", func() {
			backendName := "backend1"
			alias := "alias1"
			backendPath := filepath.Join(tempDir, backendName)

			// Create backend directory
			err := os.MkdirAll(backendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			// Create alias file
			err = os.WriteFile(filepath.Join(backendPath, "alias"), []byte(alias), 0644)
			Expect(err).NotTo(HaveOccurred())

			backends, err := ListSystemBackends(tempDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(backends).To(HaveKeyWithValue(alias, filepath.Join(tempDir, backendName, "run.sh")))
		})

		It("should return error when base path doesn't exist", func() {
			_, err := ListSystemBackends(filepath.Join(tempDir, "non-existent"))
			Expect(err).To(HaveOccurred())
		})
	})
})
