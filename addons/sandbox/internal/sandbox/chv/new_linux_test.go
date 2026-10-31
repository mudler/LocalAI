//go:build linux

package chv

import (
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("New", func() {
	It("refuses bad directories", func() {
		long := filepath.Join(GinkgoT().TempDir(), strings.Repeat("x", 100))
		_, err := New(Options{StateDir: long, CacheDir: GinkgoT().TempDir()})
		Expect(err).To(MatchError(ContainSubstring("too long")), "long state dir")
		comma := filepath.Join(GinkgoT().TempDir(), "a,b")
		_, err = New(Options{StateDir: GinkgoT().TempDir(), CacheDir: comma})
		Expect(err).To(MatchError(ContainSubstring("comma")), "comma")
	})
})
