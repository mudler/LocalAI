package vrambudget_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/vrambudget"
)

const gb = uint64(1000 * 1000 * 1000)

var _ = Describe("Budget", func() {
	Describe("Parse", func() {
		It("treats empty/zero as unset (no error)", func() {
			for _, s := range []string{"", "  ", "0", "0%", "0b"} {
				b, err := vrambudget.Parse(s)
				Expect(err).NotTo(HaveOccurred())
				Expect(b.IsSet()).To(BeFalse(), "input %q", s)
			}
		})

		It("parses percentage forms", func() {
			for _, s := range []string{"80%", "0.8"} {
				b, err := vrambudget.Parse(s)
				Expect(err).NotTo(HaveOccurred())
				Expect(b.IsSet()).To(BeTrue())
				total, free := b.Apply(10*gb, 10*gb)
				Expect(total).To(Equal(8 * gb))
				Expect(free).To(Equal(8 * gb))
			}
		})

		It("treats 100% / 1.0 as a valid no-op ceiling", func() {
			b, err := vrambudget.Parse("100%")
			Expect(err).NotTo(HaveOccurred())
			total, free := b.Apply(10*gb, 6*gb)
			Expect(total).To(Equal(10 * gb))
			Expect(free).To(Equal(6 * gb))
		})

		It("parses absolute forms with decimal and binary suffixes and raw bytes", func() {
			cases := map[string]uint64{
				"12GB":        12 * gb,
				"12000MB":     12 * gb,
				"12000000000": 12 * gb,
				"12GiB":       12 * 1024 * 1024 * 1024,
			}
			for s, want := range cases {
				b, err := vrambudget.Parse(s)
				Expect(err).NotTo(HaveOccurred(), "input %q", s)
				Expect(b.Ceiling(64 * gb)).To(Equal(want), "input %q", s)
			}
		})

		It("rejects malformed and out-of-range input", func() {
			for _, s := range []string{"120%", "1.5", "-1", "abc", "12ZB", "%", "12 GB x"} {
				_, err := vrambudget.Parse(s)
				Expect(err).To(HaveOccurred(), "input %q", s)
			}
		})
	})

	Describe("Apply", func() {
		It("is a passthrough when unset", func() {
			b, _ := vrambudget.Parse("")
			total, free := b.Apply(16*gb, 9*gb)
			Expect(total).To(Equal(16 * gb))
			Expect(free).To(Equal(9 * gb))
		})

		It("caps total and free to the budget ceiling (hard ceiling)", func() {
			b, _ := vrambudget.Parse("12GB")
			total, free := b.Apply(16*gb, 15*gb)
			Expect(total).To(Equal(12 * gb))
			Expect(free).To(Equal(12 * gb))
		})

		It("never raises free above detected when free is already below budget", func() {
			b, _ := vrambudget.Parse("12GB")
			_, free := b.Apply(16*gb, 5*gb)
			Expect(free).To(Equal(5 * gb))
		})

		It("clamps an absolute budget above physical to detected", func() {
			b, _ := vrambudget.Parse("64GB")
			total, free := b.Apply(16*gb, 10*gb)
			Expect(total).To(Equal(16 * gb))
			Expect(free).To(Equal(10 * gb))
		})
	})

	Describe("String", func() {
		It("round-trips percentage and absolute canonical forms", func() {
			p, _ := vrambudget.Parse("80%")
			Expect(p.String()).To(Equal("80%"))
			a, _ := vrambudget.Parse("12000MB")
			Expect(a.String()).To(Equal("12GB"))
			z, _ := vrambudget.Parse("")
			Expect(z.String()).To(Equal(""))
		})
	})
})
