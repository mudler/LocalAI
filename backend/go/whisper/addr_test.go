package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Specs for resolveAddr run under the suite bootstrap in gowhisper_test.go
// (TestWhisper); they need no native library, so they never skip.
var _ = Describe("resolveAddr", func() {
	It("prefers an explicitly set -addr over a positional argument", func() {
		Expect(resolveAddr("127.0.0.1:12345", true, []string{"127.0.0.1:59999"})).To(Equal("127.0.0.1:12345"))
	})

	It("keeps an explicit -addr equal to the default over a positional argument", func() {
		Expect(resolveAddr(defaultAddr, true, []string{"127.0.0.1:59999"})).To(Equal(defaultAddr))
	})

	It("falls back to the positional argument when -addr is unset", func() {
		Expect(resolveAddr(defaultAddr, false, []string{"127.0.0.1:59999"})).To(Equal("127.0.0.1:59999"))
	})

	It("keeps the default when -addr is unset and no positional argument is given", func() {
		Expect(resolveAddr(defaultAddr, false, nil)).To(Equal(defaultAddr))
	})

	It("uses the first positional argument among several", func() {
		Expect(resolveAddr(defaultAddr, false, []string{"127.0.0.1:59999", "extra"})).To(Equal("127.0.0.1:59999"))
	})

	It("treats an explicitly empty -addr as unset", func() {
		Expect(resolveAddr("", true, []string{"127.0.0.1:59999"})).To(Equal("127.0.0.1:59999"))
		Expect(resolveAddr("", true, nil)).To(Equal(defaultAddr))
	})
})
