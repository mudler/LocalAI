//go:build linux

package chv

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("New", func() {
	It("refuses a negative stage limit", func() {
		_, err := New(Options{StateDir: GinkgoT().TempDir(), CacheDir: GinkgoT().TempDir(), StageLimit: -1})
		Expect(err).To(MatchError(ContainSubstring("StageLimit")))
	})
})
