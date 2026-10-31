//go:build linux || darwin

package host

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

var _ = Describe("New as root", func() {
	It("never runs as root", func() {
		old := geteuid
		geteuid = func() int { return 0 }
		defer func() { geteuid = old }()
		b, err := New(Options{Root: GinkgoT().TempDir()})
		Expect(b).To(BeNil())
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
	})
})
