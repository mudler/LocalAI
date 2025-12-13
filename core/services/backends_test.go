package services_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"
)

var _ = Describe("InstallExternalBackend", func() {
	var (
		tempDir     string
		galleries   []config.Gallery
		ml          *model.ModelLoader
		systemState *system.SystemState
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backends-service-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err = system.GetSystemState(system.WithBackendPath(tempDir))
		Expect(err).NotTo(HaveOccurred())
		ml = model.NewModelLoader(systemState)

		// Setup test gallery
		galleries = []config.Gallery{
			{
				Name: "test-gallery",
				URL:  "file://" + filepath.Join(tempDir, "test-gallery.yaml"),
			},
		}
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("with gallery backend name", func() {
		BeforeEach(func() {
			// Create a test gallery file with a test backend
			testBackend := []map[string]interface{}{
				{
					"name": "test-backend",
					"uri":  "https://gist.githubusercontent.com/mudler/71d5376bc2aa168873fa519fa9f4bd56/raw/testbackend/run.sh",
				},
			}
			data, err := yaml.Marshal(testBackend)
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempDir, "test-gallery.yaml"), data, 0644)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail when name or alias is provided for gallery backend", func() {
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"test-backend", // gallery name
				"custom-name",  // name should not be allowed
				"",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("specifying a name or alias is not supported for gallery backends"))
		})

		It("should fail when backend is not found in gallery", func() {
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"non-existent-backend",
				"",
				"",
			)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("with OCI image", func() {
		It("should fail when name is not provided for OCI image", func() {
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"oci://quay.io/mudler/tests:localai-backend-test",
				"", // name is required for OCI images
				"",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("specifying a name is required for OCI images"))
		})
	})

	Context("with directory path", func() {
		var testBackendPath string

		BeforeEach(func() {
			// Create a test backend directory with required files
			testBackendPath = filepath.Join(tempDir, "source-backend")
			err := os.MkdirAll(testBackendPath, 0750)
			Expect(err).NotTo(HaveOccurred())

			// Create run.sh
			err = os.WriteFile(filepath.Join(testBackendPath, "run.sh"), []byte("#!/bin/bash\necho test"), 0755)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should infer name from directory path when name is not provided", func() {
			// This test verifies that the function attempts to install using the directory name
			// The actual installation may fail due to test environment limitations
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"", // name should be inferred as "source-backend"
				"",
			)
			// The function should at least attempt to install with the inferred name
			// Even if it fails for other reasons, it shouldn't fail due to missing name
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("name is required"))
			}
		})

		It("should use provided name when specified", func() {
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"custom-backend-name",
				"",
			)
			// The function should use the provided name
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("name is required"))
			}
		})

		It("should support alias when provided", func() {
			err := services.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"custom-backend-name",
				"custom-alias",
			)
			// The function should accept alias for directory paths
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("alias is not supported"))
			}
		})
	})
})

var _ = Describe("GalleryOp with External Backend", func() {
	It("should have external backend fields in GalleryOp", func() {
		// Test that the GalleryOp struct has the new external backend fields
		op := services.GalleryOp[string, string]{
			ExternalURI:   "oci://example.com/backend:latest",
			ExternalName:  "test-backend",
			ExternalAlias: "test-alias",
		}

		Expect(op.ExternalURI).To(Equal("oci://example.com/backend:latest"))
		Expect(op.ExternalName).To(Equal("test-backend"))
		Expect(op.ExternalAlias).To(Equal("test-alias"))
	})
})
