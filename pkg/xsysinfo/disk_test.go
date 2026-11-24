package xsysinfo_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
)

var _ = Describe("GetDiskInfo", func() {
	It("reports the filesystem holding an existing directory", func() {
		info, err := xsysinfo.GetDiskInfo(GinkgoT().TempDir())

		Expect(err).ToNot(HaveOccurred())
		Expect(info.Total).To(BeNumerically(">", 0))
		Expect(info.Available).To(BeNumerically("<=", info.Total))
	})

	It("measures the nearest existing ancestor when the models dir is not created yet", func() {
		// A fresh worker registers before its models directory exists; the
		// capacity report must still be a real reading rather than an error.
		notYet := filepath.Join(GinkgoT().TempDir(), "models", "subdir")

		info, err := xsysinfo.GetDiskInfo(notYet)

		Expect(err).ToNot(HaveOccurred())
		Expect(info.Total).To(BeNumerically(">", 0))
	})

	It("errors rather than guessing when given no path", func() {
		_, err := xsysinfo.GetDiskInfo("")
		Expect(err).To(HaveOccurred())
	})
})
