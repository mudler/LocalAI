package oci_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/oci"
)

var _ = Describe("SweepStagingDir", func() {
	It("removes the partial downloads a crash left and nothing else", func() {
		dir := GinkgoT().TempDir()
		partial := filepath.Join(dir, "localai-oci-123456")
		Expect(os.MkdirAll(partial, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(partial, "image-1.tar"), make([]byte, 4096), 0o600)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "other"), 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "localai-oci-file"), []byte("x"), 0o600)).To(Succeed())

		Expect(oci.SweepStagingDir(dir)).To(Equal([]string{"localai-oci-123456"}))
		Expect(partial).NotTo(BeAnExistingFile())
		Expect(filepath.Join(dir, "other")).To(BeADirectory())
		Expect(filepath.Join(dir, "localai-oci-file")).To(BeAnExistingFile(), "only the directories of a download")
	})

	It("does nothing without a staging directory", func() {
		Expect(oci.SweepStagingDir("")).To(BeEmpty())
		Expect(oci.SweepStagingDir(filepath.Join(GinkgoT().TempDir(), "missing"))).To(BeEmpty())
	})
})
