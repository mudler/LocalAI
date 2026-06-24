package downloader_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("file:// sources", func() {
	var noProgress = func(string, string, string, float64) {}

	It("copies a file:// source into a destination that does not exist yet", func() {
		// the layout from issue #11701: the source sits in a nested directory,
		// the destination is the flat models dir, so it cannot already exist
		srcDir := GinkgoT().TempDir()
		nested := filepath.Join(srcDir, "InternScience", "Agents-A1-4B-Q8_0-GGUF")
		Expect(os.MkdirAll(nested, 0o755)).To(Succeed())
		srcPath := filepath.Join(nested, "Agents-A1-4B-Q8_0.gguf")
		payload := []byte("GGUF-not-really-but-enough-bytes")
		Expect(os.WriteFile(srcPath, payload, 0o600)).To(Succeed())

		sum := sha256.Sum256(payload)
		destPath := filepath.Join(GinkgoT().TempDir(), "Agents-A1-4B-Q8_0.gguf")

		uri := URI("file://" + srcPath)
		Expect(uri.DownloadFile(destPath, hex.EncodeToString(sum[:]), 1, 1, noProgress)).To(Succeed())

		got, err := os.ReadFile(destPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(payload))
	})

	It("reports the missing source when a file:// source does not exist", func() {
		destPath := filepath.Join(GinkgoT().TempDir(), "absent.gguf")
		missing := filepath.Join(GinkgoT().TempDir(), "absent.gguf")

		err := URI("file://"+missing).DownloadFile(destPath, "", 1, 1, noProgress)
		Expect(err).To(HaveOccurred())
		// the error must name the source that could not be read, not claim the
		// destination is an unrecognized URL
		Expect(err.Error()).To(ContainSubstring(missing))
	})
})
