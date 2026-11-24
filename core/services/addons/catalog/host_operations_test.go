// SPDX-License-Identifier: MIT
package catalog

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("host operations hint of an index entry", func() {
	It("tells an absent hint from a manifest that requests nothing", func() {
		none, err := DecodeIndex([]byte(yamlEntry))
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(HaveLen(1))
		Expect(none[0].HostOperations).To(BeNil())

		empty, err := DecodeIndex([]byte(yamlEntry + "  hostOperations: {}\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(empty[0].HostOperations).NotTo(BeNil())
		Expect(empty[0].HostOperations.Required).To(BeEmpty())
		Expect(empty[0].HostOperations.Optional).To(BeEmpty())
	})
	It("reads the required and optional host operations of an entry", func() {
		got, err := DecodeIndex([]byte(yamlEntry + "  hostOperations:\n    required:\n      - \"a.b\"\n    optional:\n      - \"c.d\"\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].HostOperations).NotTo(BeNil())
		Expect(got[0].HostOperations.Required).To(Equal([]string{"a.b"}))
		Expect(got[0].HostOperations.Optional).To(Equal([]string{"c.d"}))
	})
})
