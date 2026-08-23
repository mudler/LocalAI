package model

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend process state directory", func() {
	It("reports why the state directory could not be created", func() {
		// A worker whose volume is full, or whose TMPDIR no longer resolves,
		// cannot get a state directory. go-processmanager's New() drops the
		// option error, leaving StateDir empty, and Run() then failed with
		// "mkdir : no such file or directory" naming no path at all. Resolving
		// the directory here keeps the real cause attached.
		GinkgoT().Setenv("TMPDIR", filepath.Join(GinkgoT().TempDir(), "does-not-exist"))

		dir, err := newProcessStateDir()
		Expect(err).To(HaveOccurred())
		Expect(dir).To(BeEmpty())
		Expect(err.Error()).To(ContainSubstring("backend process state directory"))
		Expect(err.Error()).To(ContainSubstring("does-not-exist"),
			"the error must name the directory it could not create")
	})

	It("returns a usable directory when the temp location works", func() {
		GinkgoT().Setenv("TMPDIR", GinkgoT().TempDir())

		dir, err := newProcessStateDir()
		Expect(err).ToNot(HaveOccurred())
		Expect(dir).ToNot(BeEmpty())
		info, statErr := os.Stat(dir)
		Expect(statErr).ToNot(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())
	})
})
