// enforceNotBefore is unexported, so its tests live in package
// cosignverify (alongside the external _test package's specs — both
// share Ginkgo's global registry, so the external suite's RunSpecs
// picks these up too).
package cosignverify

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

var _ = Describe("enforceNotBefore", func() {
	cutoff := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	makeResult := func(stamps ...time.Time) *verify.VerificationResult {
		res := &verify.VerificationResult{}
		for _, ts := range stamps {
			res.VerifiedTimestamps = append(res.VerifiedTimestamps, verify.TimestampVerificationResult{
				Type:      "Tlog",
				URI:       "https://rekor.sigstore.dev",
				Timestamp: ts,
			})
		}
		return res
	}

	It("accepts a signature newer than the cutoff", func() {
		Expect(enforceNotBefore(makeResult(cutoff.Add(time.Hour)), cutoff)).To(Succeed())
	})

	It("accepts a signature exactly at the cutoff", func() {
		Expect(enforceNotBefore(makeResult(cutoff), cutoff)).To(Succeed())
	})

	It("rejects a signature older than the cutoff", func() {
		err := enforceNotBefore(makeResult(cutoff.Add(-time.Hour)), cutoff)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("before NotBefore cutoff"))
	})

	It("rejects when the earliest of several timestamps predates the cutoff", func() {
		err := enforceNotBefore(makeResult(
			cutoff.Add(time.Hour),
			cutoff.Add(-time.Minute),
			cutoff.Add(2*time.Hour),
		), cutoff)
		Expect(err).To(HaveOccurred())
	})

	It("treats absent timestamps as a hard error", func() {
		err := enforceNotBefore(makeResult(), cutoff)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no verified timestamp"))
	})
})
