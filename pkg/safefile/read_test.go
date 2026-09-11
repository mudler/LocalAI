package safefile_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/safefile"
)

var _ = Describe("ReadRegularAt", func() {
	It("reads a regular direct child", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "model.yaml"), []byte("safe"), 0o640)).To(Succeed())
		data, mode, err := safefile.ReadRegularAt(dir, "model.yaml")
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal([]byte("safe")))
		Expect(mode).To(Equal(os.FileMode(0o640)))
	})

	It("rejects symbolic links", func() {
		dir := GinkgoT().TempDir()
		target := filepath.Join(dir, "target")
		Expect(os.WriteFile(target, []byte("secret"), 0o600)).To(Succeed())
		Expect(os.Symlink(target, filepath.Join(dir, "model.yaml"))).To(Succeed())
		_, _, err := safefile.ReadRegularAt(dir, "model.yaml")
		Expect(err).To(HaveOccurred())
	})

	It("rejects non-regular files without reading them", func() {
		dir := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(dir, "model.yaml"), 0o700)).To(Succeed())
		_, _, err := safefile.ReadRegularAt(dir, "model.yaml")
		Expect(err).To(MatchError(ContainSubstring("not a regular file")))
	})
})
