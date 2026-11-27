package studio

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the content of a plugin change", func() {
	It("is one JSON object of the plugins tool, with nothing after it", func() {
		_, err := parsePluginChange(`{"action":"install","plugin":"fmt@acme"}`)
		Expect(err).NotTo(HaveOccurred())
		_, err = parsePluginChange(`{"action":"install","plugin":"fmt@acme"} {"action":"remove","id":"i1"}`)
		Expect(err).To(HaveOccurred(), "a second object after the first")
		_, err = parsePluginChange(`{"action":"install","plugin":"fmt@acme"}x`)
		Expect(err).To(HaveOccurred(), "trailing data")
		_, err = parsePluginChange(`{"action":"browse"}`)
		Expect(err).To(HaveOccurred(), "a read action needs no confirmation")
	})
})
