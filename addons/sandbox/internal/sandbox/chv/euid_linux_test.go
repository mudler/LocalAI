//go:build linux

package chv

import (
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("New", func() {
	// It checks that the backend refuses to run as root on the host.
	It("refuses to run as root", func() {
		old := geteuid
		geteuid = func() int { return 0 }
		defer func() { geteuid = old }()
		_, err := New(Options{StateDir: GinkgoT().TempDir(), CacheDir: GinkgoT().TempDir()})
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
	})
})
