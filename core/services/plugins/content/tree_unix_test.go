//go:build unix

package content_test

import (
	"path/filepath"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins/content"
)

var _ = Describe("Check on Unix", func() {
	It("refuses a named pipe", func() {
		dir := tree(map[string]string{"a.md": "a"})
		Expect(syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644)).To(Succeed())
		refusedWith(checkErr(dir, content.DefaultLimits()), content.CodeSpecialFile)
	})
})
