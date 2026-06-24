package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("config revision options", func() {
	It("captures the revision supplied by the resolved model config", func() {
		opts := NewOptions(WithConfigRevision("revision-a"))
		Expect(opts.configRevision).To(Equal("revision-a"))
	})
})
