package sound

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Float32sToInt16LEBytes", func() {
	It("converts in-range samples to int16 little-endian bytes", func() {
		out := Float32sToInt16LEBytes([]float32{0, 0.5, -0.5})
		Expect(BytesToInt16sLE(out)).To(Equal([]int16{0, 16383, -16383}))
	})

	It("clamps out-of-range samples instead of wrapping", func() {
		out := Float32sToInt16LEBytes([]float32{2.0, -2.0})
		Expect(out).To(Equal([]byte{0xff, 0x7f, 0x00, 0x80})) // 32767, -32768
	})

	It("returns an empty slice for empty input", func() {
		Expect(Float32sToInt16LEBytes(nil)).To(BeEmpty())
	})
})
