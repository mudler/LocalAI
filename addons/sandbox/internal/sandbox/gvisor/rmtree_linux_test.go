//go:build linux

package gvisor

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("removeAll", func() {
	It("empties a read-only folder", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "x")
		Expect(os.MkdirAll(filepath.Join(dir, "ro", "sub"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "ro", "sub", "f"), []byte("x"), 0o444)).To(Succeed())
		Expect(os.Chmod(filepath.Join(dir, "ro", "sub"), 0o555)).To(Succeed())
		Expect(os.Chmod(filepath.Join(dir, "ro"), 0o555)).To(Succeed())
		Expect(removeAll(dir)).To(Succeed())
		_, err := os.Stat(dir)
		Expect(os.IsNotExist(err)).To(BeTrue(), "folder still there: %v", err)
	})
})
