package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("cosineDistance", func() {
	It("is 0 for identical vectors", func() {
		Expect(cosineDistance([]float32{1, 0, 0}, []float32{1, 0, 0})).To(BeNumerically("~", 0, 1e-6))
	})
	It("is ~1 for orthogonal vectors", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{0, 1})).To(BeNumerically("~", 1, 1e-6))
	})
	It("is ~2 for opposite vectors", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{-1, 0})).To(BeNumerically("~", 2, 1e-6))
	})
	It("returns 1 for length mismatch", func() {
		Expect(cosineDistance([]float32{1, 0}, []float32{1})).To(BeNumerically("~", 1, 1e-6))
	})
	It("returns 1 for a zero vector", func() {
		Expect(cosineDistance([]float32{0, 0}, []float32{1, 0})).To(BeNumerically("~", 1, 1e-6))
	})
})
