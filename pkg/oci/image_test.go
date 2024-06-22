package oci_test

import (
	"os"
	"runtime"

	. "github.com/mudler/LocalAI/pkg/oci" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {

	Context("when template is loaded successfully", func() {
		It("should evaluate the template correctly", func() {
			if runtime.GOOS == "darwin" {
				Skip("Skipping test on darwin")
			}
			imageName := "alpine"
			img, err := GetImage(imageName, "", nil, nil)
			Expect(err).NotTo(HaveOccurred())

			size, err := GetOCIImageSize(imageName, "", nil, nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(size).ToNot(Equal(int64(0)))

			// Create tempdir
			dir, err := os.MkdirTemp("", "example")
			Expect(err).NotTo(HaveOccurred())
			defer os.RemoveAll(dir)

			err = ExtractOCIImage(img, dir)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
