package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("Candidate.EffectiveMinVRAM", func() {
	It("reports no floor when neither authored nor inferred", func() {
		c := gallery.Candidate{Model: "x"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeFalse())
		Expect(bytes).To(Equal(uint64(0)))
	})

	It("uses the inferred value when nothing is authored", func() {
		c := gallery.Candidate{Model: "x", InferredMinVRAM: "6GiB"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeTrue())
		Expect(bytes).To(Equal(uint64(6 * 1024 * 1024 * 1024)))
	})

	It("lets an authored value win over an inferred one", func() {
		c := gallery.Candidate{Model: "x", MinVRAM: "20GiB", InferredMinVRAM: "6GiB"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeTrue())
		Expect(bytes).To(Equal(uint64(20 * 1024 * 1024 * 1024)))
	})

	It("errors on an unparseable floor rather than silently treating it as absent", func() {
		c := gallery.Candidate{Model: "x", MinVRAM: "twenty gigs"}
		_, _, err := c.EffectiveMinVRAM()
		Expect(err).To(HaveOccurred())
	})
})
