// SPDX-License-Identifier: MIT
package systemone

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Decision admission leases", func() {
	It("fails promptly on saturation, respects cancellation and releases once", func() {
		var releases []func()
		defer func() {
			for _, r := range releases {
				r()
			}
		}()
		for i := 0; i < MaxAdmissions; i++ {
			r, err := AcquireAdmission(context.Background())
			Expect(err).NotTo(HaveOccurred())
			releases = append(releases, r)
		}
		_, err := AcquireAdmission(context.Background())
		Expect(err).To(MatchError(ErrAdmissionCapacity))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = AcquireAdmission(ctx)
		Expect(err).To(MatchError(context.Canceled))
		releases[0]()
		releases[0]()
		r, err := AcquireAdmission(context.Background())
		Expect(err).NotTo(HaveOccurred())
		r()
	})
})
