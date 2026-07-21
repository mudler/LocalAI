package oci_test

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/pkg/oci"
	. "github.com/mudler/LocalAI/pkg/oci" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {

	Context("when template is loaded successfully", func() {
		It("should evaluate the template correctly", func() {
			var layerTar bytes.Buffer
			writer := tar.NewWriter(&layerTar)
			content := []byte("offline OCI fixture\n")
			Expect(writer.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0o644, Size: int64(len(content))})).To(Succeed())
			_, err := writer.Write(content)
			Expect(err).NotTo(HaveOccurred())
			Expect(writer.Close()).To(Succeed())

			layer, err := tarball.LayerFromReader(bytes.NewReader(layerTar.Bytes()))
			Expect(err).NotTo(HaveOccurred())
			img, err := mutate.AppendLayers(empty.Image, layer)
			Expect(err).NotTo(HaveOccurred())
			size, err := layer.Size()
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(BeNumerically(">", 0))

			// Create tempdir
			dir, err := os.MkdirTemp("", "example")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, dir)

			err = ExtractOCIImage(context.TODO(), img, "fixture:offline", dir, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.ReadFile(filepath.Join(dir, "fixture.txt"))).To(Equal(content))
		})
	})
})

var _ = Describe("GetImageDigest", func() {
	It("returns an error for an invalid image reference", func() {
		_, err := oci.GetImageDigest("!!!invalid-ref!!!", "", nil, nil)
		Expect(err).To(HaveOccurred())
	})
})
