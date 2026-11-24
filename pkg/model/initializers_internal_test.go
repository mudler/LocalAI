package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parallelSlotsFromOptions", func() {
	It("reads the parallel slot count from the backend options", func() {
		Expect(parallelSlotsFromOptions([]string{"use_jinja:true", "parallel:4"})).To(Equal("4"))
	})
	It("accepts the n_parallel alias", func() {
		Expect(parallelSlotsFromOptions([]string{"n_parallel:8"})).To(Equal("8"))
	})
	It("defaults to a single slot when unset", func() {
		Expect(parallelSlotsFromOptions([]string{"use_jinja:true"})).To(Equal("1"))
		Expect(parallelSlotsFromOptions(nil)).To(Equal("1"))
	})
})
