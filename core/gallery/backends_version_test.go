package gallery_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend versioning", func() {
	var tempDir string
	var systemState *system.SystemState
	var modelLoader *model.ModelLoader

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "gallery-version-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err = system.GetSystemState(
			system.WithBackendPath(tempDir),
		)
		Expect(err).NotTo(HaveOccurred())
		modelLoader = model.NewModelLoader(systemState)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("records version in metadata when installing a backend with a version", func() {
		// Create a fake backend source directory with a run.sh
		srcDir, err := os.MkdirTemp("", "gallery-version-src-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(srcDir)
		err = os.WriteFile(filepath.Join(srcDir, "run.sh"), []byte("#!/bin/sh\necho ok"), 0755)
		Expect(err).NotTo(HaveOccurred())

		backend := &gallery.GalleryBackend{}
		backend.Name = "test-backend"
		backend.URI = srcDir
		backend.Version = "1.2.3"

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil)
		Expect(err).NotTo(HaveOccurred())

		// Read the metadata file and check version
		metadataPath := filepath.Join(tempDir, "test-backend", "metadata.json")
		data, err := os.ReadFile(metadataPath)
		Expect(err).NotTo(HaveOccurred())

		var metadata map[string]any
		err = json.Unmarshal(data, &metadata)
		Expect(err).NotTo(HaveOccurred())

		Expect(metadata["version"]).To(Equal("1.2.3"))
	})

	It("records URI in metadata", func() {
		srcDir, err := os.MkdirTemp("", "gallery-version-src-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(srcDir)
		err = os.WriteFile(filepath.Join(srcDir, "run.sh"), []byte("#!/bin/sh\necho ok"), 0755)
		Expect(err).NotTo(HaveOccurred())

		backend := &gallery.GalleryBackend{}
		backend.Name = "test-backend-uri"
		backend.URI = srcDir
		backend.Version = "2.0.0"

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil)
		Expect(err).NotTo(HaveOccurred())

		metadataPath := filepath.Join(tempDir, "test-backend-uri", "metadata.json")
		data, err := os.ReadFile(metadataPath)
		Expect(err).NotTo(HaveOccurred())

		var metadata map[string]any
		err = json.Unmarshal(data, &metadata)
		Expect(err).NotTo(HaveOccurred())

		Expect(metadata["uri"]).To(Equal(srcDir))
	})

	It("omits version key when version is empty", func() {
		srcDir, err := os.MkdirTemp("", "gallery-version-src-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(srcDir)
		err = os.WriteFile(filepath.Join(srcDir, "run.sh"), []byte("#!/bin/sh\necho ok"), 0755)
		Expect(err).NotTo(HaveOccurred())

		backend := &gallery.GalleryBackend{}
		backend.Name = "test-backend-noversion"
		backend.URI = srcDir
		// Version intentionally left empty

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil)
		Expect(err).NotTo(HaveOccurred())

		metadataPath := filepath.Join(tempDir, "test-backend-noversion", "metadata.json")
		data, err := os.ReadFile(metadataPath)
		Expect(err).NotTo(HaveOccurred())

		var metadata map[string]any
		err = json.Unmarshal(data, &metadata)
		Expect(err).NotTo(HaveOccurred())

		// omitempty should exclude the version key entirely
		_, hasVersion := metadata["version"]
		Expect(hasVersion).To(BeFalse())
	})
})
