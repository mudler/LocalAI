//go:build !windows

package ociimage

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LockFile", func() {
	It("returns the context error while the lock is held", func() {
		p := filepath.Join(GinkgoT().TempDir(), "x.lock")
		unlock, err := LockFile(context.Background(), p)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err = LockFile(ctx, p)
		Expect(err).To(MatchError(context.DeadlineExceeded))
		unlock()
		again, err := LockFile(context.Background(), p)
		Expect(err).NotTo(HaveOccurred(), "after the release")
		again()
	})
})
