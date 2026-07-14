package openai

import (
	"encoding/base64"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("processImageFile", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "processimage")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("should decode base64 and write all bytes to disk", func() {
		// 4x4 red pixel PNG (68 bytes raw) — small enough to fit in bufio's
		// default 4096-byte buffer, which is exactly the scenario where a
		// missing Flush() produces a 0-byte file.
		pngBytes := []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
			0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
			0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x04,
			0x08, 0x02, 0x00, 0x00, 0x00, 0x26, 0x93, 0x09,
			0x29, 0x00, 0x00, 0x00, 0x1c, 0x49, 0x44, 0x41, // IDAT chunk
			0x54, 0x78, 0x9c, 0x62, 0xf8, 0xcf, 0xc0, 0xc0,
			0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
			0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0x00, 0x00,
			0x00, 0x31, 0x00, 0x01, 0x2e, 0xa8, 0xd1, 0xe5,
			0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, // IEND chunk
			0xae, 0x42, 0x60, 0x82,
		}
		b64 := base64.StdEncoding.EncodeToString(pngBytes)

		outPath := processImageFile(b64, tmpDir)
		Expect(outPath).ToNot(BeEmpty(), "processImageFile should return a file path")

		written, err := os.ReadFile(outPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(written).To(Equal(pngBytes), "file on disk must match the original bytes")
	})

	It("should return empty string for invalid base64", func() {
		outPath := processImageFile("not-valid-base64!!!", tmpDir)
		Expect(outPath).To(BeEmpty(), "should return empty string for invalid base64")
	})
})
