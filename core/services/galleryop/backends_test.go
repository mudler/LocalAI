package galleryop_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
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
			testBackend := []map[string]any{
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
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"test-backend", // gallery name
				"custom-name",  // name should not be allowed
				"",
				false,
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("specifying a name or alias is not supported for gallery backends"))
		})

		It("should fail when backend is not found in gallery", func() {
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"non-existent-backend",
				"",
				"",
				false,
			)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("with OCI image", func() {
		It("should fail when name is not provided for OCI image", func() {
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				"oci://quay.io/mudler/tests:localai-backend-test",
				"", // name is required for OCI images
				"",
				false,
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
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"", // name should be inferred as "source-backend"
				"",
				false,
			)
			// The function should at least attempt to install with the inferred name
			// Even if it fails for other reasons, it shouldn't fail due to missing name
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("name is required"))
			}
		})

		It("should use provided name when specified", func() {
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"custom-backend-name",
				"",
				false,
			)
			// The function should use the provided name
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("name is required"))
			}
		})

		It("should support alias when provided", func() {
			err := galleryop.InstallExternalBackend(
				context.Background(),
				galleries,
				systemState,
				ml,
				nil,
				testBackendPath,
				"custom-backend-name",
				"custom-alias",
				false,
			)
			// The function should accept alias for directory paths
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("alias is not supported"))
			}
		})
	})
})

var _ = Describe("ManagementOp with External Backend", func() {
	It("should have external backend fields in ManagementOp", func() {
		// Test that the ManagementOp struct has the new external backend fields
		op := galleryop.ManagementOp[string, string]{
			ExternalURI:   "oci://example.com/backend:latest",
			ExternalName:  "test-backend",
			ExternalAlias: "test-alias",
		}

		Expect(op.ExternalURI).To(Equal("oci://example.com/backend:latest"))
		Expect(op.ExternalName).To(Equal("test-backend"))
		Expect(op.ExternalAlias).To(Equal("test-alias"))
	})

	Context("TargetNodeID field", func() {
		It("defaults to empty string", func() {
			op := galleryop.ManagementOp[string, string]{
				ExternalURI: "oci://example.com/backend:latest",
			}
			Expect(op.TargetNodeID).To(BeEmpty())
		})

		It("preserves TargetNodeID across a channel send", func() {
			ch := make(chan galleryop.ManagementOp[string, string], 1)
			ch <- galleryop.ManagementOp[string, string]{
				GalleryElementName: "llama-cpp",
				TargetNodeID:       "node-abc-123",
			}
			received := <-ch
			Expect(received.TargetNodeID).To(Equal("node-abc-123"))
			Expect(received.GalleryElementName).To(Equal("llama-cpp"))
		})
	})

	Describe("NodeScopedKey", func() {
		It("builds a unique key per (nodeID, backend) pair", func() {
			Expect(galleryop.NodeScopedKey("node-a", "llama-cpp")).To(Equal("node:node-a:llama-cpp"))
			Expect(galleryop.NodeScopedKey("node-b", "llama-cpp")).To(Equal("node:node-b:llama-cpp"))
			Expect(galleryop.NodeScopedKey("node-a", "vllm")).To(Equal("node:node-a:vllm"))
		})

		It("handles backend names containing colons", func() {
			// Gallery IDs sometimes look like "official@llama-cpp"; nodeIDs are UUIDs
			// without colons, but the backend slug may contain anything. Splitting on
			// the first colon after the prefix MUST yield the full backend back.
			key := galleryop.NodeScopedKey("node-1", "official@llama-cpp:v2")
			node, backend, ok := galleryop.ParseNodeScopedKey(key)
			Expect(ok).To(BeTrue())
			Expect(node).To(Equal("node-1"))
			Expect(backend).To(Equal("official@llama-cpp:v2"))
		})

		It("rejects keys without the node prefix", func() {
			_, _, ok := galleryop.ParseNodeScopedKey("llama-cpp")
			Expect(ok).To(BeFalse())
			_, _, ok = galleryop.ParseNodeScopedKey("official@llama-cpp")
			Expect(ok).To(BeFalse())
		})

		It("rejects malformed node-prefixed keys", func() {
			_, _, ok := galleryop.ParseNodeScopedKey("node:only-one-segment")
			Expect(ok).To(BeFalse())
		})

		It("rejects keys with an empty nodeID segment", func() {
			_, _, ok := galleryop.ParseNodeScopedKey("node::llama-cpp")
			Expect(ok).To(BeFalse())
		})
	})
})
