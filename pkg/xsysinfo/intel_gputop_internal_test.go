package xsysinfo

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("intelGPUTopArgs", func() {
	It("requests one JSON sample instead of an infinite 1ms refresh loop", func() {
		Expect(intelGPUTopArgs()).To(Equal([]string{"-J", "-n", "1"}))
	})
})
