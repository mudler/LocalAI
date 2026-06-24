package nodes

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stale revision error detail", func() {
	It("keeps errors.Is matching so callers can still classify it", func() {
		err := fmt.Errorf("%w (request carries %s, controller holds %s)",
			ErrStaleModelConfigRevision, shortRevision("aaaabbbbccccdddd"), shortRevision("1111222233334444"))
		Expect(errors.Is(err, ErrStaleModelConfigRevision)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("stale model config revision"))
	})

	It("names both revisions so an operator can tell which side moved", func() {
		Expect(shortRevision("aaaabbbbccccdddd")).To(Equal("aaaabbbbcccc"))
		Expect(shortRevision("short")).To(Equal("short"))
		Expect(shortRevision("")).To(Equal("(none)"))
	})
})
