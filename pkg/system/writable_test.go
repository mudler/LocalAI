package system

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CheckWritable", func() {
	It("passes folders that can be written and leaves no file behind", func() {
		dir := GinkgoT().TempDir()
		Expect(CheckWritable([]Folder{{Name: "data", Path: dir}})).To(BeEmpty())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("checks a missing folder at the nearest folder above it, without making it", func() {
		dir := GinkgoT().TempDir()
		missing := filepath.Join(dir, "a", "b")
		Expect(CheckWritable([]Folder{{Name: "models", Path: missing}})).To(BeEmpty())
		Expect(filepath.Join(dir, "a")).NotTo(BeADirectory())
	})

	It("names a folder that cannot be written, with its owner", func() {
		if runtime.GOOS == "windows" {
			Skip("permission bits do not stop writes on Windows")
		}
		dir := GinkgoT().TempDir()
		ro := filepath.Join(dir, "ro")
		Expect(os.Mkdir(ro, 0o500)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(ro, 0o700) })
		if probeWrite(ro) == nil {
			Skip("the test runs as a user that can write a read-only folder (root)")
		}
		bad := CheckWritable([]Folder{
			{Name: "models", Path: ro},
			{Name: "generated", Path: filepath.Join(ro, "generated")},
			{Name: "same", Path: ro + "/"},
			{Name: "empty"},
			{Name: "data", Path: dir},
		})
		Expect(bad).To(HaveLen(2))
		Expect(bad[0].Name).To(Equal("models"))
		Expect(bad[0].OwnerUID).To(Equal(os.Getuid()))
		Expect(bad[0].Err).To(HaveOccurred())
		Expect(bad[1].Name).To(Equal("generated"))
		Expect(FormatUnwritable(bad)).To(Equal(
			"models=" + ro + " (owner uid " + itoa(os.Getuid()) + "), generated=" + filepath.Join(ro, "generated") + " (owner uid " + itoa(os.Getuid()) + ")"))
	})
})
var itoa = strconv.Itoa
