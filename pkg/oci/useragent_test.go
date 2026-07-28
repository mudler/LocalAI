package oci_test

import (
	"github.com/mudler/LocalAI/internal"
	. "github.com/mudler/LocalAI/pkg/oci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OCI", func() {
	Context("UserAgent", func() {
		var savedVersion string

		BeforeEach(func() {
			savedVersion = internal.Version
		})

		AfterEach(func() {
			internal.Version = savedVersion
		})

		It("identifies as LocalAI when no version is stamped", func() {
			internal.Version = ""
			Expect(UserAgent()).To(Equal("LocalAI"))
		})

		It("appends the build version when one is stamped", func() {
			internal.Version = "v3.2.1"
			Expect(UserAgent()).To(Equal("LocalAI/v3.2.1"))
		})
	})
})
