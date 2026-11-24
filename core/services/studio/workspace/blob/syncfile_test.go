// SPDX-License-Identifier: MIT
//go:build !darwin

package blob

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// On every platform but macOS the barrier is a plain Sync: it succeeds on an
// open file and fails on a closed one, as Sync does.
var _ = Describe("barrierSync off macOS", func() {
	It("is a plain Sync", func() {
		f, err := os.Create(filepath.Join(GinkgoT().TempDir(), "f"))
		Expect(err).NotTo(HaveOccurred())
		Expect(barrierSync(f)).To(Succeed())
		Expect(f.Close()).To(Succeed())
		Expect(barrierSync(f)).NotTo(Succeed())
	})
})
