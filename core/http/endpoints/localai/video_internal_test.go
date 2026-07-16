package localai

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("video media staging", func() {
	It("stages raw base64 into a private temporary file", func() {
		directory := GinkgoT().TempDir()
		content := []byte("avatar audio")

		path, err := stageVideoMedia(context.Background(), directory, base64.StdEncoding.EncodeToString(content))

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Remove, path)
		Expect(filepath.Dir(path)).To(Equal(directory))
		Expect(os.ReadFile(path)).To(Equal(content))
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("accepts browser data URIs with codec parameters", func() {
		content := []byte("recorded speech")
		ref := "data:audio/webm;codecs=opus;base64," + base64.StdEncoding.EncodeToString(content)

		path, err := stageVideoMedia(context.Background(), GinkgoT().TempDir(), ref)

		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Remove, path)
		Expect(os.ReadFile(path)).To(Equal(content))
	})

	It("rejects malformed base64 and removes the partial file", func() {
		directory := GinkgoT().TempDir()

		_, err := stageVideoMedia(context.Background(), directory, "not%%%base64")

		Expect(err).To(MatchError(ContainSubstring("decoding media")))
		entries, readErr := os.ReadDir(directory)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("enforces the configured streaming limit", func() {
		directory := GinkgoT().TempDir()
		encoded := base64.StdEncoding.EncodeToString([]byte("four"))

		_, err := stageVideoMediaWithLimit(context.Background(), directory, encoded, 3)

		Expect(err).To(MatchError(ContainSubstring("3-byte limit")))
		entries, readErr := os.ReadDir(directory)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("rejects non-base64 data URIs", func() {
		_, err := stageVideoMedia(context.Background(), GinkgoT().TempDir(), "data:audio/wav,plain")

		Expect(err).To(MatchError(ContainSubstring("base64 payload")))
	})
})
