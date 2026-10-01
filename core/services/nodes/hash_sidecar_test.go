package nodes

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hash sidecar containment", func() {
	DescribeTable("does not trust or overwrite a sidecar outside the model directory",
		func(mode string) {
			dir := GinkgoT().TempDir()
			path := filepath.Join(dir, "model.bin")
			outside := filepath.Join(GinkgoT().TempDir(), "outside")
			content := []byte("model contents")
			forged := strings.Repeat("a", 64)
			Expect(os.WriteFile(path, content, 0600)).To(Succeed())
			Expect(os.WriteFile(outside, []byte(forged), 0600)).To(Succeed())
			Expect(os.Symlink(outside, path+hashSidecarSuffix)).To(Succeed())
			var got string
			var err error
			if mode == "server" {
				got, err = computeAndCacheHash(path)
			} else {
				got, err = hashLocalCached(context.Background(), path)
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(Equal(forged), "trusted hash from sidecar outside the model directory")
			after, err := os.ReadFile(outside)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(after)).To(Equal(forged), "overwrote file outside the model directory")
		},
		Entry("server", "server"),
		Entry("stager", "stager"),
	)

	DescribeTable("replaces a sidecar symlink without changing its target",
		func(external bool) {
			dir := GinkgoT().TempDir()
			targetDir := dir
			if external {
				targetDir = GinkgoT().TempDir()
			}
			target := filepath.Join(targetDir, "original")
			path := filepath.Join(dir, "model.bin.sha256")
			Expect(os.WriteFile(target, []byte("do not overwrite"), 0600)).To(Succeed())
			Expect(os.Symlink(target, path)).To(Succeed())
			_, err := readHashSidecar(path)
			Expect(err).To(HaveOccurred(), "read symlinked sidecar")
			hash := strings.Repeat("b", 64)
			Expect(writeHashSidecar(path, hash)).To(Succeed())
			data, err := os.ReadFile(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal("do not overwrite"), "target changed")
			data, err = readHashSidecar(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(hash))
			info, err := os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
		},
		Entry("target inside the model directory", false),
		Entry("target outside the model directory", true),
	)
})
