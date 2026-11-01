package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("profile", func() {
	It("names the binary by its absolute path", func() {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		runsc := filepath.Join(dir, "runsc")
		Expect(os.WriteFile(runsc, []byte("x"), 0o755)).To(Succeed())
		got, err := profile(runsc)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(ContainSubstring(`"` + runsc + `" flags=(unconfined)`))
		Expect(got).To(ContainSubstring("userns,"))
	})

	It("refuses what it cannot name", func() {
		dir := GinkgoT().TempDir()
		_, err := profile(filepath.Join(dir, "missing"))
		Expect(err).To(HaveOccurred(), "a missing file was accepted")
		_, err = profile(dir)
		Expect(err).To(HaveOccurred(), "a folder was accepted")
		odd := filepath.Join(dir, `a"b {`)
		Expect(os.WriteFile(odd, []byte("x"), 0o755)).To(Succeed())
		_, err = profile(odd)
		Expect(err).To(HaveOccurred(), "a path with a quote was accepted")
	})

	// New resolves symlinks before it checks the host, and AppArmor attaches a
	// profile by the real path, so the profile must name the file behind a link.
	It("names the file behind a symlink", func() {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		real := filepath.Join(dir, "runsc-real")
		link := filepath.Join(dir, "runsc")
		Expect(os.WriteFile(real, []byte("x"), 0o755)).To(Succeed())
		Expect(os.Symlink(real, link)).To(Succeed())
		got, err := profile(link)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(ContainSubstring(`"` + real + `" flags=(unconfined)`))
		Expect(got).ToNot(ContainSubstring(`"` + link + `"`))
	})
})
