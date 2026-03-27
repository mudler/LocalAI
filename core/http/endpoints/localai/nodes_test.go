package localai

import (
	"crypto/sha256"
	"crypto/subtle"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("token validation",
	func(expectedToken, providedToken string, wantMatch bool) {
		if expectedToken == "" {
			// No auth required — always matches
			Expect(wantMatch).To(BeTrue(), "no-auth should always pass")
			return
		}

		if providedToken == "" {
			Expect(wantMatch).To(BeFalse(), "empty token should be rejected")
			return
		}

		expectedHash := sha256.Sum256([]byte(expectedToken))
		providedHash := sha256.Sum256([]byte(providedToken))
		match := subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) == 1

		Expect(match).To(Equal(wantMatch))
	},
	Entry("matching tokens", "my-secret-token", "my-secret-token", true),
	Entry("mismatched tokens", "my-secret-token", "wrong-token", false),
	Entry("empty expected (no auth)", "", "any-token", true),
	Entry("empty provided when expected set", "my-secret-token", "", false),
)
