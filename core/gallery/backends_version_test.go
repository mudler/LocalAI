package gallery_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/oci"
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

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil, false)
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

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil, false)
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

		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil, false)
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

	It("installs a backend from an ocifile tar without recording a digest", func() {
		// Build a minimal OCI image tar the way `local-ai util
		// create-oci-image` does, with run.sh at the layer root (the layout of
		// the shipped backend-image tars).
		archivePath := filepath.Join(tempDir, "archive.tar")
		archiveFile, err := os.Create(archivePath)
		Expect(err).NotTo(HaveOccurred())
		runSh := []byte("#!/bin/sh\necho ok\n")
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		Expect(tw.WriteHeader(&tar.Header{Name: "run.sh", Mode: 0755, Size: int64(len(runSh))})).To(Succeed())
		_, err = tw.Write(runSh)
		Expect(err).NotTo(HaveOccurred())
		Expect(tw.Close()).To(Succeed())
		_, err = archiveFile.Write(buf.Bytes())
		Expect(err).NotTo(HaveOccurred())
		Expect(archiveFile.Close()).To(Succeed())

		imageTar := filepath.Join(tempDir, "image.tar")
		Expect(oci.CreateTar(archivePath, imageTar, "localai/test:latest", "amd64", "windows")).To(Succeed())

		backend := &gallery.GalleryBackend{}
		backend.Name = "test-ocifile"
		backend.URI = "ocifile://" + imageTar

		// The whole point of the ocifile scheme is a local stream: no
		// registry digest exists to record, and the installer must not fail
		// (or warn) trying to resolve one for a path that cannot be parsed as
		// a registry reference.
		err = gallery.InstallBackend(context.Background(), systemState, modelLoader, backend, nil, false)
		Expect(err).NotTo(HaveOccurred())

		metadataPath := filepath.Join(tempDir, "test-ocifile", "metadata.json")
		data, err := os.ReadFile(metadataPath)
		Expect(err).NotTo(HaveOccurred())

		var metadata map[string]any
		Expect(json.Unmarshal(data, &metadata)).To(Succeed())
		_, hasDigest := metadata["digest"]
		Expect(hasDigest).To(BeFalse())

		// The staged run.sh must have landed through the tar extraction.
		_, err = os.Stat(filepath.Join(tempDir, "test-ocifile", "run.sh"))
		Expect(err).NotTo(HaveOccurred())
	})
})
