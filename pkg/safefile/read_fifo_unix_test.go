//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package safefile_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"

	"github.com/mudler/LocalAI/pkg/safefile"
)

var _ = Describe("ReadRegularAt Unix special files", func() {
	It("rejects a FIFO without blocking for a writer", func() {
		dir := GinkgoT().TempDir()
		Expect(unix.Mkfifo(filepath.Join(dir, "model.yaml"), 0o600)).To(Succeed())
		_, _, err := safefile.ReadRegularAt(dir, "model.yaml")
		Expect(err).To(MatchError(ContainSubstring("not a regular file")))
	})
})
