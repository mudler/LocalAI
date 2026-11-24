//go:build !(darwin && arm64)

package vmm

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVmm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Virtualization.framework binding")
}

// On a system without Virtualization.framework every entry point says so and
// nothing blocks.
var _ = Describe("a system without Virtualization.framework", func() {
	It("refuses every call and never blocks", func() {
		Expect(Check()).To(MatchError(ErrUnsupported))
		_, err := New(Config{})
		Expect(err).To(MatchError(ErrUnsupported))
		var v VM
		Expect(v.Start(context.Background())).To(MatchError(ErrUnsupported))
		_, err = v.Dial(context.Background(), 1024)
		Expect(err).To(MatchError(ErrUnsupported))
		Expect(v.Listen(1024, nil)).To(MatchError(ErrUnsupported))
		Expect(v.Done()).To(BeClosed())
		v.Stop()
		Expect(Limits()).To(Equal(HostLimits{}))
	})
})
