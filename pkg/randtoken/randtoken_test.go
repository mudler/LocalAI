// SPDX-License-Identifier: MIT
package randtoken_test

import (
	"regexp"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/randtoken"
)

func TestRandtoken(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Randtoken Suite")
}

var _ = Describe("randtoken", func() {
	It("returns a base64url token of the requested size", func() {
		token, err := randtoken.Base64URL(32)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(MatchRegexp(`^[A-Za-z0-9_-]{43}$`))
	})

	It("returns a hex token of the requested size", func() {
		token, err := randtoken.Hex(16)
		Expect(err).ToNot(HaveOccurred())
		Expect(regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(token)).To(BeTrue())
	})

	It("returns a different token each time", func() {
		first, err := randtoken.Hex(16)
		Expect(err).ToNot(HaveOccurred())
		second, err := randtoken.Hex(16)
		Expect(err).ToNot(HaveOccurred())
		Expect(first).ToNot(Equal(second))
	})
})
